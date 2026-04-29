package main_test

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

func setupIntegrationServer(t *testing.T) *httptest.Server {
	t.Helper()

	watchSQL, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = watchSQL.Close() })

	_, err = watchSQL.Exec(`
		CREATE TABLE companies (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, category TEXT NOT NULL);
		CREATE TABLE aliases (id INTEGER PRIMARY KEY AUTOINCREMENT, company_id INTEGER NOT NULL, alias TEXT NOT NULL);
		CREATE INDEX idx_companies_name ON companies(name COLLATE NOCASE);
		CREATE INDEX idx_aliases_alias ON aliases(alias COLLATE NOCASE);
		INSERT INTO companies (id, name, category) VALUES
			(1, 'Amazon', 'FORTUNE 500 (US)'),
			(2, 'Alphabet', 'FORTUNE 500 (US)'),
			(3, 'Microsoft', 'FORTUNE 500 (US)');
		INSERT INTO aliases (company_id, alias) VALUES
			(1, 'AWS'), (1, 'Amazon Web Services'),
			(2, 'Google'), (2, 'Google Cloud'), (2, 'YouTube'),
			(3, 'Azure'), (3, 'LinkedIn');
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
	router := api.NewRouter(m, wdb, adb, cwdb)

	return httptest.NewServer(router)
}

func TestIntegration_CheckBlocked(t *testing.T) {
	srv := setupIntegrationServer(t)
	defer srv.Close()

	body := `{"prompt":"Deploy to AWS and use Google Cloud for failover"}`
	resp, err := http.Post(srv.URL+"/check", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Blocked bool `json:"blocked"`
		Matches []struct {
			Name   string `json:"name"`
			Parent string `json:"parent"`
		} `json:"matches"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if !result.Blocked {
		t.Fatal("expected blocked")
	}
	if len(result.Matches) < 2 {
		t.Errorf("expected at least 2 matches, got %d", len(result.Matches))
	}
}

func TestIntegration_CheckAllowed(t *testing.T) {
	srv := setupIntegrationServer(t)
	defer srv.Close()

	body := `{"prompt":"Write a function that sorts an array"}`
	resp, err := http.Post(srv.URL+"/check", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result struct {
		Blocked bool `json:"blocked"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}

	if result.Blocked {
		t.Fatal("expected allowed")
	}
}

func TestIntegration_AllowlistFlow(t *testing.T) {
	srv := setupIntegrationServer(t)
	defer srv.Close()

	body := `{"prompt":"Deploy to AWS"}`
	resp, err := http.Post(srv.URL+"/check", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	var r1 struct{ Blocked bool }
	if err := json.NewDecoder(resp.Body).Decode(&r1); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !r1.Blocked {
		t.Fatal("step 1: expected blocked")
	}

	body = `{"term":"AWS"}`
	resp, err = http.Post(srv.URL+"/allowlist", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 201 {
		t.Fatalf("step 2: expected 201, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	body = `{"prompt":"Deploy to AWS"}`
	resp, err = http.Post(srv.URL+"/check", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	var r2 struct{ Blocked bool }
	if err := json.NewDecoder(resp.Body).Decode(&r2); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if r2.Blocked {
		t.Fatal("step 3: expected allowed after allowlisting")
	}

	resp, err = http.Get(srv.URL + "/allowlist")
	if err != nil {
		t.Fatal(err)
	}
	var list struct{ Terms []string }
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(list.Terms) != 1 || list.Terms[0] != "AWS" {
		t.Errorf("step 4: unexpected terms: %v", list.Terms)
	}

	req, err := http.NewRequest("DELETE", srv.URL+"/allowlist/AWS", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 204 {
		t.Fatalf("step 5: expected 204, got %d", resp.StatusCode)
	}
	_ = resp.Body.Close()

	body = `{"prompt":"Deploy to AWS"}`
	resp, err = http.Post(srv.URL+"/check", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	var r3 struct{ Blocked bool }
	if err := json.NewDecoder(resp.Body).Decode(&r3); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if !r3.Blocked {
		t.Fatal("step 6: expected blocked after removing from allowlist")
	}
}

func TestIntegration_Health(t *testing.T) {
	srv := setupIntegrationServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var health struct {
		Status         string `json:"status"`
		WatchlistCount int    `json:"watchlist_count"`
		AliasCount     int    `json:"alias_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&health); err != nil {
		t.Fatal(err)
	}

	if health.Status != "ok" {
		t.Errorf("expected ok, got %s", health.Status)
	}
	if health.WatchlistCount != 3 {
		t.Errorf("expected 3 companies, got %d", health.WatchlistCount)
	}
	if health.AliasCount != 7 {
		t.Errorf("expected 7 aliases, got %d", health.AliasCount)
	}
}
