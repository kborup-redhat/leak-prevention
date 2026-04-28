# Leak Prevention API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the bash-based leak prevention hook with a Go HTTP API running in a local Podman container, backed by SQLite, with subsidiary/alias matching via an alias table.

**Architecture:** Go HTTP server in a Podman container serves a matching API on localhost:8642. A thin Go CLI binary (`leak-prevention-hook`) acts as the Claude Code hook, reading prompts from stdin and POSTing them to the API. The server uses two SQLite databases: a read-only watchlist (baked into the image) and a read-write allowlist (on a Podman volume). Matching runs three phases: watchlist+alias lookup, auto-detection of unknown proper nouns, and decision.

**Tech Stack:** Go 1.22+, SQLite (via `modernc.org/sqlite` — pure Go, no CGO), `net/http` stdlib router, Podman with UBI9 images, `encoding/json` for API payloads.

---

## File Structure

```
claude-leak-prevention-hook/
  go.mod                              # Go module definition
  go.sum                              # Go dependency checksums
  cmd/
    server/
      main.go                         # Server entrypoint — CLI flags, DB init, HTTP listen
    hook/
      main.go                         # Hook CLI — reads stdin, POSTs to API, returns hook JSON
  internal/
    db/
      watchlist.go                    # Read-only watchlist queries (companies + aliases)
      watchlist_test.go
      allowlist.go                    # Read-write allowlist CRUD
      allowlist_test.go
    matcher/
      matcher.go                      # Three-phase matching logic
      matcher_test.go
      techterms.go                    # Embedded tech terms list (var TechTerms = map[string]bool{...})
      dictionary.go                   # Embedded dictionary from /usr/share/dict/words
    api/
      handler.go                      # HTTP handlers for /check, /allowlist, /health
      handler_test.go
      router.go                       # Route setup (http.ServeMux)
  Containerfile                       # Multi-stage build (UBI go-toolset + UBI minimal)
  seed-watchlist.sh                   # Creates watchlist.db from seed-watchlist.sql
  update-watchlist.sh                 # (existing) AI-powered watchlist updater — add pass 2 for aliases
  install.sh                          # (existing) Updated installer
  leak-prevention-allowlist.txt       # Starter allowlist (just "Shell")
  leak-prevention-watchlist.txt       # Existing flat watchlist (3066 entries)
```

**Design notes:**
- `modernc.org/sqlite` is a pure-Go SQLite implementation — no CGO needed, simplifies cross-compilation and container builds.
- The server uses `net/http.ServeMux` (Go 1.22+ with method routing) — no external router dependency.
- Tech terms are compiled into the binary as a Go map literal, not loaded from a file at runtime.
- The dictionary is embedded via `//go:embed` from a file generated at build time (stripped from `/usr/share/dict/words`).

---

### Task 1: Go Module and Dependencies

**Files:**
- Create: `go.mod`
- Create: `go.sum` (auto-generated)

- [ ] **Step 1: Initialize Go module**

```bash
cd /home/kborup/ai-code/claude-leak-prevention-hook
go mod init github.com/kborup-redhat/leak-prevention
```

- [ ] **Step 2: Add SQLite dependency**

```bash
cd /home/kborup/ai-code/claude-leak-prevention-hook
go get modernc.org/sqlite
```

- [ ] **Step 3: Verify module file**

Run: `cat go.mod`
Expected: module name `github.com/kborup-redhat/leak-prevention`, Go 1.22+, `modernc.org/sqlite` in require block.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum
git commit -m "feat: initialize Go module with SQLite dependency"
```

---

### Task 2: Watchlist Database Layer

**Files:**
- Create: `internal/db/watchlist.go`
- Create: `internal/db/watchlist_test.go`

- [ ] **Step 1: Write the failing test for WatchlistDB.FindCompany**

```go
// internal/db/watchlist_test.go
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
	t.Cleanup(func() { sqlDB.Close() })

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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./internal/db/ -v -run TestFindCompany`
Expected: FAIL — package `db` does not exist yet.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/db/watchlist.go
package db

import "database/sql"

type Match struct {
	Name     string `json:"name"`
	Parent   string `json:"parent,omitempty"`
	Category string `json:"category"`
}

type WatchlistDB struct {
	db *sql.DB
}

func NewWatchlistDB(sqlDB *sql.DB) *WatchlistDB {
	return &WatchlistDB{db: sqlDB}
}

func (w *WatchlistDB) FindCompany(term string) (Match, bool) {
	var m Match

	// Check companies table first
	row := w.db.QueryRow(
		"SELECT name, category FROM companies WHERE name = ?1 COLLATE NOCASE",
		term,
	)
	if err := row.Scan(&m.Name, &m.Category); err == nil {
		return m, true
	}

	// Check aliases table
	row = w.db.QueryRow(`
		SELECT a.alias, c.name, c.category
		FROM aliases a
		JOIN companies c ON a.company_id = c.id
		WHERE a.alias = ?1 COLLATE NOCASE`,
		term,
	)
	if err := row.Scan(&m.Name, &m.Parent, &m.Category); err == nil {
		return m, true
	}

	return Match{}, false
}

func (w *WatchlistDB) CompanyCount() int {
	var count int
	w.db.QueryRow("SELECT COUNT(*) FROM companies").Scan(&count)
	return count
}

func (w *WatchlistDB) AliasCount() int {
	var count int
	w.db.QueryRow("SELECT COUNT(*) FROM aliases").Scan(&count)
	return count
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./internal/db/ -v -run "TestFindCompany|TestCompanyCount|TestAliasCount"`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/watchlist.go internal/db/watchlist_test.go
git commit -m "feat: add watchlist database layer with company/alias lookup"
```

---

### Task 3: Allowlist Database Layer

**Files:**
- Create: `internal/db/allowlist.go`
- Create: `internal/db/allowlist_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/db/allowlist_test.go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./internal/db/ -v -run TestAllowlist`
Expected: FAIL — `NewAllowlistDB` not defined.

- [ ] **Step 3: Write minimal implementation**

```go
// internal/db/allowlist.go
package db

import "database/sql"

type AllowlistDB struct {
	db *sql.DB
}

