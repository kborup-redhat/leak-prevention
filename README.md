# Claude Leak Prevention Hook

A Claude Code `UserPromptSubmit` hook that prevents accidental leakage of customer names, company names, and other sensitive organizational data to AI models.

Runs as a Go API server in a local Podman container, backed by SQLite. A thin Go CLI binary acts as the hook, querying the API on every prompt.

## How It Works

Every prompt you send is scanned **before** it reaches the model. If a company or organization name is detected, the prompt is **blocked** and you're shown the detected names with instructions to allowlist them if they're safe to send.

### Three-Phase Detection

1. **Watchlist matching** — Checks your prompt against a curated list of 3000+ company and organization names (Fortune 500 US/Europe, FTSE 100, DAX 40, CAC 40, Nordic, Nikkei 225, ASX 200, S&P/TSX 60, Hang Seng, government entities, international organizations). Case-insensitive matching.

2. **Subsidiary and alias matching** — Checks against known subsidiaries, brand names, and abbreviations linked to parent companies (e.g., `AWS` -> `Amazon`, `YouTube` -> `Alphabet`, `Instagram` -> `Meta`).

3. **Auto-detection** — Flags unknown capitalized words that aren't in the system dictionary or a built-in list of 500+ tech terms. Skips random tokens (mixed case + digits). Catches names not on the watchlist by detecting proper nouns that look like organization names.

### Fail-Closed

If the leak prevention server is not running, the hook **blocks all prompts** and instructs you to start the container. No prompts pass through without scanning.

## Architecture

```
┌─────────────────────┐    POST /check     ┌────────────────────────────┐
│ leak-prevention-hook │ ────────────────── │ leak-prevention-server     │
│ (Go CLI binary)      │  localhost:8642    │ (Podman container)         │
│                      │                    │                            │
│ ~/.claude/hooks/     │  ◄── JSON ──       │ watchlist.db (read-only)   │
│                      │                    │ allowlist.db (volume)      │
└─────────────────────┘                    └────────────────────────────┘
```

## Files

| File | Description |
|------|-------------|
| `cmd/server/main.go` | Go API server entrypoint |
| `cmd/hook/main.go` | Go hook CLI binary entrypoint |
| `internal/` | Matching logic, database access, HTTP handlers |
| `Containerfile` | Multi-stage build (UBI Go toolset + UBI minimal) |
| `update-watchlist.sh` | AI-powered watchlist updater with web search |
| `seed-watchlist.sh` | Converts update output to SQLite database |
| `install.sh` | Full installer (builds, deploys, configures) |
| `leak-prevention-allowlist.txt` | Starter allowlist for fresh installs |

## Requirements

- **Podman** (for running the server container)
- **Go 1.22+** (for building the hook binary)
- **jq** (for configuring settings.json)
- An AI CLI for watchlist updates: `claude`, `gemini`, `copilot`, or `chatgpt`

## Installation

```bash
./install.sh
```

The installer will:
1. Build the hook CLI binary and install to `~/.claude/hooks/`
2. Pull or build the container image (`quay.io/kborup/leak-prevention:latest`)
3. Create a Podman volume (`leak-prevention-data`) for the allowlist
4. Start the container on `localhost:8642`
5. Configure the hook in `~/.claude/settings.json`
6. Verify the API is reachable with a health check

It is idempotent — running it again won't create duplicate entries or containers.

## Container Image

```
quay.io/kborup/leak-prevention:latest
```

### Manual container management

```bash
# Start
podman start leak-prevention

# Stop
podman stop leak-prevention

# View logs
podman logs leak-prevention

# Rebuild from source
podman build -t quay.io/kborup/leak-prevention:latest .

# Run (first time)
podman run -d \
  --name leak-prevention \
  --restart unless-stopped \
  -p 127.0.0.1:8642:8642 \
  -v leak-prevention-data:/data/allowlist:Z \
  quay.io/kborup/leak-prevention:latest
```

## Usage

Once installed, the hook runs automatically on every prompt. No action needed.

### When a name is detected

```
Organization name(s) detected: AWS, Google Cloud

To allowlist, run:
  ! curl -s -X POST -H 'Content-Type: application/json' \
    -d '{"term":"AWS"}' http://localhost:8642/allowlist

Then re-send your message.
```

### When the server is not running

```
Leak prevention server is not running.

Start it with:
  ! podman start leak-prevention

Then re-send your message.
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/check` | Scan a prompt for leaked names |
| `GET` | `/allowlist` | List all allowlisted terms |
| `POST` | `/allowlist` | Add a term to the allowlist |
| `DELETE` | `/allowlist/{term}` | Remove a term from the allowlist |
| `GET` | `/health` | Health check with counts |

### Examples

```bash
# Check a prompt
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"prompt":"Deploy to AWS"}' \
  http://localhost:8642/check

# Add to allowlist
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"term":"Shell"}' \
  http://localhost:8642/allowlist

# List allowlist
curl -s http://localhost:8642/allowlist

# Remove from allowlist
curl -s -X DELETE http://localhost:8642/allowlist/Shell

# Health check
curl -s http://localhost:8642/health
```

## Updating the Watchlist

The `update-watchlist.sh` script uses an AI CLI with web search to fetch current company/organization listings and their subsidiaries.

```bash
./update-watchlist.sh
```

It auto-detects which AI CLI is available and uses web search to get current data.

### Two-pass update

1. **Pass 1**: Queries each category for current company names
2. **Pass 2**: Queries for subsidiaries, brand names, and abbreviations for each company

Results are written as SQL seed data, then converted to `watchlist.db` via `seed-watchlist.sh`. Rebuild the container image to pick up the new data.

### Supported AI CLIs

| CLI | Web Search Method |
|-----|-------------------|
| `claude` | `--allowedTools WebSearch` |
| `gemini` | Built-in Google Search grounding |
| `copilot` | Built-in `web_fetch` tool |
| `chatgpt` | `--search` flag |

### Options

```
--provider <name>    Force a specific AI CLI instead of auto-detect
--file <path>        Path to watchlist file (default: ~/.claude/leak-prevention-watchlist.txt)
--parallel <n>       Max concurrent AI queries (default: 6)
--dry-run            Show what would be added without modifying the file
--verbose            Show detailed progress including raw AI responses
```

### Categories

17 categories: Fortune 500 (US/Europe), DAX 40, CAC 40, FTSE 100, Nordic, Nikkei 225, ASX 200, S&P/TSX 60, Hang Seng, government agencies (US, European, Asian, Middle East, Latin American), and international organizations.
