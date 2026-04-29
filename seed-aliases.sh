#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DB="${SCRIPT_DIR}/watchlist.db"
PROVIDER=""
DRY_RUN=false
VERBOSE=false
BATCH_SIZE=20
MAX_PARALLEL=4

usage() {
  cat <<EOF
Usage: $(basename "$0") [OPTIONS] [watchlist.db]

Populate the aliases table with subsidiaries, brand names, and abbreviations
for companies in the watchlist database. Uses an AI CLI with web search.

Options:
  --provider <name>    Use a specific AI CLI (claude|gemini|copilot|chatgpt)
  --batch-size <n>     Companies per AI query (default: ${BATCH_SIZE})
  --parallel <n>       Max concurrent AI queries (default: ${MAX_PARALLEL})
  --dry-run            Show what would be added without modifying the database
  --verbose            Show detailed progress
  -h, --help           Show this help message
EOF
  exit 0
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --provider) PROVIDER="$2"; shift 2 ;;
    --batch-size) BATCH_SIZE="$2"; shift 2 ;;
    --parallel) MAX_PARALLEL="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    --verbose) VERBOSE=true; shift ;;
    -h|--help) usage ;;
    *) if [[ -f "$1" ]]; then DB="$1"; shift; else echo "Unknown option: $1"; usage; fi ;;
  esac
done

log() {
  if $VERBOSE; then echo "  [INFO] $*" >&2; fi
}

detect_provider() {
  for cmd in claude gemini copilot chatgpt; do
    if command -v "$cmd" &>/dev/null; then
      echo "$cmd"
      return
    fi
  done
  echo ""
}

if [[ -z "$PROVIDER" ]]; then
  PROVIDER=$(detect_provider)
  if [[ -z "$PROVIDER" ]]; then
    echo "ERROR: No supported AI CLI found (claude, gemini, copilot, chatgpt)."
    exit 1
  fi
  echo "Auto-detected provider: ${PROVIDER}"
fi

if [[ ! -f "$DB" ]]; then
  echo "ERROR: Database not found at ${DB}"
  echo "Run seed-watchlist.sh first."
  exit 1
fi

query_ai() {
  local prompt="$1"
  local output_file="$2"
  local result=""

  case "$PROVIDER" in
    claude)
      result=$(claude -p "$prompt" \
        --allowedTools "WebSearch" \
        --no-session-persistence \
        --bare \
        --model haiku \
        2>/dev/null) || true
      ;;
    gemini)
      result=$(gemini -p "$prompt" \
        --sandbox \
        2>/dev/null) || true
      ;;
    copilot)
      result=$(copilot -p "$prompt" \
        --allow-all-tools \
        2>/dev/null) || true
      ;;
    chatgpt)
      result=$(chatgpt --search "$prompt" \
        2>/dev/null) || true
      ;;
  esac

  echo "$result" > "$output_file"
}

EXISTING_ALIASES=$(sqlite3 "$DB" "SELECT COUNT(*) FROM aliases;")
COMPANY_COUNT=$(sqlite3 "$DB" "SELECT COUNT(*) FROM companies;")

echo "=== Seed Aliases (Pass 2) ==="
echo "Provider: ${PROVIDER}"
echo "Database: ${DB}"
echo "Companies: ${COMPANY_COUNT}"
echo "Existing aliases: ${EXISTING_ALIASES}"
echo "Batch size: ${BATCH_SIZE}"
echo "Parallel queries: ${MAX_PARALLEL}"
if $DRY_RUN; then echo "Mode: DRY RUN"; fi
echo ""

TMPDIR=$(mktemp -d)
trap "rm -rf ${TMPDIR}" EXIT

# Get all companies with their IDs
sqlite3 "$DB" "SELECT id, name FROM companies ORDER BY id;" | while IFS='|' read -r id name; do
  echo "${id}|${name}"
done > "${TMPDIR}/companies.txt"

TOTAL=$(wc -l < "${TMPDIR}/companies.txt")

