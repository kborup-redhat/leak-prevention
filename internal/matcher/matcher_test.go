package matcher_test

import (
	"database/sql"
	"testing"

	"github.com/kborup-redhat/leak-prevention/internal/db"
	"github.com/kborup-redhat/leak-prevention/internal/matcher"
	_ "modernc.org/sqlite"
)

func setupMatcher(t *testing.T) *matcher.Matcher {
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

	return matcher.New(wdb, adb)
}

func TestMatcher_WatchlistMatch(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("We should migrate to Amazon for hosting")
	if !result.Blocked {
		t.Fatal("expected blocked for Amazon")
	}
	if len(result.Matches) != 1 || result.Matches[0].Name != "Amazon" {
		t.Errorf("unexpected matches: %+v", result.Matches)
	}
}

func TestMatcher_AliasMatch(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("Deploy our app to AWS")
	if !result.Blocked {
		t.Fatal("expected blocked for AWS")
	}
	if len(result.Matches) != 1 {
		t.Fatalf("expected 1 match, got %d: %+v", len(result.Matches), result.Matches)
	}
	if result.Matches[0].Name != "AWS" || result.Matches[0].Parent != "Amazon" {
		t.Errorf("unexpected match: %+v", result.Matches[0])
	}
}

func TestMatcher_MultiWordAlias(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("Use Google Cloud for backup storage")
	if !result.Blocked {
		t.Fatal("expected blocked for Google Cloud")
	}
	found := false
	for _, match := range result.Matches {
		if match.Name == "Google Cloud" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected to find Google Cloud in matches: %+v", result.Matches)
	}
}

func TestMatcher_MultipleMatches(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("Migrate from AWS to Google Cloud")
	if !result.Blocked {
		t.Fatal("expected blocked")
	}
	if len(result.Matches) < 2 {
		t.Errorf("expected at least 2 matches, got %d: %+v", len(result.Matches), result.Matches)
	}
}

func TestMatcher_AllowlistBypass(t *testing.T) {
	m := setupMatcher(t)
	if err := m.Allowlist().Add("AWS"); err != nil {
		t.Fatal(err)
	}
	result := m.Check("Deploy our app to AWS")
	if result.Blocked {
		t.Fatal("expected not blocked after allowlisting AWS")
	}
}

func TestMatcher_CleanPrompt(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("Write a function that adds two numbers")
	if result.Blocked {
		t.Fatal("expected not blocked for clean prompt")
	}
}

func TestMatcher_AutoDetectUnknownProperNoun(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("We need to integrate with Palantir")
	if !result.Blocked {
		t.Fatal("expected blocked for unknown proper noun Palantir")
	}
}

func TestMatcher_SkipTechTerms(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("Deploy to OpenShift using Kubernetes and Tekton")
	if result.Blocked {
		t.Fatalf("tech terms should not trigger: %+v", result.Matches)
	}
}

func TestMatcher_SkipRandomTokens(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("Use token RKExfcAFcG853FvSRPThDx for authentication")
	if result.Blocked {
		t.Fatalf("random tokens should not trigger: %+v", result.Matches)
	}
}

func TestMatcher_SkipShortWords(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("Go is a great language")
	if result.Blocked {
		t.Fatalf("short words (<=2 chars) should not trigger: %+v", result.Matches)
	}
}

func TestMatcher_SkipDictionaryWithSuffix(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("Running processes are monitored")
	if result.Blocked {
		t.Fatalf("dictionary words with suffixes should not trigger: %+v", result.Matches)
	}
}

func TestMatcher_CaseInsensitive(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("we use amazon for hosting")
	if !result.Blocked {
		t.Fatal("expected blocked for lowercase amazon")
	}
}

func TestMatcher_AllowlistCommand(t *testing.T) {
	m := setupMatcher(t)
	result := m.Check("Deploy to AWS")
	if result.AllowlistCommands == "" {
		t.Fatal("expected allowlist commands in blocked result")
	}
}
