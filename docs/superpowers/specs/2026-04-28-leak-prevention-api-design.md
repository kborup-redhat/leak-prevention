# Leak Prevention API Design Spec

## Overview

Replace the current bash-based leak prevention hook with a Go HTTP API running in a local Podman container, backed by SQLite. Adds subsidiary/shortname matching via an alias table. The hook becomes a thin Go CLI binary that queries the API.

## Components

### 1. leak-prevention-server (Go HTTP server)

Runs inside a Podman container built on UBI-minimal. Serves the matching API.

**Responsibilities:**
- Load watchlist data from a read-only SQLite database baked into the image
- Manage the allowlist in a separate read-write SQLite database on a Podman volume
- Tokenize prompts and match against companies, aliases, and auto-detected proper nouns
- Return block/allow decisions as JSON

**Port:** 8642 (localhost only)

### 2. leak-prevention-hook (Go CLI binary)

Installed at `~/.claude/hooks/leak-prevention-hook`. Called by the `UserPromptSubmit` hook in `settings.json`.

**Responsibilities:**
- Read prompt JSON from stdin (Claude hook protocol)
- POST the prompt to `http://localhost:8642/check`
- Return the hook response JSON (block with reason, or exit 0 to allow)
- If the server is unreachable, block the prompt and instruct the user to start the container (fail-closed)

### 3. watchlist.db (read-only, baked into image)

SQLite database containing the company/organization watchlist and their aliases.

**Schema:**

```sql
CREATE TABLE companies (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    category TEXT NOT NULL
);

CREATE TABLE aliases (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id INTEGER NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    alias TEXT NOT NULL
);

CREATE INDEX idx_companies_name ON companies(name COLLATE NOCASE);
CREATE INDEX idx_companies_category ON companies(category);
CREATE INDEX idx_aliases_alias ON aliases(alias COLLATE NOCASE);
CREATE INDEX idx_aliases_company ON aliases(company_id);
```

**Example data:**
- Company: `Amazon` (category: `FORTUNE 500 (US)`)
  - Aliases: `AWS`, `Amazon Web Services`, `Kindle`, `Twitch`, `Ring`, `Whole Foods`
- Company: `Alphabet` (category: `FORTUNE 500 (US)`)
  - Aliases: `Google`, `Google Cloud`, `YouTube`, `Waymo`, `DeepMind`, `Fitbit`

### 4. allowlist.db (read-write, Podman volume)

SQLite database for user-managed safe terms. Persists across container restarts via a named Podman volume (`leak-prevention-data`).

**Schema:**

```sql
CREATE TABLE allowlist (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    term TEXT NOT NULL UNIQUE COLLATE NOCASE,
    added_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
```

### 5. update-watchlist.sh (bash script, unchanged runtime)

Generates the watchlist data by querying AI CLIs with web search. Runs on the host before `podman build`.

**Changes from current version:**
- After querying companies per category, runs a second parallel pass asking the AI for subsidiaries, brands, and common abbreviations for each company
- Outputs a seed SQL file (`seed-watchlist.sql`) instead of a flat text file
- The seed file contains INSERT statements for both `companies` and `aliases` tables

**Two-pass flow per category:**
1. Pass 1: "List current [category] companies" (existing behavior, one company per line)
2. Pass 2: "For each of these companies, list their major subsidiaries, brand names, and common abbreviations. Format: ParentCompany: Sub1, Sub2, Sub3" (one parent per line, comma-separated aliases after the colon)

**Pass 2 output parsing:**
- Split each line on `:` to get parent name and alias list
- Split the alias list on `,` and trim whitespace
- Look up the parent in the `companies` table by name (case-insensitive)
- Insert each alias into the `aliases` table linked to that company's ID
- Skip aliases that match the parent name itself

### 6. seed-watchlist.sh (new helper script)

Converts the update script output into a SQLite database file (`watchlist.db`). Called by `update-watchlist.sh` at the end, or standalone.

**Steps:**
1. Create `watchlist.db` with the schema above
2. Execute `seed-watchlist.sql` to populate
3. Verify integrity with `PRAGMA integrity_check`

## API Endpoints

### POST /check

Scan a prompt for leaked names.

**Request:**
```json
{"prompt": "We should migrate to AWS and use Google Cloud for backup"}
```

**Response (blocked):**
```json
{
  "blocked": true,
  "matches": [
    {"name": "AWS", "parent": "Amazon", "category": "FORTUNE 500 (US)"},
    {"name": "Google Cloud", "parent": "Alphabet", "category": "FORTUNE 500 (US)"}
  ],
  "allowlist_commands": "echo 'AWS' | curl -X POST -d @- http://localhost:8642/allowlist; echo 'Google Cloud' | curl -X POST -d @- http://localhost:8642/allowlist"
}
```

**Response (allowed):**
```json
{"blocked": false, "matches": []}
```

### GET /allowlist

List all allowlisted terms.

**Response:**
```json
{"terms": ["Shell", "Meta", "WHO"]}
```

### POST /allowlist

Add a term to the allowlist.

**Request:**
```json
{"term": "Shell"}
```

**Response:** `201 Created`

### DELETE /allowlist/{term}

Remove a term from the allowlist.

**Response:** `204 No Content`

