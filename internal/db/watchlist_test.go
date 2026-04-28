package db_test

import (
	"database/sql"
	"testing"

	"github.com/kborup-redhat/leak-prevention/internal/db"
	_ "modernc.org/sqlite"
)

func setupWatchlistDB(t *testing.T) *db.WatchlistDB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })

	_, err = sqlDB.Exec(`
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
		INSERT INTO companies (id, name, category) VALUES (2, 'Alphabet', 'FORTUNE 500 (US)');
		INSERT INTO aliases (company_id, alias) VALUES (1, 'AWS');
		INSERT INTO aliases (company_id, alias) VALUES (1, 'Amazon Web Services');
		INSERT INTO aliases (company_id, alias) VALUES (2, 'Google');
		INSERT INTO aliases (company_id, alias) VALUES (2, 'Google Cloud');
		INSERT INTO aliases (company_id, alias) VALUES (2, 'YouTube');
	`)
	if err != nil {
		t.Fatal(err)
	}

	return db.NewWatchlistDB(sqlDB)
}

func TestFindCompany_ExactMatch(t *testing.T) {
	wdb := setupWatchlistDB(t)
	match, found := wdb.FindCompany("Amazon")
	if !found {
		t.Fatal("expected to find Amazon")
	}
	if match.Name != "Amazon" || match.Parent != "" || match.Category != "FORTUNE 500 (US)" {
		t.Errorf("unexpected match: %+v", match)
	}
}

func TestFindCompany_AliasMatch(t *testing.T) {
	wdb := setupWatchlistDB(t)
	match, found := wdb.FindCompany("AWS")
	if !found {
		t.Fatal("expected to find AWS")
	}
	if match.Name != "AWS" || match.Parent != "Amazon" || match.Category != "FORTUNE 500 (US)" {
		t.Errorf("unexpected match: %+v", match)
	}
}

func TestFindCompany_CaseInsensitive(t *testing.T) {
	wdb := setupWatchlistDB(t)
	match, found := wdb.FindCompany("google cloud")
	if !found {
		t.Fatal("expected to find google cloud (case-insensitive)")
	}
	if match.Parent != "Alphabet" {
		t.Errorf("expected parent Alphabet, got %s", match.Parent)
	}
}

func TestFindCompany_NotFound(t *testing.T) {
	wdb := setupWatchlistDB(t)
	_, found := wdb.FindCompany("Nonexistent Corp")
	if found {
		t.Fatal("expected not to find Nonexistent Corp")
	}
}

func TestCompanyCount(t *testing.T) {
	wdb := setupWatchlistDB(t)
	count := wdb.CompanyCount()
	if count != 2 {
		t.Errorf("expected 2 companies, got %d", count)
	}
}

func TestAliasCount(t *testing.T) {
	wdb := setupWatchlistDB(t)
	count := wdb.AliasCount()
	if count != 5 {
		t.Errorf("expected 5 aliases, got %d", count)
	}
}