func NewAllowlistDB(sqlDB *sql.DB) (*AllowlistDB, error) {
	_, err := sqlDB.Exec(`
		CREATE TABLE IF NOT EXISTS allowlist (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			term TEXT NOT NULL UNIQUE COLLATE NOCASE,
			added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return nil, err
	}
	return &AllowlistDB{db: sqlDB}, nil
}

func (a *AllowlistDB) IsAllowed(term string) bool {
	var count int
	a.db.QueryRow("SELECT COUNT(*) FROM allowlist WHERE term = ?1 COLLATE NOCASE", term).Scan(&count)
	return count > 0
}

func (a *AllowlistDB) Add(term string) error {
	_, err := a.db.Exec(
		"INSERT INTO allowlist (term) VALUES (?1) ON CONFLICT (term) DO NOTHING",
		term,
	)
	return err
}

func (a *AllowlistDB) Remove(term string) error {
	_, err := a.db.Exec("DELETE FROM allowlist WHERE term = ?1 COLLATE NOCASE", term)
	return err
}

func (a *AllowlistDB) List() ([]string, error) {
	rows, err := a.db.Query("SELECT term FROM allowlist ORDER BY term")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var terms []string
	for rows.Next() {
		var term string
		if err := rows.Scan(&term); err != nil {
			return nil, err
		}
		terms = append(terms, term)
	}
	return terms, rows.Err()
}

func (a *AllowlistDB) Count() int {
	var count int
	a.db.QueryRow("SELECT COUNT(*) FROM allowlist").Scan(&count)
	return count
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./internal/db/ -v -run TestAllowlist`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/db/allowlist.go internal/db/allowlist_test.go
git commit -m "feat: add allowlist database layer with CRUD operations"
```

---

### Task 4: Tech Terms List

**Files:**
- Create: `internal/matcher/techterms.go`

- [ ] **Step 1: Generate tech terms Go file from current bash hook**

Extract the `TECH_TERMS` variable from `leak-prevention-hook.sh` and convert to a Go map. The current hook has ~280 terms separated by `|`.

Run this command to generate the file from the existing bash hook's `TECH_TERMS` variable (the script extracts each `|`-separated term and writes it as a Go map entry):

```bash
cd /home/kborup/ai-code/claude-leak-prevention-hook

# Extract terms from bash hook and generate Go source
TERMS=$(grep -oP 'TECH_TERMS="[^"]*"' leak-prevention-hook.sh | sed 's/TECH_TERMS="//;s/"$//')

{
  echo 'package matcher'
  echo ''
  echo 'var TechTerms = map[string]bool{'
  echo "$TERMS" | tr '|' '\n' | sort -u | while read -r term; do
    [[ -n "$term" ]] && printf '\t"%s": true,\n' "$term"
  done
  echo '}'
} > internal/matcher/techterms.go
```

- [ ] **Step 2: Verify the file compiles**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go build ./internal/matcher/`
Expected: no errors.

- [ ] **Step 3: Verify term count**

Run: `grep -c 'true,' internal/matcher/techterms.go`
Expected: ~280 (matching the number of terms in the bash hook).

- [ ] **Step 4: Commit**

```bash
git add internal/matcher/techterms.go
git commit -m "feat: add compiled tech terms list for auto-detection filter"
```

---

### Task 5: Embedded Dictionary

**Files:**
- Create: `internal/matcher/dictionary.go`

- [ ] **Step 1: Create the dictionary embed file**

The dictionary is embedded from `/usr/share/dict/words` at build time. We lowercase and deduplicate entries, then embed as a sorted text file.

```go
// internal/matcher/dictionary.go
package matcher

import (
	_ "embed"
	"strings"
)

//go:embed words.txt
var wordsRaw string

var dictionary map[string]bool

func init() {
	dictionary = make(map[string]bool, 120000)
	for _, word := range strings.Split(wordsRaw, "\n") {
		if word != "" {
			dictionary[strings.ToLower(word)] = true
		}
	}
}

func IsEnglishWord(word string) bool {
	return dictionary[strings.ToLower(word)]
}
```

- [ ] **Step 2: Generate the words.txt file for embedding**

```bash
cd /home/kborup/ai-code/claude-leak-prevention-hook/internal/matcher
tr '[:upper:]' '[:lower:]' < /usr/share/dict/words | sort -u > words.txt
wc -l words.txt
```

Expected: ~100,000+ lines.

- [ ] **Step 3: Write a test for dictionary lookup**

```go
// Add to a new file: internal/matcher/dictionary_test.go
package matcher

import "testing"

func TestIsEnglishWord_CommonWords(t *testing.T) {
	words := []string{"hello", "world", "computer", "running", "development"}
	for _, w := range words {
		if !IsEnglishWord(w) {
			t.Errorf("expected %q to be in dictionary", w)
		}
	}
}

func TestIsEnglishWord_NotInDictionary(t *testing.T) {
	words := []string{"xyzzy", "asdfgh", "qwerty123"}
	for _, w := range words {
		if IsEnglishWord(w) {
			t.Errorf("expected %q to NOT be in dictionary", w)
		}
	}
}

func TestIsEnglishWord_CaseInsensitive(t *testing.T) {
	if !IsEnglishWord("Hello") {
		t.Error("dictionary lookup should be case-insensitive")
	}
}
```

- [ ] **Step 4: Run tests**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./internal/matcher/ -v -run TestIsEnglishWord`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/dictionary.go internal/matcher/dictionary_test.go internal/matcher/words.txt
git commit -m "feat: add embedded English dictionary for auto-detection"
```

---

### Task 6: Matcher Logic (Three-Phase Detection)

**Files:**
- Create: `internal/matcher/matcher.go`
- Create: `internal/matcher/matcher_test.go`

- [ ] **Step 1: Write the failing tests**

```go
// internal/matcher/matcher_test.go
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
	t.Cleanup(func() { watchSQL.Close() })

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
	t.Cleanup(func() { allowSQL.Close() })

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

	m.Allowlist().Add("AWS")
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
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./internal/matcher/ -v -run TestMatcher`
Expected: FAIL — `matcher.New` not defined.

- [ ] **Step 3: Write the matcher implementation**

```go
// internal/matcher/matcher.go
package matcher

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/kborup-redhat/leak-prevention/internal/db"
)

type Result struct {
	Blocked           bool       `json:"blocked"`
	Matches           []db.Match `json:"matches"`
	AllowlistCommands string     `json:"allowlist_commands,omitempty"`
}

type Matcher struct {
	watchlist *db.WatchlistDB
	allowlist *db.AllowlistDB
}

func New(watchlist *db.WatchlistDB, allowlist *db.AllowlistDB) *Matcher {
	return &Matcher{watchlist: watchlist, allowlist: allowlist}
}

func (m *Matcher) Allowlist() *db.AllowlistDB {
	return m.allowlist
}

var wordRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9-]*`)
var randomTokenRe = regexp.MustCompile(`[0-9]`)
var hasLower = regexp.MustCompile(`[a-z]`)
var hasUpper = regexp.MustCompile(`[A-Z]`)

func (m *Matcher) Check(prompt string) Result {
	seen := make(map[string]bool)
	var matches []db.Match

	// Phase 1: Watchlist + Alias matching
	// Extract words and check sliding windows (1, 2, 3 word combos)
	words := wordRe.FindAllString(prompt, -1)

	for windowSize := 3; windowSize >= 1; windowSize-- {
		for i := 0; i <= len(words)-windowSize; i++ {
			token := strings.Join(words[i:i+windowSize], " ")

			if seen[strings.ToLower(token)] {
				continue
			}

			if m.allowlist.IsAllowed(token) {
				continue
			}

			match, found := m.watchlist.FindCompany(token)
			if found {
				seen[strings.ToLower(token)] = true
				matches = append(matches, match)
			}
		}
	}

	// Phase 2: Auto-detection
	allWords := wordRe.FindAllString(prompt, -1)
	checked := make(map[string]bool)

	for _, word := range allWords {
		if len(word) <= 2 {
			continue
		}

		if !unicode.IsUpper(rune(word[0])) {
			continue
		}

		lower := strings.ToLower(word)
		if checked[lower] {
			continue
		}
		checked[lower] = true

		if seen[lower] {
			continue
		}

		// Skip random tokens (8+ chars with mixed case and digits)
		if len(word) >= 8 && randomTokenRe.MatchString(word) && hasLower.MatchString(word) && hasUpper.MatchString(word) {
			continue
		}

		// Skip tech terms
		if TechTerms[lower] {
			continue
		}

		// Skip allowlisted
		if m.allowlist.IsAllowed(word) {
			continue
		}

		// Check dictionary (with suffix stripping)
		if isKnownWord(lower) {
			continue
		}

		seen[lower] = true
		matches = append(matches, db.Match{
			Name:     word,
			Category: "AUTO-DETECTED",
		})
	}

	// Phase 3: Decision
	result := Result{
		Blocked: len(matches) > 0,
		Matches: matches,
	}

	if result.Blocked {
		var cmds []string
		for _, match := range matches {
			cmds = append(cmds, fmt.Sprintf(
				`curl -s -X POST -H 'Content-Type: application/json' -d '{"term":"%s"}' http://localhost:8642/allowlist`,
				match.Name,
			))
		}
		result.AllowlistCommands = strings.Join(cmds, "; ")
	}

	if result.Matches == nil {
		result.Matches = []db.Match{}
	}

	return result
}

func isKnownWord(lower string) bool {
	if IsEnglishWord(lower) {
		return true
	}

	suffixes := []string{"s", "ed", "d", "ing", "ly", "er", "est"}
	for _, suffix := range suffixes {
		if strings.HasSuffix(lower, suffix) {
			stem := strings.TrimSuffix(lower, suffix)
			if stem != "" && IsEnglishWord(stem) {
				return true
			}
		}
	}

	return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./internal/matcher/ -v -run TestMatcher`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/matcher/matcher.go internal/matcher/matcher_test.go
git commit -m "feat: add three-phase prompt matching logic"
```

---

### Task 7: API Handlers and Router

**Files:**
- Create: `internal/api/handler.go`
- Create: `internal/api/router.go`
- Create: `internal/api/handler_test.go`

- [ ] **Step 1: Write failing tests for all API endpoints**

```go
// internal/api/handler_test.go
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
	t.Cleanup(func() { watchSQL.Close() })

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
	t.Cleanup(func() { allowSQL.Close() })

	wdb := db.NewWatchlistDB(watchSQL)
	adb, err := db.NewAllowlistDB(allowSQL)
	if err != nil {
		t.Fatal(err)
	}
	m := matcher.New(wdb, adb)

	return api.NewRouter(m, wdb, adb)
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
	json.NewDecoder(rec.Body).Decode(&result)

	if result.Blocked {
		t.Fatalf("expected not blocked, matches: %+v", result.Matches)
	}
}

func TestAllowlistEndpoint_AddAndList(t *testing.T) {
	srv := setupServer(t)

	// Add
	body := `{"term": "Shell"}`
	req := httptest.NewRequest(http.MethodPost, "/allowlist", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// List
	req = httptest.NewRequest(http.MethodGet, "/allowlist", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	var listResp struct {
		Terms []string `json:"terms"`
	}
	json.NewDecoder(rec.Body).Decode(&listResp)

	if len(listResp.Terms) != 1 || listResp.Terms[0] != "Shell" {
		t.Errorf("unexpected terms: %v", listResp.Terms)
	}
}

func TestAllowlistEndpoint_Delete(t *testing.T) {
	srv := setupServer(t)

	// Add first
	body := `{"term": "Shell"}`
	req := httptest.NewRequest(http.MethodPost, "/allowlist", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	// Delete
	req = httptest.NewRequest(http.MethodDelete, "/allowlist/Shell", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}

	// Verify gone
	req = httptest.NewRequest(http.MethodGet, "/allowlist", nil)
	rec = httptest.NewRecorder()
	srv.ServeHTTP(rec, req)

	var listResp struct {
		Terms []string `json:"terms"`
	}
	json.NewDecoder(rec.Body).Decode(&listResp)

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
	json.NewDecoder(rec.Body).Decode(&health)

	if health.Status != "ok" {
		t.Errorf("expected status ok, got %s", health.Status)
	}
	if health.WatchlistCount != 1 {
		t.Errorf("expected watchlist_count 1, got %d", health.WatchlistCount)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./internal/api/ -v`
Expected: FAIL — package `api` does not exist.

- [ ] **Step 3: Write the router**

```go
// internal/api/router.go
package api

import (
	"net/http"

	"github.com/kborup-redhat/leak-prevention/internal/db"
	"github.com/kborup-redhat/leak-prevention/internal/matcher"
)

func NewRouter(m *matcher.Matcher, wdb *db.WatchlistDB, adb *db.AllowlistDB) http.Handler {
	mux := http.NewServeMux()

	h := &Handler{matcher: m, watchlist: wdb, allowlist: adb}

	mux.HandleFunc("POST /check", h.Check)
	mux.HandleFunc("GET /allowlist", h.ListAllowlist)
	mux.HandleFunc("POST /allowlist", h.AddAllowlist)
	mux.HandleFunc("DELETE /allowlist/{term}", h.DeleteAllowlist)
	mux.HandleFunc("GET /health", h.Health)

	return mux
}
```

- [ ] **Step 4: Write the handlers**

```go
// internal/api/handler.go
package api

import (
	"encoding/json"
	"net/http"

	"github.com/kborup-redhat/leak-prevention/internal/db"
	"github.com/kborup-redhat/leak-prevention/internal/matcher"
)

type Handler struct {
	matcher   *matcher.Matcher
	watchlist *db.WatchlistDB
	allowlist *db.AllowlistDB
}

func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Prompt string `json:"prompt"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"invalid JSON"}`, http.StatusBadRequest)
		return
	}

	result := h.matcher.Check(req.Prompt)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (h *Handler) ListAllowlist(w http.ResponseWriter, r *http.Request) {
	terms, err := h.allowlist.List()
	if err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}
	if terms == nil {
		terms = []string{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string][]string{"terms": terms})
}

func (h *Handler) AddAllowlist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Term string `json:"term"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Term == "" {
		http.Error(w, `{"error":"invalid request, need {\"term\":\"...\"}"}`, http.StatusBadRequest)
		return
	}

	if err := h.allowlist.Add(req.Term); err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
}

