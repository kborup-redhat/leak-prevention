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
	watchlist       *db.WatchlistDB
	allowlist       *db.AllowlistDB
	customWatchlist *db.CustomWatchlistDB
}

func New(watchlist *db.WatchlistDB, allowlist *db.AllowlistDB) *Matcher {
	return &Matcher{watchlist: watchlist, allowlist: allowlist}
}

func (m *Matcher) SetCustomWatchlist(cw *db.CustomWatchlistDB) {
	m.customWatchlist = cw
}

func (m *Matcher) Allowlist() *db.AllowlistDB {
	return m.allowlist
}

var wordRe = regexp.MustCompile(`[A-Za-z][A-Za-z0-9-]*`)
var randomTokenRe = regexp.MustCompile(`[0-9]`)
var hasLower = regexp.MustCompile(`[a-z]`)
var hasUpper = regexp.MustCompile(`[A-Z]`)
var internalUpperRe = regexp.MustCompile(`[a-z][A-Z]|[A-Z]{2,}[a-z]`)

var programmingPrefixes = []string{
	"Get", "Set", "Is", "Has", "Can", "Will", "Should",
	"On", "Handle", "From", "To", "As", "With", "Without",
	"New", "Make", "Create", "Delete", "Remove", "Destroy",
	"Add", "Insert", "Update", "Find", "Search", "Lookup",
	"Open", "Close", "Read", "Write", "Load", "Save", "Store",
	"Start", "Stop", "Init", "Reset", "Clear", "Flush",
	"Parse", "Format", "Convert", "Transform", "Encode", "Decode",
	"Enable", "Disable", "Register", "Unregister",
	"Map", "Unmap", "Lock", "Unlock", "Try", "Do", "Run",
	"Build", "Check", "Validate", "Verify", "Test", "Assert",
	"Free", "Alloc", "Copy", "Clone", "Merge", "Split",
	"Bad", "Not", "No", "Un", "Shm", "Sys",
}

func (m *Matcher) Check(prompt string) Result {
	seen := make(map[string]bool)
	var matches []db.Match

	// Phase 1: Watchlist + Alias matching
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
				continue
			}

			if m.customWatchlist != nil {
				if cmatch, cfound := m.customWatchlist.Find(token); cfound {
					seen[strings.ToLower(token)] = true
					matches = append(matches, cmatch)
				}
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

		// Skip CamelCase identifiers (e.g. BadAccess, IOException, HashMap)
		if internalUpperRe.MatchString(word) {
			continue
		}

		// Skip pure ALL-CAPS words (acronyms/abbreviations, not company names — those are caught by Phase 1)
		if !hasLower.MatchString(word) {
			continue
		}

		// Skip words containing digits (format specifiers like B8G8R8A8, FP32, R32G32B32A32)
		if randomTokenRe.MatchString(word) {
			continue
		}

		// Skip hyphenated compounds where all parts are dictionary words (e.g. Double-buffered, Semi-transparent)
		if strings.Contains(word, "-") && isHyphenatedDictionaryWord(word) {
			continue
		}

		// Skip words with common programming prefixes or that are exact prefix matches (e.g. GetName, IsValid, Alloc)
		if hasProgrammingPrefix(word) {
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

func hasProgrammingPrefix(word string) bool {
	for _, prefix := range programmingPrefixes {
		if word == prefix {
			return true
		}
		if strings.HasPrefix(word, prefix) && len(word) > len(prefix) && unicode.IsUpper(rune(word[len(prefix)])) {
			return true
		}
	}
	return false
}

func isHyphenatedDictionaryWord(word string) bool {
	parts := strings.Split(strings.ToLower(word), "-")
	for _, part := range parts {
		if part == "" {
			continue
		}
		if !isKnownWord(part) {
			return false
		}
	}
	return true
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