### GET /health

Health check.

**Response:**
```json
{"status": "ok", "watchlist_count": 3066, "alias_count": 8500, "allowlist_count": 12}
```

## Matching Logic (POST /check)

The server processes prompts in three phases:

### Phase 1: Watchlist + Alias matching
1. Tokenize the prompt into individual words and sliding multi-word windows (2-word, 3-word combinations)
2. For each token/window, query `companies.name` and `aliases.alias` with case-insensitive matching
3. Skip any match where the term appears in the allowlist
4. Collect all matches with their parent company and category

### Phase 2: Auto-detection
1. Extract all words starting with an uppercase letter
2. Skip words <= 2 characters
3. Skip words matching a built-in tech terms list (same list as current hook, compiled into the binary)
4. Skip words matching the allowlist
5. Skip words that look like random tokens (8+ chars with mixed uppercase, lowercase, and digits)
6. Check remaining words against an embedded English dictionary (compiled from /usr/share/dict/words)
7. Check common suffixes (-s, -ed, -d, -ing, -ly, -er, -est) against dictionary
8. Flag unmatched words as potential organization names

### Phase 3: Decision
- If any matches from Phase 1 or Phase 2: return `blocked: true` with the match list
- Otherwise: return `blocked: false`

## Container Setup

### Containerfile

```dockerfile
# Build stage
FROM registry.access.redhat.com/ubi9/go-toolset:latest AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o leak-prevention-server ./cmd/server

# Runtime stage
FROM registry.access.redhat.com/ubi9-minimal:latest
COPY --from=builder /app/leak-prevention-server /usr/local/bin/
COPY watchlist.db /data/watchlist.db
EXPOSE 8642
VOLUME /data/allowlist
CMD ["leak-prevention-server", "--watchlist", "/data/watchlist.db", "--allowlist-dir", "/data/allowlist", "--port", "8642"]
```

### Run command

```bash
podman run -d \
  --name leak-prevention \
  --restart unless-stopped \
  -p 127.0.0.1:8642:8642 \
  -v leak-prevention-data:/data/allowlist:Z \
  quay.io/kborup/leak-prevention:latest
```

### Hook binary build

```bash
# Built separately on the host
go build -o ~/.claude/hooks/leak-prevention-hook ./cmd/hook
```

## Hook Integration

### settings.json entry

```json
{
  "hooks": {
    "UserPromptSubmit": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "~/.claude/hooks/leak-prevention-hook",
            "timeout": 10,
            "statusMessage": "Scanning for data leaks..."
          }
        ]
      }
    ]
  }
}
```

### Hook binary behavior

1. Read JSON from stdin: `{"prompt": "..."}`
2. Extract the prompt field
3. POST to `http://localhost:8642/check` with `{"prompt": "..."}`
4. If response `blocked: true`:
   ```json
   {
     "decision": "block",
     "reason": "Organization name(s) detected: AWS, Google Cloud\n\nTo allowlist, run:\n  ! curl -s -X POST -H 'Content-Type: application/json' -d '{\"term\":\"AWS\"}' http://localhost:8642/allowlist\n\nThen re-send your message."
   }
   ```
5. If response `blocked: false`: exit 0 (no output)
6. If server unreachable: return a block response telling the user to start the container:
   ```json
   {
     "decision": "block",
     "reason": "Leak prevention server is not running.\n\nStart it with:\n  ! podman start leak-prevention\n\nThen re-send your message."
   }
   ```

## install.sh Changes

The installer needs to:
1. Build the Go hook binary and install to `~/.claude/hooks/`
2. Build from source (`podman build`) if Containerfile exists, otherwise pull `quay.io/kborup/leak-prevention:latest`
3. Create the Podman volume (`leak-prevention-data`) if it doesn't exist
4. Start the container on `127.0.0.1:8642` with the volume mounted
5. Remove any old shell-based hook entries from `settings.json` and add the new binary hook
6. Verify the API is reachable with a health check (retries up to 5 times)

## Project Directory Structure

```
claude-leak-prevention-hook/
  cmd/
    server/
      main.go           # Server entrypoint
    hook/
      main.go           # Hook CLI entrypoint
  internal/
    matcher/
      matcher.go        # Prompt matching logic (phases 1-3)
      techterms.go      # Embedded tech terms list
      dictionary.go     # Embedded dictionary for auto-detect
    db/
      watchlist.go      # Watchlist SQLite queries
      allowlist.go      # Allowlist SQLite CRUD
    api/
      handler.go        # HTTP handlers
      router.go         # Route setup
  Containerfile
  go.mod
  go.sum
  seed-watchlist.sh     # Converts AI output to watchlist.db
  update-watchlist.sh   # AI-powered watchlist updater
  install.sh            # Full installer
  README.md
  leak-prevention-allowlist.txt   # Starter allowlist (for fresh installs)
  docs/
    superpowers/
      specs/
        2026-04-28-leak-prevention-api-design.md
```

## Testing

- Unit tests for the matcher (known companies, aliases, auto-detect, allowlist bypass, random token skip)
- Integration test: start server, POST prompts, verify block/allow responses
- Hook binary test: pipe mock JSON, verify output format
- Container build test: `podman build` succeeds, health check passes