func (h *Handler) DeleteAllowlist(w http.ResponseWriter, r *http.Request) {
	term := r.PathValue("term")
	if term == "" {
		http.Error(w, `{"error":"term required"}`, http.StatusBadRequest)
		return
	}

	if err := h.allowlist.Remove(term); err != nil {
		http.Error(w, `{"error":"database error"}`, http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"status":          "ok",
		"watchlist_count": h.watchlist.CompanyCount(),
		"alias_count":     h.watchlist.AliasCount(),
		"allowlist_count": h.allowlist.Count(),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./internal/api/ -v`
Expected: all PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/api/handler.go internal/api/router.go internal/api/handler_test.go
git commit -m "feat: add HTTP API handlers and router"
```

---

### Task 8: Server Entrypoint

**Files:**
- Create: `cmd/server/main.go`

- [ ] **Step 1: Write the server entrypoint**

```go
// cmd/server/main.go
package main

import (
	"database/sql"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/kborup-redhat/leak-prevention/internal/api"
	"github.com/kborup-redhat/leak-prevention/internal/db"
	"github.com/kborup-redhat/leak-prevention/internal/matcher"
	_ "modernc.org/sqlite"
)

func main() {
	watchlistPath := flag.String("watchlist", "/data/watchlist.db", "Path to watchlist SQLite database")
	allowlistDir := flag.String("allowlist-dir", "/data/allowlist", "Directory for allowlist SQLite database")
	port := flag.Int("port", 8642, "Port to listen on")
	flag.Parse()

	// Open watchlist (read-only)
	if _, err := os.Stat(*watchlistPath); err != nil {
		log.Fatalf("Watchlist database not found: %s", *watchlistPath)
	}
	watchDB, err := sql.Open("sqlite", *watchlistPath+"?mode=ro")
	if err != nil {
		log.Fatalf("Failed to open watchlist: %v", err)
	}
	defer watchDB.Close()

	// Open or create allowlist (read-write)
	if err := os.MkdirAll(*allowlistDir, 0755); err != nil {
		log.Fatalf("Failed to create allowlist directory: %v", err)
	}
	allowlistPath := filepath.Join(*allowlistDir, "allowlist.db")
	allowDB, err := sql.Open("sqlite", allowlistPath)
	if err != nil {
		log.Fatalf("Failed to open allowlist: %v", err)
	}
	defer allowDB.Close()

	wdb := db.NewWatchlistDB(watchDB)
	adb, err := db.NewAllowlistDB(allowDB)
	if err != nil {
		log.Fatalf("Failed to initialize allowlist: %v", err)
	}

	m := matcher.New(wdb, adb)
	router := api.NewRouter(m, wdb, adb)

	addr := fmt.Sprintf(":%d", *port)
	log.Printf("Leak prevention server starting on %s", addr)
	log.Printf("Watchlist: %d companies, %d aliases", wdb.CompanyCount(), wdb.AliasCount())
	log.Printf("Allowlist: %d terms", adb.Count())

	if err := http.ListenAndServe(addr, router); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
```

- [ ] **Step 2: Verify it compiles**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go build ./cmd/server/`
Expected: no errors, produces `server` binary in current directory.

- [ ] **Step 3: Clean up build artifact**

```bash
rm -f /home/kborup/ai-code/claude-leak-prevention-hook/server
```

- [ ] **Step 4: Commit**

```bash
git add cmd/server/main.go
git commit -m "feat: add server entrypoint with CLI flags"
```

---

### Task 9: Hook CLI Binary

**Files:**
- Create: `cmd/hook/main.go`
- Create: `cmd/hook/main_test.go`

- [ ] **Step 1: Write the failing test**

```go
// cmd/hook/main_test.go
package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFormatBlockResponse(t *testing.T) {
	matches := []matchEntry{
		{Name: "AWS", Parent: "Amazon", Category: "FORTUNE 500 (US)"},
		{Name: "Google Cloud", Parent: "Alphabet", Category: "FORTUNE 500 (US)"},
	}

	resp := formatBlockResponse(matches)

	var parsed struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Decision != "block" {
		t.Errorf("expected decision block, got %s", parsed.Decision)
	}
	if parsed.Reason == "" {
		t.Error("expected non-empty reason")
	}
}

func TestFormatServerDownResponse(t *testing.T) {
	resp := formatServerDownResponse()

	var parsed struct {
		Decision string `json:"decision"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(resp), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.Decision != "block" {
		t.Errorf("expected decision block, got %s", parsed.Decision)
	}
}

func TestCallServer_Blocked(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"blocked":true,"matches":[{"name":"AWS","parent":"Amazon","category":"FORTUNE 500 (US)"}]}`))
	}))
	defer srv.Close()

	result, err := callServer(srv.URL+"/check", "Deploy to AWS")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Blocked {
		t.Fatal("expected blocked")
	}
}

