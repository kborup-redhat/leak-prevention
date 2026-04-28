#!/usr/bin/env bash
set -euo pipefail

WATCHLIST="${HOME}/.claude/leak-prevention-watchlist.txt"
PROVIDER=""
DRY_RUN=false
VERBOSE=false
MAX_PARALLEL=6

usage() {
  cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Update the leak prevention watchlist using an AI CLI with web search.
Auto-detects available AI CLI (claude, gemini, copilot, chatgpt) or use --provider.
Queries run in parallel for speed (default: ${MAX_PARALLEL} concurrent).

Options:
  --provider <name>    Use a specific AI CLI (claude|gemini|copilot|chatgpt)
  --file <path>        Path to watchlist file (default: ~/.claude/leak-prevention-watchlist.txt)
  --parallel <n>       Max concurrent AI queries (default: ${MAX_PARALLEL})
  --dry-run            Show what would be added without modifying the file
  --verbose            Show detailed progress
  -h, --help           Show this help message
EOF
  exit 0
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --provider) PROVIDER="$2"; shift 2 ;;
    --file) WATCHLIST="$2"; shift 2 ;;
    --parallel) MAX_PARALLEL="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    --verbose) VERBOSE=true; shift ;;
    -h|--help) usage ;;
    *) echo "Unknown option: $1"; usage ;;
  esac
done

log() {
  if $VERBOSE; then
    echo "  [INFO] $*" >&2
  fi
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
    echo "Install one or use --provider to specify."
    exit 1
  fi
  echo "Auto-detected provider: ${PROVIDER}"
else
  if ! command -v "$PROVIDER" &>/dev/null; then
    echo "ERROR: ${PROVIDER} is not installed or not on PATH."
    exit 1
  fi
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
        --model sonnet \
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
    *)
      echo "ERROR: Unsupported provider: ${PROVIDER}" >&2
      exit 1
      ;;
  esac

  echo "$result" > "$output_file"
}

clean_ai_output() {
  sed -E '
    /^[[:space:]]*$/d
    /^#/d
    /^[0-9]+[\.\)]/s/^[0-9]+[\.\)][[:space:]]*//' |
  sed -E '
    s/^[-\*][[:space:]]+//
    s/\[//g
    s/\]//g
    s/\|/ /g
    s/\*\*//g
    s/[[:space:]]*\(.*\)[[:space:]]*$//
    s/[[:space:]]*—.*$//
    s/[[:space:]]*-[[:space:]].*$//
    s/^[[:space:]]+//
    s/[[:space:]]+$//' |
  grep -vEi '^(here|note|these|the |i |below|this|sure|---|```|source|to |you |visit|access|unfortunately|please|search|based|as of|for |if |or |and |check|see |try |go to|http|www\.|however|sorry|cannot|can.t|let me|would|could|should|there|it |a |an |list |top |view |all |types |biggest |cabinet |departments |agencies |organisation|institutions|ministries|a-z )' |
  grep -vEi '(index|companies|largest|government departments|government agencies|government ministries)' |
  grep -vE '^\s*$' |
  grep -vE '^.{80,}$' |
  grep -vE '\.$' |
  grep -vE '^.{0,2}$'
}

