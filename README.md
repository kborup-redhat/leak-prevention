# Claude Leak Prevention Hook

A Claude Code `UserPromptSubmit` hook that prevents accidental leakage of customer names, company names, and other sensitive organizational data to AI models.

Runs as a Go API server in a local Podman container, backed by SQLite. A thin Go CLI binary acts as the hook, querying the API on every prompt.

## Quick Start

### 1. Download the hook binary

Pre-built binaries for **Linux**, **macOS**, and **Windows** (amd64 and arm64) are available on the [Releases](https://github.com/kborup-redhat/leak-prevention/releases/latest) page.

Download the binary for your platform and install it:

```bash
# Example for Linux amd64 — see Releases page for all platforms
mkdir -p ~/.claude/hooks
curl -sL https://github.com/kborup-redhat/leak-prevention/releases/latest/download/leak-prevention-hook-linux-amd64 \
  -o ~/.claude/hooks/leak-prevention-hook
chmod +x ~/.claude/hooks/leak-prevention-hook
```

### 2. Pull and run the container

```bash
podman volume create leak-prevention-data

podman run -d \
  --name leak-prevention \
  --restart unless-stopped \
  -p 127.0.0.1:8642:8642 \
  -v leak-prevention-data:/data/allowlist:Z \
  quay.io/kborup/leak-prevention:latest
```

> **Tip:** To pin a specific version, replace `latest` with a version tag (e.g., `1.2.0`). See the [VERSION](VERSION) file for the current release.

### 3. Configure Claude Code

Add the hook to your `~/.claude/settings.json`:

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

### 4. Verify

```bash
curl -s http://localhost:8642/health
```

You should see: `{"status":"ok","watchlist_count":3025,"alias_count":1314,...}`

## Container Image

```
quay.io/kborup/leak-prevention:latest
```

Pull from [quay.io/kborup/leak-prevention](https://quay.io/repository/kborup/leak-prevention). Version tags match the [VERSION](VERSION) file (e.g., `1.2.0`).

## How It Works

Every prompt you send is scanned **before** it reaches the model. If a company or organization name is detected, the prompt is **blocked** and you're shown the detected names with instructions to allowlist them if they're safe to send.

### Three-Phase Detection

1. **Watchlist matching** — Checks your prompt against a curated list of 3000+ company and organization names (Fortune 500 US/Europe, FTSE 100, DAX 40, CAC 40, Nordic, Nikkei 225, ASX 200, S&P/TSX 60, Hang Seng, government entities, international organizations), plus any custom watchlist entries. Case-insensitive matching.

2. **Subsidiary and alias matching** — Checks against known subsidiaries, brand names, and abbreviations linked to parent companies (e.g., `AWS` -> `Amazon`, `YouTube` -> `Alphabet`, `Instagram` -> `Meta`).

3. **Auto-detection** — Flags unknown capitalized words that aren't in the system dictionary or a built-in list of 500+ tech terms. Skips random tokens (mixed case + digits). Catches names not on the watchlist by detecting proper nouns that look like organization names.

### Fail-Closed

If the leak prevention server is not running, the hook **blocks all prompts** and instructs you to start the container. No prompts pass through without scanning.

## Custom Watchlist

Two ways to add custom terms that should be blocked (customer names, legal terms, project code names):

### Build-time: `custom-watchlist.txt`

Add terms to `custom-watchlist.txt` in the repo (one per line). These are merged into the watchlist database when building the container image and persist across rebuilds.

```
# custom-watchlist.txt
Acme Corp
Project Nightfall
Restricted Term
```

After editing, rebuild and push the container:
```bash
./seed-watchlist.sh
podman build -t quay.io/kborup/leak-prevention:latest .
```

### Runtime: CLI or API

Add terms at runtime without rebuilding. These are stored on the Podman volume and persist across container restarts.

```bash
# Add a term
leak-prevention-hook watchlist add "Acme Corp" --category CUSTOMER

# List custom terms
leak-prevention-hook watchlist list

# Remove a term
leak-prevention-hook watchlist remove "Acme Corp"
```

The `--category` flag is optional (defaults to `CUSTOM`). Use it to tag entries by source: `CUSTOMER`, `LEGAL`, `PROJECT`, etc.

<details>
<summary>Alternative: curl</summary>

```bash
curl -s -X POST -H 'Content-Type: application/json' \
  -d '{"term":"Acme Corp","category":"CUSTOMER"}' \
  http://localhost:8642/watchlist/custom

curl -s http://localhost:8642/watchlist/custom

curl -s -X DELETE http://localhost:8642/watchlist/custom/Acme%20Corp
```
</details>

## Architecture

```
┌─────────────────────┐    POST /check     ┌────────────────────────────┐
│ leak-prevention-hook │ ────────────────── │ leak-prevention-server     │
│ (Go CLI binary)      │  localhost:8642    │ (Podman container)         │
│                      │                    │                            │
│ ~/.claude/hooks/     │  ◄── JSON ──       │ watchlist.db (read-only)   │
│                      │                    │ custom-watchlist.db (vol)  │
│                      │                    │ allowlist.db (volume)      │
└─────────────────────┘                    └────────────────────────────┘
```

## Usage

Once installed, the hook runs automatically on every prompt. No action needed.

### When a name is detected

```
Organization name(s) detected: AWS, Google Cloud

To allowlist, run:
  ! /home/user/.claude/hooks/leak-prevention-hook allowlist add "AWS"
  ! /home/user/.claude/hooks/leak-prevention-hook allowlist add "Google Cloud"

Then re-send your message.
```

> The command shows the full path to wherever you installed the hook binary.

### When the server is not running

```
Leak prevention server is not running.

Start it with:
  ! podman start leak-prevention

Then re-send your message.
```

### CLI Commands

The hook binary doubles as a management CLI. No `curl` required.

```bash
# Server status
leak-prevention-hook health

# Allowlist management
leak-prevention-hook allowlist list
leak-prevention-hook allowlist add "AWS"
leak-prevention-hook allowlist remove "AWS"

# Custom watchlist management
leak-prevention-hook watchlist list
leak-prevention-hook watchlist add "Acme Corp"
leak-prevention-hook watchlist add "Acme Corp" --category CUSTOMER
leak-prevention-hook watchlist remove "Acme Corp"

# Help
leak-prevention-hook help
```

## API Endpoints

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/check` | Scan a prompt for leaked names |
| `GET` | `/allowlist` | List all allowlisted terms |
| `POST` | `/allowlist` | Add a term to the allowlist |
| `DELETE` | `/allowlist/{term}` | Remove a term from the allowlist |
| `GET` | `/watchlist/custom` | List custom watchlist entries |
| `POST` | `/watchlist/custom` | Add a custom watchlist entry |
| `DELETE` | `/watchlist/custom/{term}` | Remove a custom watchlist entry |
| `GET` | `/health` | Health check with counts |

## Building from Source

### Requirements

- **Podman** (for running the server container)
- **Go 1.22+** (for building the hook binary)
- **jq** (for configuring settings.json)
- **sqlite3** (for generating watchlist database)

### Automated install

```bash
./install.sh
```

The installer builds the hook binary, builds or pulls the container image, creates the volume, starts the container, and configures `~/.claude/settings.json`.

### Manual build

```bash
# Build hook binary
go build -o ~/.claude/hooks/leak-prevention-hook ./cmd/hook

# Build container image
podman build -t quay.io/kborup/leak-prevention:latest .
```

## Container Management

```bash
# Start
podman start leak-prevention

# Stop
podman stop leak-prevention

# View logs
podman logs leak-prevention

# Rebuild from source
podman build -t quay.io/kborup/leak-prevention:latest .
podman stop leak-prevention && podman rm leak-prevention
podman run -d --name leak-prevention --restart unless-stopped \
  -p 127.0.0.1:8642:8642 -v leak-prevention-data:/data/allowlist:Z \
  quay.io/kborup/leak-prevention:latest
```

## Updating the Watchlist

The `update-watchlist.sh` script uses an AI CLI with web search to fetch current company/organization listings.

```bash
./update-watchlist.sh
```

After updating, seed the database and populate aliases, then rebuild:

```bash
./seed-watchlist.sh
./seed-aliases.sh
podman build -t quay.io/kborup/leak-prevention:latest .
```

### Alias Seeding (Pass 2)

`seed-aliases.sh` populates the aliases table with subsidiaries, brand names, and abbreviations for each company (e.g., `AWS` -> `Amazon`, `YouTube` -> `Alphabet`, `LinkedIn` -> `Microsoft`). Uses the same AI CLI as `update-watchlist.sh`.

```bash
./seed-aliases.sh                           # auto-detect AI CLI
./seed-aliases.sh --provider claude         # use specific CLI
./seed-aliases.sh --dry-run --verbose       # preview without writing
```

### Supported AI CLIs

| CLI | Web Search Method |
|-----|-------------------|
| `claude` | `--allowedTools WebSearch` |
| `gemini` | Built-in Google Search grounding |
| `copilot` | Built-in `web_fetch` tool |
| `chatgpt` | `--search` flag |

### Categories

17 categories: Fortune 500 (US/Europe), DAX 40, CAC 40, FTSE 100, Nordic, Nikkei 225, ASX 200, S&P/TSX 60, Hang Seng, government agencies (US, European, Asian, Middle East, Latin American), and international organizations.