func TestCallServer_Allowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"blocked":false,"matches":[]}`))
	}))
	defer srv.Close()

	result, err := callServer(srv.URL+"/check", "hello world")
	if err != nil {
		t.Fatal(err)
	}
	if result.Blocked {
		t.Fatal("expected not blocked")
	}
}

func TestCallServer_Unreachable(t *testing.T) {
	_, err := callServer("http://127.0.0.1:1/check", "hello")
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./cmd/hook/ -v`
Expected: FAIL — functions not defined.

- [ ] **Step 3: Write the hook CLI implementation**

```go
// cmd/hook/main.go
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const serverURL = "http://localhost:8642"

type matchEntry struct {
	Name     string `json:"name"`
	Parent   string `json:"parent,omitempty"`
	Category string `json:"category"`
}

type checkResponse struct {
	Blocked           bool         `json:"blocked"`
	Matches           []matchEntry `json:"matches"`
	AllowlistCommands string       `json:"allowlist_commands,omitempty"`
}

func main() {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprint(os.Stderr, formatServerDownResponse())
		os.Exit(1)
	}

	var hookInput struct {
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(input, &hookInput); err != nil || hookInput.Prompt == "" {
		os.Exit(0)
	}

	result, err := callServer(serverURL+"/check", hookInput.Prompt)
	if err != nil {
		fmt.Print(formatServerDownResponse())
		os.Exit(0)
	}

	if result.Blocked {
		fmt.Print(formatBlockResponse(result.Matches))
	}
}

func callServer(url, prompt string) (*checkResponse, error) {
	body, err := json.Marshal(map[string]string{"prompt": prompt})
	if err != nil {
		return nil, err
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post(url, "application/json", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result checkResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func formatBlockResponse(matches []matchEntry) string {
	var names []string
	for _, m := range matches {
		names = append(names, m.Name)
	}
	namesList := strings.Join(names, ", ")

	var cmds []string
	for _, m := range matches {
		cmds = append(cmds, fmt.Sprintf(
			`  ! curl -s -X POST -H 'Content-Type: application/json' -d '{"term":"%s"}' %s/allowlist`,
			m.Name, serverURL,
		))
	}

	reason := fmt.Sprintf(
		"Organization name(s) detected: %s\\n\\nTo allowlist, run:\\n%s\\n\\nThen re-send your message.",
		namesList, strings.Join(cmds, "\\n"),
	)

	resp := map[string]string{
		"decision": "block",
		"reason":   reason,
	}
	out, _ := json.Marshal(resp)
	return string(out)
}

func formatServerDownResponse() string {
	resp := map[string]string{
		"decision": "block",
		"reason":   "Leak prevention server is not running.\\n\\nStart it with:\\n  ! podman start leak-prevention\\n\\nThen re-send your message.",
	}
	out, _ := json.Marshal(resp)
	return string(out)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./cmd/hook/ -v`