declare -A CATEGORIES
CATEGORIES=(
  ["FORTUNE 500 (US)"]="You MUST use web search to find the current Fortune 500 US companies list. Output ONLY the company names, one per line. Do not include numbers, bullets, explanations, headers, markdown, or any other text. Do not say you cannot do it. Search the web and output the names."
  ["FORTUNE 500 EUROPE / MAJOR EUROPEAN"]="You MUST use web search to find the current largest European companies by revenue (Fortune Global 500 European entries). Output ONLY the company names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["DAX 40 (Germany)"]="You MUST use web search to find all current DAX 40 index companies in Germany. Output ONLY the company names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["CAC 40 (France)"]="You MUST use web search to find all current CAC 40 index companies in France. Output ONLY the company names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["FTSE 100 (UK)"]="You MUST use web search to find all current FTSE 100 index companies in the UK. Output ONLY the company names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["NORDIC COMPANIES"]="You MUST use web search to find the top 100 largest Nordic companies (Sweden, Denmark, Norway, Finland, Iceland) by revenue. Output ONLY the company names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["NIKKEI 225 (Japan)"]="You MUST use web search to find the current Nikkei 225 index companies in Japan. Output ONLY the company names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["ASX 200 (Australia)"]="You MUST use web search to find the current ASX 200 index companies in Australia. Output ONLY the company names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["S&P/TSX 60 (Canada)"]="You MUST use web search to find the current S&P/TSX 60 index companies in Canada. Output ONLY the company names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["HANG SENG (Hong Kong)"]="You MUST use web search to find the current Hang Seng Index companies in Hong Kong. Output ONLY the company names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["US GOVERNMENT AGENCIES"]="You MUST use web search to find all major US federal government agencies and departments. Output ONLY the agency/department names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["ASIAN GOVERNMENTS"]="You MUST use web search to list all Asian country government names and their major ministries and agencies. Output ONLY the names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["MIDDLE EAST GOVERNMENTS"]="You MUST use web search to list all Middle Eastern country government names and their major ministries and agencies. Output ONLY the names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["LATIN AMERICAN GOVERNMENTS"]="You MUST use web search to list all Latin American country government names and their major ministries and agencies. Output ONLY the names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["INTERNATIONAL ORGANIZATIONS"]="You MUST use web search to find all major international organizations (UN agencies, World Bank, IMF, NATO, EU institutions, OECD, WTO, etc). Output ONLY the organization names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["EUROPEAN GOVERNMENTS & GOVERNMENT ENTITIES"]="You MUST use web search to find all European country government names and their major ministries, agencies, and EU institutions. Output ONLY the names, one per line. No numbers, bullets, explanations, headers, or markdown."
  ["ADDITIONAL MAJOR EUROPEAN COMPANIES"]="You MUST use web search to find major European companies not typically in the DAX, CAC, or FTSE indices. Include large companies from Benelux, Switzerland, Spain, Italy, and Eastern Europe. Output ONLY the company names, one per line. No numbers, bullets, explanations, headers, or markdown."
)

CATEGORY_ORDER=(
  "FORTUNE 500 (US)"
  "FORTUNE 500 EUROPE / MAJOR EUROPEAN"
  "DAX 40 (Germany)"
  "CAC 40 (France)"
  "FTSE 100 (UK)"
  "NORDIC COMPANIES"
  "NIKKEI 225 (Japan)"
  "ASX 200 (Australia)"
  "S&P/TSX 60 (Canada)"
  "HANG SENG (Hong Kong)"
  "US GOVERNMENT AGENCIES"
  "EUROPEAN GOVERNMENTS & GOVERNMENT ENTITIES"
  "ASIAN GOVERNMENTS"
  "MIDDLE EAST GOVERNMENTS"
  "LATIN AMERICAN GOVERNMENTS"
  "INTERNATIONAL ORGANIZATIONS"
  "ADDITIONAL MAJOR EUROPEAN COMPANIES"
)

if [[ ! -f "$WATCHLIST" ]]; then
  echo "ERROR: Watchlist not found at ${WATCHLIST}"
  echo "Run install.sh first or use --file to specify the path."
  exit 1
fi

EXISTING_NAMES=$(grep -v '^#' "$WATCHLIST" | grep -v '^$' | sort -uf)
EXISTING_COUNT=$(echo "$EXISTING_NAMES" | wc -l)

echo "=== Leak Prevention Watchlist Updater ==="
echo "Provider: ${PROVIDER}"
echo "Watchlist: ${WATCHLIST}"
echo "Existing entries: ${EXISTING_COUNT}"
echo "Categories: ${#CATEGORY_ORDER[@]}"
echo "Parallel queries: ${MAX_PARALLEL}"
if $DRY_RUN; then
  echo "Mode: DRY RUN (no changes will be made)"
fi
echo ""

# --- Phase 1: Query all categories in parallel ---
echo "Phase 1: Querying all categories in parallel..."
echo ""

TMPDIR_RESULTS=$(mktemp -d)
trap "rm -rf ${TMPDIR_RESULTS}" EXIT

declare -A PIDS
running=0

for category in "${CATEGORY_ORDER[@]}"; do
  prompt="${CATEGORIES[$category]}"
  safe_name=$(echo "$category" | tr ' /&' '___')
  output_file="${TMPDIR_RESULTS}/${safe_name}.txt"

  while [[ $running -ge $MAX_PARALLEL ]]; do
    for pid_cat in "${!PIDS[@]}"; do
      pid="${PIDS[$pid_cat]}"
      if ! kill -0 "$pid" 2>/dev/null; then
        wait "$pid" 2>/dev/null || true
        echo "  Done: ${pid_cat}"
        unset "PIDS[$pid_cat]"
        ((running--)) || true
      fi
    done
    if [[ $running -ge $MAX_PARALLEL ]]; then
      sleep 1
    fi
  done

  echo "  Starting: ${category}"
  log "Prompt: ${prompt}"
  query_ai "$prompt" "$output_file" &
  PIDS["$category"]=$!
  ((running++)) || true
