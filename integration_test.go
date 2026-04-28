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
	t.Cleanup(func() { watchSQL.Close() })

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
	t.Cleanup(func() { allowSQL.Close() })

	wdb := db.NewWatchlistDB(watchSQL)
	adb, err := db.NewAllowlistDB(allowSQL)
	if err != nil {
		t.Fatal(err)
	}
	m := matcher.New(wdb, adb)
	router := api.NewRouter(m, wdb, adb)

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
	defer resp.Body.Close()

	var result struct {
		Blocked bool `json:"blocked"`
		Matches []struct {
			Name   string `json:"name"`
			Parent string `json:"parent"`
		} `json:"matches"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

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
	defer resp.Body.Close()

	var result struct {
		Blocked bool `json:"blocked"`
	}
	json.NewDecoder(resp.Body).Decode(&result)

	if result.Blocked {
		t.Fatal("expected allowed")
	}
}

func TestIntegration_AllowlistFlow(t *testing.T) {
	srv := setupIntegrationServer(t)
	defer srv.Close()

	// Step 1: Check blocked
	body := `{"prompt":"Deploy to AWS"}`
	resp, _ := http.Post(srv.URL+"/check", "application/json", bytes.NewBufferString(body))
	var r1 struct{ Blocked bool }
	json.NewDecoder(resp.Body).Decode(&r1)
	resp.Body.Close()
	if !r1.Blocked {
		t.Fatal("step 1: expected blocked")
	}

	// Step 2: Add to allowlist
	body = `{"term":"AWS"}`
	resp, _ = http.Post(srv.URL+"/allowlist", "application/json", bytes.NewBufferString(body))
	if resp.StatusCode != 201 {
		t.Fatalf("step 2: expected 201, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Step 3: Check again — should pass
	body = `{"prompt":"Deploy to AWS"}`
	resp, _ = http.Post(srv.URL+"/check", "application/json", bytes.NewBufferString(body))
	var r2 struct{ Blocked bool }
	json.NewDecoder(resp.Body).Decode(&r2)
	resp.Body.Close()
	if r2.Blocked {
		t.Fatal("step 3: expected allowed after allowlisting")
	}

	// Step 4: List allowlist
	resp, _ = http.Get(srv.URL + "/allowlist")
	var list struct{ Terms []string }
	json.NewDecoder(resp.Body).Decode(&list)
	resp.Body.Close()
	if len(list.Terms) != 1 || list.Terms[0] != "AWS" {
		t.Errorf("step 4: unexpected terms: %v", list.Terms)
	}

	// Step 5: Delete from allowlist
	req, _ := http.NewRequest("DELETE", srv.URL+"/allowlist/AWS", nil)
	resp, _ = http.DefaultClient.Do(req)
	if resp.StatusCode != 204 {
		t.Fatalf("step 5: expected 204, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Step 6: Check again — should block
	body = `{"prompt":"Deploy to AWS"}`
	resp, _ = http.Post(srv.URL+"/check", "application/json", bytes.NewBufferString(body))
	var r3 struct{ Blocked bool }
	json.NewDecoder(resp.Body).Decode(&r3)
	resp.Body.Close()
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
	defer resp.Body.Close()

	var health struct {
		Status         string `json:"status"`
		WatchlistCount int    `json:"watchlist_count"`
		AliasCount     int    `json:"alias_count"`
	}
	json.NewDecoder(resp.Body).Decode(&health)

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