Expected: all PASS.

- [ ] **Step 5: Verify the binary builds**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go build -o /tmp/leak-prevention-hook ./cmd/hook && file /tmp/leak-prevention-hook && rm /tmp/leak-prevention-hook`
Expected: ELF 64-bit executable.

- [ ] **Step 6: Commit**

```bash
git add cmd/hook/main.go cmd/hook/main_test.go
git commit -m "feat: add hook CLI binary (reads stdin, queries API, returns hook JSON)"
```

---

### Task 10: seed-watchlist.sh (SQLite Database Generator)

**Files:**
- Create: `seed-watchlist.sh`

- [ ] **Step 1: Write the seed script**

This script converts the existing flat `leak-prevention-watchlist.txt` (with `# === CATEGORY ===` section headers) into a SQLite database with `companies` and `aliases` tables. Initially, all entries go into the `companies` table — aliases are populated by the update script's pass 2 (Task 11).

```bash
#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WATCHLIST="${1:-${SCRIPT_DIR}/leak-prevention-watchlist.txt}"
DB_OUT="${2:-${SCRIPT_DIR}/watchlist.db}"
SEED_SQL="${SCRIPT_DIR}/seed-watchlist.sql"

if [[ ! -f "$WATCHLIST" ]]; then
  echo "ERROR: Watchlist not found at ${WATCHLIST}"
  exit 1
fi

echo "=== Seed Watchlist Database ==="
echo "Source: ${WATCHLIST}"
echo "Output: ${DB_OUT}"
echo ""

# Generate SQL seed file from watchlist
{
  echo "BEGIN TRANSACTION;"
  echo ""
  echo "CREATE TABLE IF NOT EXISTS companies ("
  echo "    id INTEGER PRIMARY KEY AUTOINCREMENT,"
  echo "    name TEXT NOT NULL,"
  echo "    category TEXT NOT NULL"
  echo ");"
  echo ""
  echo "CREATE TABLE IF NOT EXISTS aliases ("
  echo "    id INTEGER PRIMARY KEY AUTOINCREMENT,"
  echo "    company_id INTEGER NOT NULL REFERENCES companies(id) ON DELETE CASCADE,"
  echo "    alias TEXT NOT NULL"
  echo ");"
  echo ""

  current_category="UNCATEGORIZED"

  while IFS= read -r line; do
    # Skip empty lines
    [[ -z "$line" ]] && continue

    # Check for category header: # === CATEGORY ===
    if [[ "$line" =~ ^#\ ===\ (.+)\ ===$ ]]; then
      current_category="${BASH_REMATCH[1]}"
      continue
    fi

    # Skip other comments
    [[ "$line" == \#* ]] && continue

    # Escape single quotes for SQL
    escaped="${line//\'/\'\'}"
    cat_escaped="${current_category//\'/\'\'}"

    echo "INSERT INTO companies (name, category) VALUES ('${escaped}', '${cat_escaped}');"
  done < "$WATCHLIST"

  echo ""
  echo "CREATE INDEX IF NOT EXISTS idx_companies_name ON companies(name COLLATE NOCASE);"
  echo "CREATE INDEX IF NOT EXISTS idx_companies_category ON companies(category);"
  echo "CREATE INDEX IF NOT EXISTS idx_aliases_alias ON aliases(alias COLLATE NOCASE);"
  echo "CREATE INDEX IF NOT EXISTS idx_aliases_company ON aliases(company_id);"
  echo ""
  echo "COMMIT;"
} > "$SEED_SQL"

ENTRY_COUNT=$(grep -c "INSERT INTO companies" "$SEED_SQL" || true)
echo "Generated ${ENTRY_COUNT} company INSERT statements."
echo "Seed SQL: ${SEED_SQL}"

# Create the database
rm -f "$DB_OUT"
sqlite3 "$DB_OUT" < "$SEED_SQL"

# Verify
COMPANY_COUNT=$(sqlite3 "$DB_OUT" "SELECT COUNT(*) FROM companies;")
echo ""
echo "Database created:"
echo "  Companies: ${COMPANY_COUNT}"
echo "  Aliases:   0 (run update-watchlist.sh pass 2 to populate)"
echo ""

sqlite3 "$DB_OUT" "PRAGMA integrity_check;" | head -1
echo ""
echo "Done: ${DB_OUT}"
```

- [ ] **Step 2: Make it executable and test**

```bash
chmod +x /home/kborup/ai-code/claude-leak-prevention-hook/seed-watchlist.sh
```

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && ./seed-watchlist.sh`
Expected: Creates `watchlist.db` with ~3066 companies, 0 aliases, integrity check OK.

- [ ] **Step 3: Verify database content**

Run: `sqlite3 watchlist.db "SELECT COUNT(*) FROM companies; SELECT COUNT(DISTINCT category) FROM companies; SELECT category, COUNT(*) FROM companies GROUP BY category ORDER BY COUNT(*) DESC LIMIT 5;"`
Expected: ~3066 total companies, ~17 categories, top categories with reasonable counts.

- [ ] **Step 4: Commit**

```bash
git add seed-watchlist.sh
git commit -m "feat: add seed-watchlist.sh to convert flat watchlist to SQLite"
```

---

### Task 11: Update watchlist.sh Pass 2 (Alias Generation)

**Files:**
- Modify: `update-watchlist.sh`

- [ ] **Step 1: Read the current update-watchlist.sh**

Run: `cat /home/kborup/ai-code/claude-leak-prevention-hook/update-watchlist.sh`
Understand the current two-phase flow (Phase 1: parallel AI queries, Phase 2: merge results into flat file).

- [ ] **Step 2: Add Pass 2 for subsidiary/alias generation**

After the existing Phase 2 (merge results), add a new Phase 3 that:
1. Reads the updated watchlist
2. For each category, queries the AI for subsidiaries/brands/abbreviations
3. Outputs alias data to `seed-watchlist.sql`

Add these sections after the existing "=== Update Complete ===" block:

```bash
# --- Phase 3: Generate aliases (subsidiaries, brands, abbreviations) ---
echo ""
echo "Phase 3: Querying subsidiaries and aliases..."
echo ""

ALIAS_TMPDIR=$(mktemp -d)
trap "rm -rf ${TMPDIR_RESULTS} ${ALIAS_TMPDIR}" EXIT

# Get unique company names grouped by category
declare -A ALIAS_PIDS
alias_running=0

