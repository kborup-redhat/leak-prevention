package api_test

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kborup-redhat/leak-prevention/internal/api"
	"github.com/kborup-redhat/leak-prevention/internal/db"
	"github.com/kborup-redhat/leak-prevention/internal/matcher"
	_ "modernc.org/sqlite"
)

func setupServer(t *testing.T) http.Handler {
	t.Helper()

	watchSQL, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watchSQL.Close() })

	_, err = watchSQL.Exec(`
		CREATE TABLE companies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			category TEXT NOT NULL
		);
		CREATE TABLE aliases (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			company_id INTEGER NOT NULL REFERENCES companies(id),
			alias TEXT NOT NULL
		);
		CREATE INDEX idx_companies_name ON companies(name COLLATE NOCASE);
		CREATE INDEX idx_aliases_alias ON aliases(alias COLLATE NOCASE);
		INSERT INTO companies (id, name, category) VALUES (1, 'Amazon', 'FORTUNE 500 (US)');
		INSERT INTO aliases (company_id, alias) VALUES (1, 'AWS');
	`)
	if err != nil {
		t.Fatal(err)
	}

	allowSQL, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = allowSQL.Close() })

	wdb := db.NewWatchlistDB(watchSQL)
	adb, err := db.NewAllowlistDB(allowSQL)
	if err != nil {
		t.Fatal(err)
	}

	customSQL, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = customSQL.Close() })
	cwdb, err := db.NewCustomWatchlistDB(customSQL)
	if err != nil {
		t.Fatal(err)
	}

	m := matcher.New(wdb, adb)
	m.SetCustomWatchlist(cwdb)

	return api.NewRouter(m, wdb, adb, cwdb)
}

func TestCheckEndpoint_Blocked(t *testing.T) {
	srv := setupServer(t)

	body := `{"prompt": "Deploy to AWS"}`
	req := httptest.NewRequest(http.MethodPost, "/check", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var result matcher.Result
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if !result.Blocked {
		t.Fatal("expected blocked")
	}
	if len(result.Matches) == 0 {
		t.Fatal("expected matches")
	}
}

func TestCheckEndpoint_Allowed(t *testing.T) {
	srv := setupServer(t)

	body := `{"prompt": "Write a hello world function"}`
	req := httptest.NewRequest(http.MethodPost, "/check", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var result matcher.Result
	if err := json.NewDecoder(rec.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if result.Blocked {
		t.Fatalf("expected not blocked, matches: %+v", result.Matches)
	}
}

func TestAllowlistEndpoint_AddAndList(t *testing.T) {
	srv := setupServer(t)

	body := `{"term": "Shell"}`
	req := httptest.NewRequest(http.MethodPost, "/allowlist", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/allowlist", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var listResp struct {
		Terms []string `json:"terms"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Terms) != 1 || listResp.Terms[0] != "Shell" {
		t.Errorf("unexpected terms: %v", listResp.Terms)
	}
}

func TestAllowlistEndpoint_Delete(t *testing.T) {
	srv := setupServer(t)

	body := `{"term": "Shell"}`
	req := httptest.NewRequest(http.MethodPost, "/allowlist", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	req = httptest.NewRequest(http.MethodDelete, "/allowlist/Shell", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "/allowlist", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var listResp struct {
		Terms []string `json:"terms"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&listResp); err != nil {
		t.Fatal(err)
	}
	if len(listResp.Terms) != 0 {
		t.Errorf("expected empty allowlist after delete, got: %v", listResp.Terms)
	}
}

func TestHealthEndpoint(t *testing.T) {
	srv := setupServer(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var health struct {
		Status         string `json:"status"`
		WatchlistCount int    `json:"watchlist_count"`
		AliasCount     int    `json:"alias_count"`
		AllowlistCount int    `json:"allowlist_count"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}
	if health.Status != "ok" {
		t.Errorf("expected status ok, got %s", health.Status)
	}
	if health.WatchlistCount != 1 {
		t.Errorf("expected watchlist_count 1, got %d", health.WatchlistCount)
	}
}