done

echo ""
echo "Waiting for remaining queries to finish..."
for pid_cat in "${!PIDS[@]}"; do
  pid="${PIDS[$pid_cat]}"
  wait "$pid" 2>/dev/null || true
  echo "  Done: ${pid_cat}"
done

# --- Phase 2: Merge results sequentially ---
echo ""
echo "Phase 2: Merging results into watchlist..."
echo ""

TOTAL_ADDED=0
ALL_NEW_NAMES=""

for category in "${CATEGORY_ORDER[@]}"; do
  safe_name=$(echo "$category" | tr ' /&' '___')
  output_file="${TMPDIR_RESULTS}/${safe_name}.txt"
  section_header="# === ${category} ==="

  if [[ ! -f "$output_file" ]] || [[ ! -s "$output_file" ]]; then
    echo "  ${category}: WARNING — no response, skipping."
    continue
  fi

  raw_output=$(cat "$output_file")
  cleaned=$(echo "$raw_output" | clean_ai_output || true)
  raw_count=$(echo "$raw_output" | wc -l)
  cleaned_count=$(echo "$cleaned" | grep -c . || true)
  log "${category}: ${raw_count} raw lines -> ${cleaned_count} after cleaning"
  if $VERBOSE; then
    echo "  --- RAW RESPONSE ---"
    echo "$raw_output" | sed 's/^/  | /'
    echo "  --- CLEANED ---"
    echo "$cleaned" | sed 's/^/  | /'
    echo "  ---"
  fi

  new_in_category=0
  new_names_for_section=""

  while IFS= read -r name; do
    [[ -z "$name" ]] && continue
    [[ ${#name} -le 1 ]] && continue
    [[ ${#name} -ge 80 ]] && continue

    if echo "$EXISTING_NAMES" | grep -ixq "^${name}$" 2>/dev/null; then
      continue
    fi

    if [[ -n "$ALL_NEW_NAMES" ]] && echo "$ALL_NEW_NAMES" | grep -ixq "^${name}$" 2>/dev/null; then
      continue
    fi

    new_names_for_section="${new_names_for_section}${name}"$'\n'
    ALL_NEW_NAMES="${ALL_NEW_NAMES}${name}"$'\n'
    ((new_in_category++)) || true
  done <<< "$cleaned"

  if [[ $new_in_category -gt 0 ]]; then
    echo "  ${category}: ${new_in_category} new entries."

    if ! $DRY_RUN; then
      if ! grep -qF "$section_header" "$WATCHLIST" 2>/dev/null; then
        echo "" >> "$WATCHLIST"
        echo "$section_header" >> "$WATCHLIST"
      fi

      section_line=$(grep -nF "$section_header" "$WATCHLIST" | tail -1 | cut -d: -f1)

      next_section_line=$(awk -v start="$((section_line + 1))" '
        NR > start && /^# ===/ { print NR; exit }
      ' "$WATCHLIST")

      if [[ -n "$next_section_line" ]]; then
        insert_at=$((next_section_line - 1))
      else
        insert_at=$(wc -l < "$WATCHLIST")
      fi

      tmp=$(mktemp)
      head -n "$insert_at" "$WATCHLIST" > "$tmp"
      echo -n "$new_names_for_section" >> "$tmp"
      tail -n +"$((insert_at + 1))" "$WATCHLIST" >> "$tmp"
      mv "$tmp" "$WATCHLIST"

      EXISTING_NAMES=$(grep -v '^#' "$WATCHLIST" | grep -v '^$' | sort -uf)
    else
      echo "$new_names_for_section" | while IFS= read -r n; do
        [[ -n "$n" ]] && echo "    + ${n}"
      done
    fi

    TOTAL_ADDED=$((TOTAL_ADDED + new_in_category))
  else
    echo "  ${category}: no new entries."
  fi
done

echo ""
echo "=== Update Complete ==="
FINAL_COUNT=$(grep -v '^#' "$WATCHLIST" | grep -v '^$' | sort -uf | wc -l)
echo "New entries added: ${TOTAL_ADDED}"
echo "Total watchlist entries: ${FINAL_COUNT}"
if $DRY_RUN; then
  echo "(Dry run — no changes were written)"
fi