for category in "${CATEGORY_ORDER[@]}"; do
  safe_name=$(echo "$category" | tr ' /&' '___')

  # Get companies in this category from the watchlist
  section_start=$(grep -n "# === ${category} ===" "$WATCHLIST" 2>/dev/null | tail -1 | cut -d: -f1 || true)
  if [[ -z "$section_start" ]]; then
    continue
  fi

  # Extract company names for this category (between this header and the next)
  companies_in_cat=$(awk -v start="$((section_start + 1))" '
    NR >= start && /^# ===/ && NR > start { exit }
    NR >= start && !/^#/ && !/^$/ { print }
  ' "$WATCHLIST" | head -50)

  if [[ -z "$companies_in_cat" ]]; then
    continue
  fi

  company_list=$(echo "$companies_in_cat" | paste -sd, -)

  alias_prompt="For each of these companies, list their major subsidiaries, brand names, and common abbreviations. Format each line as: ParentCompany: Sub1, Sub2, Sub3. One parent per line, comma-separated aliases after the colon. Companies: ${company_list}"

  output_file="${ALIAS_TMPDIR}/${safe_name}.txt"

  while [[ $alias_running -ge $MAX_PARALLEL ]]; do
    for pid_cat in "${!ALIAS_PIDS[@]}"; do
      pid="${ALIAS_PIDS[$pid_cat]}"
      if ! kill -0 "$pid" 2>/dev/null; then
        wait "$pid" 2>/dev/null || true
        echo "  Done: ${pid_cat}"
        unset "ALIAS_PIDS[$pid_cat]"
        ((alias_running--)) || true
      fi
    done
    if [[ $alias_running -ge $MAX_PARALLEL ]]; then
      sleep 1
    fi
  done

  echo "  Starting: ${category}"
  query_ai "$alias_prompt" "$output_file" &
  ALIAS_PIDS["$category"]=$!
  ((alias_running++)) || true
done

echo ""
echo "Waiting for alias queries to finish..."
for pid_cat in "${!ALIAS_PIDS[@]}"; do
  pid="${ALIAS_PIDS[$pid_cat]}"
  wait "$pid" 2>/dev/null || true
  echo "  Done: ${pid_cat}"
done

# Parse alias results and append to seed SQL
echo ""
echo "Phase 4: Parsing alias results..."

ALIAS_SQL="${SCRIPT_DIR}/seed-aliases.sql"
TOTAL_ALIASES=0

{
  echo "-- Aliases generated by update-watchlist.sh pass 2"
  echo "BEGIN TRANSACTION;"

  for category in "${CATEGORY_ORDER[@]}"; do
    safe_name=$(echo "$category" | tr ' /&' '___')
    output_file="${ALIAS_TMPDIR}/${safe_name}.txt"

    if [[ ! -f "$output_file" ]] || [[ ! -s "$output_file" ]]; then
      continue
    fi

    while IFS= read -r line; do
      [[ -z "$line" ]] && continue
      [[ "$line" == \#* ]] && continue
      [[ ! "$line" == *:* ]] && continue

      parent=$(echo "$line" | cut -d: -f1 | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
      aliases_raw=$(echo "$line" | cut -d: -f2-)

      # Clean parent name (strip markdown, bullets, numbers)
      parent=$(echo "$parent" | sed -E 's/^\*\*//;s/\*\*$//;s/^[0-9]+[\.\)][[:space:]]*//;s/^[-\*][[:space:]]+//')
      parent="${parent//\'/\'\'}"

      IFS=',' read -ra alias_array <<< "$aliases_raw"
      for alias in "${alias_array[@]}"; do
        alias=$(echo "$alias" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
        [[ -z "$alias" ]] && continue
        [[ ${#alias} -le 1 ]] && continue
        [[ ${#alias} -ge 80 ]] && continue

        # Skip if alias matches parent name
        if [[ "${alias,,}" == "${parent,,}" ]]; then
          continue
        fi

        alias="${alias//\'/\'\'}"

        echo "INSERT OR IGNORE INTO aliases (company_id, alias) SELECT id, '${alias}' FROM companies WHERE name = '${parent}' COLLATE NOCASE LIMIT 1;"
        ((TOTAL_ALIASES++)) || true
      done
    done < "$output_file"
  done

  echo "COMMIT;"
} > "$ALIAS_SQL"

echo "  Generated ${TOTAL_ALIASES} alias INSERT statements."
echo "  Alias SQL: ${ALIAS_SQL}"

# Regenerate watchlist.db with aliases
echo ""
echo "Regenerating watchlist.db with aliases..."
"${SCRIPT_DIR}/seed-watchlist.sh"
sqlite3 "${SCRIPT_DIR}/watchlist.db" < "$ALIAS_SQL"

FINAL_ALIAS_COUNT=$(sqlite3 "${SCRIPT_DIR}/watchlist.db" "SELECT COUNT(*) FROM aliases;")
echo "  Aliases in database: ${FINAL_ALIAS_COUNT}"
```

- [ ] **Step 3: Test in dry-run mode**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && ./update-watchlist.sh --dry-run --verbose 2>&1 | tail -20`
Expected: Shows Phase 3/4 output without modifying files (verify script structure is correct).

- [ ] **Step 4: Commit**

```bash
git add update-watchlist.sh
git commit -m "feat: add pass 2 to update-watchlist.sh for subsidiary/alias generation"
```

---

### Task 12: Containerfile

**Depends on:** Task 10 (seed-watchlist.sh must have generated `watchlist.db` before `podman build` can copy it into the image).

**Files:**
- Create: `Containerfile`
- Create: `.containerignore`

- [ ] **Step 1: Write the Containerfile**

```dockerfile
# Build stage
FROM registry.access.redhat.com/ubi9/go-toolset:latest AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Generate embedded dictionary
RUN tr '[:upper:]' '[:lower:]' < /usr/share/dict/words | sort -u > internal/matcher/words.txt || \
    echo "warning: /usr/share/dict/words not found, using empty dictionary" && touch internal/matcher/words.txt
RUN go build -o leak-prevention-server ./cmd/server

# Runtime stage
FROM registry.access.redhat.com/ubi9-minimal:latest
COPY --from=builder /app/leak-prevention-server /usr/local/bin/
COPY watchlist.db /data/watchlist.db
EXPOSE 8642
VOLUME /data/allowlist
USER 1001
CMD ["leak-prevention-server", "--watchlist", "/data/watchlist.db", "--allowlist-dir", "/data/allowlist", "--port", "8642"]
```

- [ ] **Step 2: Write .containerignore**

```
.git
*.md
docs/
leak-prevention-watchlist.txt
leak-prevention-allowlist.txt
seed-watchlist.sql
seed-aliases.sql
*.sh
!seed-watchlist.sh
```

Wait — the `.containerignore` should NOT exclude `go.mod`, `go.sum`, `cmd/`, `internal/`, or source files. Let me be precise:

```
.git
*.md
docs/
leak-prevention-watchlist.txt
leak-prevention-allowlist.txt
seed-watchlist.sql
seed-aliases.sql
```

- [ ] **Step 3: Verify Containerfile builds**

First ensure `watchlist.db` exists (from Task 10), then build:

```bash
cd /home/kborup/ai-code/claude-leak-prevention-hook
# Generate words.txt locally for the embed
tr '[:upper:]' '[:lower:]' < /usr/share/dict/words | sort -u > internal/matcher/words.txt
# Ensure watchlist.db exists
[[ -f watchlist.db ]] || ./seed-watchlist.sh
# Build the container
podman build -t quay.io/kborup/leak-prevention:latest .
```

Expected: successful build, image created.

- [ ] **Step 4: Test the container starts and responds to health check**

```bash
# Run temporarily
podman run -d --name leak-prevention-test -p 127.0.0.1:8642:8642 quay.io/kborup/leak-prevention:latest
sleep 2
curl -s http://localhost:8642/health
podman stop leak-prevention-test && podman rm leak-prevention-test
```

Expected: `{"status":"ok","watchlist_count":3066,...}` (or similar count).

- [ ] **Step 5: Commit**

```bash
git add Containerfile .containerignore
git commit -m "feat: add multi-stage Containerfile with UBI9 images"
```

---

### Task 13: Install Script Update

**Files:**
- Modify: `install.sh`

The current `install.sh` is already written for the new architecture (from earlier in the session). Verify it works end-to-end with the now-existing Go code.

- [ ] **Step 1: Read the current install.sh**

Run: `cat /home/kborup/ai-code/claude-leak-prevention-hook/install.sh`
Verify it handles: Go build, container build/pull, volume creation, container start, settings.json update, health check.

- [ ] **Step 2: Add watchlist.db generation step**

The installer should ensure `watchlist.db` exists before building the container. Add between Step 1 (build hook binary) and Step 2 (build container):

Add this block after the hook binary build step:

```bash
# --- Step 1b: Ensure watchlist database exists ---
echo ""
echo "Step 1b: Ensuring watchlist database..."
if [[ ! -f "${SCRIPT_DIR}/watchlist.db" ]]; then
  if [[ -f "${SCRIPT_DIR}/seed-watchlist.sh" ]] && [[ -f "${SCRIPT_DIR}/leak-prevention-watchlist.txt" ]]; then
    echo "  Generating watchlist.db from watchlist text file..."
    bash "${SCRIPT_DIR}/seed-watchlist.sh"
  else
    echo "  ERROR: watchlist.db not found and cannot be generated."
    echo "  Run seed-watchlist.sh first, or ensure leak-prevention-watchlist.txt exists."
    exit 1
  fi
else
  echo "  watchlist.db already exists."
fi
```

Also add `sqlite3` to the prerequisites check:

```bash
command -v sqlite3 &>/dev/null || MISSING+=("sqlite3")
```

- [ ] **Step 3: Add words.txt generation for local hook binary build**

Before the Go build step, ensure `internal/matcher/words.txt` exists:

```bash
# Ensure dictionary file exists for Go embed
WORDS_FILE="${SCRIPT_DIR}/internal/matcher/words.txt"
if [[ ! -f "$WORDS_FILE" ]]; then
  if [[ -f /usr/share/dict/words ]]; then
    mkdir -p "$(dirname "$WORDS_FILE")"
    tr '[:upper:]' '[:lower:]' < /usr/share/dict/words | sort -u > "$WORDS_FILE"
  else
    touch "$WORDS_FILE"
  fi
fi
```

- [ ] **Step 4: Test the full installer**

```bash
cd /home/kborup/ai-code/claude-leak-prevention-hook
# Stop and remove any existing test container
podman stop leak-prevention 2>/dev/null || true
podman rm leak-prevention 2>/dev/null || true
# Run installer
./install.sh
```

Expected: All 6 steps pass, container is running, health check succeeds.

- [ ] **Step 5: Verify the hook binary works**

```bash
echo '{"prompt":"Deploy to AWS"}' | ~/.claude/hooks/leak-prevention-hook
```

Expected: JSON response with `"decision": "block"` mentioning AWS.

```bash
echo '{"prompt":"Write a hello world function"}' | ~/.claude/hooks/leak-prevention-hook
```

Expected: no output, exit 0.

- [ ] **Step 6: Commit**

```bash
git add install.sh
git commit -m "feat: update installer with watchlist.db generation and dictionary setup"
```

---

### Task 14: Integration Test

**Files:**
- Create: `integration_test.go`

- [ ] **Step 1: Write the integration test**

This test starts the full server in-process and exercises all endpoints.

```go
// integration_test.go
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
```

- [ ] **Step 2: Run integration tests**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test -v -run TestIntegration`
Expected: all PASS.

- [ ] **Step 3: Commit**

```bash
git add integration_test.go
git commit -m "test: add integration tests for full API flow"
```

---

### Task 15: GitHub Actions CI Workflow

**Files:**
- Create: `.github/workflows/ci.yml`

This workflow runs on every push and pull request. It must pass before merging. Steps match the ai-toolbox-ci pattern: test, lint, security scan, vulnerability check, build.

- [ ] **Step 1: Write the CI workflow**

```yaml
# .github/workflows/ci.yml
name: CI

on:
  push:
    branches: [main]
  pull_request:
    branches: [main]

permissions:
  contents: read

jobs:
  test:
    name: Test
    runs-on: ubuntu-latest
    container:
      image: quay.io/kborup/ai-toolbox-ci:1.25
    steps:
      - uses: actions/checkout@v4

      - name: Generate dictionary for embed
        run: |
          dnf install -y words 2>/dev/null || true
          if [ -f /usr/share/dict/words ]; then
            tr '[:upper:]' '[:lower:]' < /usr/share/dict/words | sort -u > internal/matcher/words.txt
          else
            touch internal/matcher/words.txt
          fi

      - name: Run tests
        run: go test ./... -v -count=1 -race

  lint:
    name: Lint
    runs-on: ubuntu-latest
    container:
      image: quay.io/kborup/ai-toolbox-ci:1.25
    steps:
      - uses: actions/checkout@v4

      - name: Generate dictionary for embed
        run: |
          dnf install -y words 2>/dev/null || true
          if [ -f /usr/share/dict/words ]; then
            tr '[:upper:]' '[:lower:]' < /usr/share/dict/words | sort -u > internal/matcher/words.txt
          else
            touch internal/matcher/words.txt
          fi

      - name: golangci-lint
        run: golangci-lint run ./...

  security:
    name: Security Scan
    runs-on: ubuntu-latest
    container:
      image: quay.io/kborup/ai-toolbox-ci:1.25
    steps:
      - uses: actions/checkout@v4

      - name: Generate dictionary for embed
        run: |
          dnf install -y words 2>/dev/null || true
          if [ -f /usr/share/dict/words ]; then
            tr '[:upper:]' '[:lower:]' < /usr/share/dict/words | sort -u > internal/matcher/words.txt
          else
            touch internal/matcher/words.txt
          fi

      - name: Run gosec
        run: gosec ./...

      - name: Run govulncheck
        run: govulncheck ./...

  build:
    name: Build
    runs-on: ubuntu-latest
    container:
      image: quay.io/kborup/ai-toolbox-ci:1.25
    needs: [test, lint, security]
    steps:
      - uses: actions/checkout@v4

      - name: Generate dictionary for embed
        run: |
          dnf install -y words 2>/dev/null || true
          if [ -f /usr/share/dict/words ]; then
            tr '[:upper:]' '[:lower:]' < /usr/share/dict/words | sort -u > internal/matcher/words.txt
          else
            touch internal/matcher/words.txt
          fi

      - name: Build server
        run: go build -o leak-prevention-server ./cmd/server

      - name: Build hook
        run: go build -o leak-prevention-hook ./cmd/hook
```

- [ ] **Step 2: Verify YAML syntax**

Run: `python3 -c "import yaml; yaml.safe_load(open('.github/workflows/ci.yml'))" && echo "Valid YAML"`
Expected: `Valid YAML`

- [ ] **Step 3: Commit**

```bash
git add .github/workflows/ci.yml
git commit -m "ci: add GitHub Actions workflow with test, lint, gosec, govulncheck"
```

---

### Task 16: Run Full Test Suite, Lint, and Security Scan Locally

- [ ] **Step 1: Run all tests**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && go test ./... -v -count=1`
Expected: All tests pass across all packages.

- [ ] **Step 2: Run golangci-lint**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && golangci-lint run ./...`
Expected: No lint errors. If `golangci-lint` is not installed, install with: `go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest`

- [ ] **Step 3: Run gosec**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && gosec ./...`
Expected: No security issues found. If `gosec` is not installed: `go install github.com/securego/gosec/v2/cmd/gosec@latest`

- [ ] **Step 4: Run govulncheck**

Run: `cd /home/kborup/ai-code/claude-leak-prevention-hook && govulncheck ./...`
Expected: No known vulnerabilities. If not installed: `go install golang.org/x/vuln/cmd/govulncheck@latest`

- [ ] **Step 5: Build all binaries**

```bash
cd /home/kborup/ai-code/claude-leak-prevention-hook
go build ./cmd/server/
go build ./cmd/hook/
rm -f server hook
```

Expected: Both compile without errors.

- [ ] **Step 6: Run seed script and verify database**

```bash
cd /home/kborup/ai-code/claude-leak-prevention-hook
./seed-watchlist.sh
sqlite3 watchlist.db "SELECT COUNT(*) FROM companies;"
sqlite3 watchlist.db "PRAGMA integrity_check;"
```

Expected: ~3066 companies, integrity OK.

- [ ] **Step 7: Build container image**

```bash
cd /home/kborup/ai-code/claude-leak-prevention-hook
podman build -t quay.io/kborup/leak-prevention:latest .
```

Expected: Build succeeds.

- [ ] **Step 8: End-to-end container test**

```bash
# Start container
podman run -d --name leak-prevention-e2e -p 127.0.0.1:8642:8642 -v leak-prevention-data:/data/allowlist:Z quay.io/kborup/leak-prevention:latest
sleep 2

# Health check
curl -s http://localhost:8642/health | python3 -m json.tool

# Test blocked prompt
curl -s -X POST -H 'Content-Type: application/json' -d '{"prompt":"Deploy to Amazon"}' http://localhost:8642/check | python3 -m json.tool

# Test allowed prompt
curl -s -X POST -H 'Content-Type: application/json' -d '{"prompt":"Write a hello world function"}' http://localhost:8642/check | python3 -m json.tool

# Test allowlist add
curl -s -X POST -H 'Content-Type: application/json' -d '{"term":"Amazon"}' http://localhost:8642/allowlist
curl -s http://localhost:8642/allowlist | python3 -m json.tool

# Test that allowlisted term now passes
curl -s -X POST -H 'Content-Type: application/json' -d '{"prompt":"Deploy to Amazon"}' http://localhost:8642/check | python3 -m json.tool

# Cleanup
podman stop leak-prevention-e2e && podman rm leak-prevention-e2e
```

Expected: Health returns ok, Amazon blocked, hello world allowed, Amazon passes after allowlisting.

- [ ] **Step 9: Test hook binary against running container**

```bash
# Start container (if not already running from step 5)
podman run -d --name leak-prevention-e2e -p 127.0.0.1:8642:8642 -v leak-prevention-data:/data/allowlist:Z quay.io/kborup/leak-prevention:latest
sleep 2

# Build and test hook binary
cd /home/kborup/ai-code/claude-leak-prevention-hook
go build -o /tmp/leak-prevention-hook ./cmd/hook

echo '{"prompt":"Deploy to AWS"}' | /tmp/leak-prevention-hook
echo '{"prompt":"Write a function"}' | /tmp/leak-prevention-hook
echo "Exit code: $?"

# Cleanup
rm /tmp/leak-prevention-hook
podman stop leak-prevention-e2e && podman rm leak-prevention-e2e
```

Expected: AWS prompt returns block JSON, clean prompt exits 0 with no output.

- [ ] **Step 10: Commit any fixes, then final commit**

```bash
git add -A
git status
# Only commit if there are changes
git commit -m "chore: final verification pass" || true
```

---

### Task 17: Create GitHub Repository and Verify CI

- [ ] **Step 1: Create the private repository**

```bash
cd /home/kborup/ai-code/claude-leak-prevention-hook
git init
git add -A
git commit -m "feat: initial implementation of leak prevention API"
gh repo create kborup-redhat/leak-prevention --private --source=. --push
```

Expected: Repository created and code pushed.

- [ ] **Step 2: Verify CI runs and passes**

```bash
gh run list --repo kborup-redhat/leak-prevention --limit 1
gh run watch --repo kborup-redhat/leak-prevention
```

Expected: All CI jobs (test, lint, security, build) pass. If any fail, fix locally, verify the fix passes locally, push, and re-check CI. Do not proceed until CI is green.

---

### Task 18: Push Container Image to Registry

- [ ] **Step 1: Log in to Quay registry**

```bash
bash /home/kborup/toolbox.sh
```

Expected: `Login Succeeded!`

- [ ] **Step 2: Push the image**

```bash
podman push quay.io/kborup/leak-prevention:latest
```

Expected: Image pushed successfully.

- [ ] **Step 3: Verify**

```bash
podman pull quay.io/kborup/leak-prevention:latest
```

Expected: Image pulled successfully from registry.

---

### Task 19: Add .gitignore

**Files:**
- Create: `.gitignore`

- [ ] **Step 1: Write .gitignore**

```
# Build artifacts
/server
/hook
/leak-prevention-hook
*.db
seed-watchlist.sql
seed-aliases.sql

# Go
/vendor/

# IDE
.idea/
.vscode/

# OS
.DS_Store
```

- [ ] **Step 2: Commit**

```bash
git add .gitignore
git commit -m "chore: add .gitignore for build artifacts and databases"
```