# Split into batches
split -l "$BATCH_SIZE" -d -a 4 "${TMPDIR}/companies.txt" "${TMPDIR}/batch_"
BATCH_FILES=(${TMPDIR}/batch_*)
BATCH_COUNT=${#BATCH_FILES[@]}

echo "Processing ${TOTAL} companies in ${BATCH_COUNT} batches..."
echo ""

TOTAL_ADDED=0
batch_num=0
running=0
declare -A PIDS
declare -A BATCH_NUMS

for batch_file in "${BATCH_FILES[@]}"; do
  ((batch_num++)) || true

  # Build the company list for the prompt
  company_list=""
  while IFS='|' read -r id name; do
    company_list="${company_list}${name}\n"
  done < "$batch_file"

  prompt="For each company below, list their well-known subsidiaries, brand names, product names, and common abbreviations that people might use instead of the parent company name. Format: one line per alias as PARENT_NAME|ALIAS. Only include widely recognized names. Skip companies with no notable aliases. Do not include the parent company name itself as an alias. No explanations, headers, or markdown.

Companies:
$(echo -e "$company_list")"

  output_file="${TMPDIR}/result_$(printf '%04d' $batch_num).txt"

  # Throttle parallel queries
  while [[ $running -ge $MAX_PARALLEL ]]; do
    for pid_key in "${!PIDS[@]}"; do
      if ! kill -0 "${PIDS[$pid_key]}" 2>/dev/null; then
        wait "${PIDS[$pid_key]}" 2>/dev/null || true
        unset "PIDS[$pid_key]"
        ((running--)) || true
      fi
    done
    if [[ $running -ge $MAX_PARALLEL ]]; then sleep 1; fi
  done

  echo "  Batch ${batch_num}/${BATCH_COUNT} ($(wc -l < "$batch_file") companies)..."
  query_ai "$prompt" "$output_file" &
  PIDS["batch_${batch_num}"]=$!
  BATCH_NUMS["batch_${batch_num}"]=$batch_num
  ((running++)) || true
done

# Wait for all remaining
echo ""
echo "Waiting for remaining queries..."
for pid_key in "${!PIDS[@]}"; do
  wait "${PIDS[$pid_key]}" 2>/dev/null || true
done
echo "All queries complete."
echo ""

# Process results
echo "Inserting aliases into database..."

SQL_FILE="${TMPDIR}/aliases.sql"
echo "BEGIN TRANSACTION;" > "$SQL_FILE"

for batch_file in "${BATCH_FILES[@]}"; do
  batch_idx=$(echo "$batch_file" | grep -oE '[0-9]+$')
  result_file="${TMPDIR}/result_$(printf '%04d' $((10#$batch_idx + 1))).txt"

  if [[ ! -f "$result_file" ]] || [[ ! -s "$result_file" ]]; then
    log "No results for batch ${batch_idx}"
    continue
  fi

  # Build a lookup map from company name to ID for this batch
  declare -A NAME_TO_ID
  while IFS='|' read -r id name; do
    NAME_TO_ID["$(echo "$name" | tr '[:upper:]' '[:lower:]')"]="$id"
  done < "$batch_file"

  # Parse AI output: expect PARENT_NAME|ALIAS format
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    [[ "$line" == \#* ]] && continue
    [[ "$line" =~ ^[[:space:]]*$ ]] && continue

    # Clean markdown artifacts
    line=$(echo "$line" | sed -E 's/^\*\*//; s/\*\*$//; s/^[-\*][[:space:]]+//; s/^[0-9]+[\.\)][[:space:]]*//')

    # Must contain a pipe separator
    if [[ "$line" != *"|"* ]]; then continue; fi

    parent=$(echo "$line" | cut -d'|' -f1 | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')
    alias=$(echo "$line" | cut -d'|' -f2 | sed 's/^[[:space:]]*//;s/[[:space:]]*$//')

    [[ -z "$parent" ]] && continue
    [[ -z "$alias" ]] && continue
    [[ ${#alias} -le 1 ]] && continue
    [[ ${#alias} -ge 80 ]] && continue

    parent_lower=$(echo "$parent" | tr '[:upper:]' '[:lower:]')
    company_id="${NAME_TO_ID[$parent_lower]:-}"

    if [[ -z "$company_id" ]]; then
      # Try fuzzy match
      company_id=$(sqlite3 "$DB" "SELECT id FROM companies WHERE LOWER(name) = '$(echo "$parent_lower" | sed "s/'/''/g")' LIMIT 1;" 2>/dev/null || true)
    fi

    if [[ -z "$company_id" ]]; then
      log "No match for parent: ${parent}"
      continue
    fi

    alias_escaped="${alias//\'/\'\'}"
    echo "INSERT OR IGNORE INTO aliases (company_id, alias) VALUES (${company_id}, '${alias_escaped}');" >> "$SQL_FILE"
    ((TOTAL_ADDED++)) || true

    if $VERBOSE; then
      echo "    ${parent} -> ${alias} (company_id=${company_id})"
    fi
  done < "$result_file"

  unset NAME_TO_ID
done

echo "COMMIT;" >> "$SQL_FILE"

ALIAS_INSERTS=$(grep -c "INSERT" "$SQL_FILE" || true)
echo ""
echo "Generated ${ALIAS_INSERTS} alias INSERT statements."

if $DRY_RUN; then
  echo "(Dry run — no changes written)"
  if $VERBOSE; then
    echo ""
    echo "SQL preview:"
    head -50 "$SQL_FILE"
  fi
else
  sqlite3 "$DB" < "$SQL_FILE"
  FINAL_COUNT=$(sqlite3 "$DB" "SELECT COUNT(*) FROM aliases;")
  echo "Aliases in database: ${FINAL_COUNT}"
fi

echo ""
echo "=== Done ==="
