#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CLAUDE_DIR="${HOME}/.claude"
HOOKS_DIR="${CLAUDE_DIR}/hooks"
SETTINGS_FILE="${CLAUDE_DIR}/settings.json"

IMAGE="quay.io/kborup/leak-prevention:latest"
CONTAINER_NAME="leak-prevention"
VOLUME_NAME="leak-prevention-data"
PORT=8642

HOOK_BINARY="leak-prevention-hook"
HOOK_CMD="${HOOKS_DIR}/${HOOK_BINARY}"

echo "=== Claude Code Leak Prevention Hook Installer ==="
echo ""

# --- Check prerequisites ---
MISSING=()
command -v jq &>/dev/null || MISSING+=("jq")
command -v podman &>/dev/null || MISSING+=("podman")
command -v go &>/dev/null || MISSING+=("go")
command -v sqlite3 &>/dev/null || MISSING+=("sqlite3")

if [[ ${#MISSING[@]} -gt 0 ]]; then
  echo "ERROR: Missing required tools: ${MISSING[*]}"
  echo "Install them with: sudo dnf install ${MISSING[*]}"
  exit 1
fi

mkdir -p "$HOOKS_DIR"

# --- Ensure dictionary file exists for Go embed ---
WORDS_FILE="${SCRIPT_DIR}/internal/matcher/words.txt"
if [[ ! -f "$WORDS_FILE" ]]; then
  if [[ -f /usr/share/dict/words ]]; then
    mkdir -p "$(dirname "$WORDS_FILE")"
    tr '[:upper:]' '[:lower:]' < /usr/share/dict/words | sort -u > "$WORDS_FILE"
  else
    touch "$WORDS_FILE"
  fi
fi

# --- Step 1: Build the hook CLI binary ---
echo "Step 1: Building hook binary..."
if [[ -d "${SCRIPT_DIR}/cmd/hook" ]]; then
  (cd "$SCRIPT_DIR" && go build -o "$HOOK_CMD" ./cmd/hook)
  chmod +x "$HOOK_CMD"
  echo "  Installed: ${HOOK_CMD}"
else
  echo "  ERROR: cmd/hook/ not found. Run from the project directory."
  exit 1
fi

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

# --- Step 2: Build or pull the container image ---
echo ""
echo "Step 2: Setting up container image..."

if [[ -f "${SCRIPT_DIR}/Containerfile" ]]; then
  echo "  Building image from source..."
  podman build -t "$IMAGE" "$SCRIPT_DIR"
  echo "  Built: ${IMAGE}"
else
  echo "  Pulling image from registry..."
  podman pull "$IMAGE"
  echo "  Pulled: ${IMAGE}"
fi

# --- Step 3: Create Podman volume ---
echo ""
echo "Step 3: Setting up storage volume..."
if podman volume inspect "$VOLUME_NAME" &>/dev/null; then
  echo "  Volume ${VOLUME_NAME} already exists."
else
  podman volume create "$VOLUME_NAME"
  echo "  Created volume: ${VOLUME_NAME}"
fi

# --- Step 4: Start the container ---
echo ""
echo "Step 4: Starting container..."

if podman container exists "$CONTAINER_NAME" 2>/dev/null; then
  RUNNING=$(podman inspect --format '{{.State.Running}}' "$CONTAINER_NAME" 2>/dev/null || echo "false")
  if [[ "$RUNNING" == "true" ]]; then
    echo "  Container ${CONTAINER_NAME} is already running."
  else
    podman start "$CONTAINER_NAME"
    echo "  Started existing container: ${CONTAINER_NAME}"
  fi
else
  podman run -d \
    --name "$CONTAINER_NAME" \
    --restart unless-stopped \
    -p "127.0.0.1:${PORT}:${PORT}" \
    -v "${VOLUME_NAME}:/data/allowlist:Z" \
    "$IMAGE"
  echo "  Started new container: ${CONTAINER_NAME}"
fi

# --- Step 5: Configure hook in settings.json ---
echo ""
echo "Step 5: Configuring Claude Code hook..."

HOOK_ENTRY='{
  "hooks": [
    {
      "type": "command",
      "command": "'"${HOOK_CMD}"'",
      "timeout": 10,
      "statusMessage": "Scanning for data leaks..."
    }
  ]
}'

if [[ ! -f "$SETTINGS_FILE" ]]; then
  echo '{}' > "$SETTINGS_FILE"
fi

CURRENT=$(cat "$SETTINGS_FILE")

# Remove any old shell-based hook entries
CURRENT=$(echo "$CURRENT" | jq '
  if .hooks.UserPromptSubmit then
    .hooks.UserPromptSubmit |= map(
      select(.hooks | all(.command | test("leak-prevention") | not))
    )
  else . end
')

# Check if new hook is already configured
ALREADY_INSTALLED=$(echo "$CURRENT" | jq -r '
  .hooks.UserPromptSubmit // [] | map(.hooks[]?.command) | flatten | any(. == "'"${HOOK_CMD}"'")
' 2>/dev/null || echo "false")

if [[ "$ALREADY_INSTALLED" == "true" ]]; then
  echo "  Hook is already configured in settings.json."
else
  UPDATED=$(echo "$CURRENT" | jq --argjson hook "$HOOK_ENTRY" '
    .hooks.UserPromptSubmit = ((.hooks.UserPromptSubmit // []) + [$hook])
  ')
  echo "$UPDATED" > "$SETTINGS_FILE"
  echo "  Hook added to settings.json."
fi

# --- Step 6: Health check ---
echo ""
echo "Step 6: Verifying API..."

RETRIES=5
for i in $(seq 1 $RETRIES); do
  if curl -sf "http://localhost:${PORT}/health" >/dev/null 2>&1; then
    HEALTH=$(curl -s "http://localhost:${PORT}/health")
    echo "  API is healthy: ${HEALTH}"
    break
  fi
  if [[ $i -eq $RETRIES ]]; then
    echo "  WARNING: API not responding after ${RETRIES} attempts."
    echo "  Check container logs: podman logs ${CONTAINER_NAME}"
  else
    sleep 1
  fi
done

# --- Done ---
echo ""
echo "=== Installation complete ==="
echo ""
echo "Components installed:"
echo "  Hook binary:  ${HOOK_CMD}"
echo "  Container:    ${CONTAINER_NAME} (${IMAGE})"
echo "  Volume:       ${VOLUME_NAME}"
echo "  API:          http://localhost:${PORT}"
echo ""
echo "The hook will scan every prompt for company/organization names,"
echo "subsidiaries, and brand names before sending to the model."
echo "If a name is detected, the prompt is blocked with instructions"
echo "to allowlist if needed."
echo ""
echo "Manage the allowlist via API:"
echo "  List:    curl -s http://localhost:${PORT}/allowlist"
echo "  Add:     curl -s -X POST -H 'Content-Type: application/json' -d '{\"term\":\"SafeTerm\"}' http://localhost:${PORT}/allowlist"
echo "  Remove:  curl -s -X DELETE http://localhost:${PORT}/allowlist/SafeTerm"
echo ""
echo "Container management:"
echo "  Start:   podman start ${CONTAINER_NAME}"
echo "  Stop:    podman stop ${CONTAINER_NAME}"
echo "  Logs:    podman logs ${CONTAINER_NAME}"
echo "  Rebuild: podman build -t ${IMAGE} ${SCRIPT_DIR} && podman stop ${CONTAINER_NAME} && podman rm ${CONTAINER_NAME} && ./install.sh"
