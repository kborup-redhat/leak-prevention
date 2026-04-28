package db_test

import (
	"database/sql"
	"testing"

	"github.com/kborup-redhat/leak-prevention/internal/db"
	_ "modernc.org/sqlite"
)

func setupAllowlistDB(t *testing.T) *db.AllowlistDB {
	t.Helper()
	sqlDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqlDB.Close() })

	adb, err := db.NewAllowlistDB(sqlDB)
	if err != nil {
		t.Fatal(err)
	}
	return adb
}

func TestAllowlist_AddAndCheck(t *testing.T) {
	adb := setupAllowlistDB(t)
	if adb.IsAllowed("Shell") {
		t.Fatal("Shell should not be allowed before adding")
	}
	if err := adb.Add("Shell"); err != nil {
		t.Fatal(err)
	}
	if !adb.IsAllowed("Shell") {
		t.Fatal("Shell should be allowed after adding")
	}
}

func TestAllowlist_CaseInsensitive(t *testing.T) {
	adb := setupAllowlistDB(t)
	if err := adb.Add("Shell"); err != nil {
		t.Fatal(err)
	}
	if !adb.IsAllowed("shell") {
		t.Fatal("allowlist check should be case-insensitive")
	}
	if !adb.IsAllowed("SHELL") {
		t.Fatal("allowlist check should be case-insensitive")
	}
}

func TestAllowlist_List(t *testing.T) {
	adb := setupAllowlistDB(t)
	adb.Add("Shell")
	adb.Add("Meta")
	adb.Add("WHO")
	terms, err := adb.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(terms) != 3 {
		t.Errorf("expected 3 terms, got %d", len(terms))
	}
}

func TestAllowlist_Remove(t *testing.T) {
	adb := setupAllowlistDB(t)
	adb.Add("Shell")
	if err := adb.Remove("Shell"); err != nil {
		t.Fatal(err)
	}
	if adb.IsAllowed("Shell") {
		t.Fatal("Shell should not be allowed after removal")
	}
}

func TestAllowlist_RemoveCaseInsensitive(t *testing.T) {
	adb := setupAllowlistDB(t)
	adb.Add("Shell")
	if err := adb.Remove("shell"); err != nil {
		t.Fatal(err)
	}
	if adb.IsAllowed("Shell") {
		t.Fatal("case-insensitive removal should work")
	}
}

func TestAllowlist_DuplicateAdd(t *testing.T) {
	adb := setupAllowlistDB(t)
	if err := adb.Add("Shell"); err != nil {
		t.Fatal(err)
	}
	err := adb.Add("Shell")
	if err != nil {
		t.Fatal("duplicate add should not error (upsert)")
	}
	terms, _ := adb.List()
	if len(terms) != 1 {
		t.Errorf("expected 1 term after duplicate add, got %d", len(terms))
	}
}

func TestAllowlist_Count(t *testing.T) {
	adb := setupAllowlistDB(t)
	adb.Add("Shell")
	adb.Add("Meta")
	if count := adb.Count(); count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}
