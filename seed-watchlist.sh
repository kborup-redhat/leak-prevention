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
    [[ -z "$line" ]] && continue

    if [[ "$line" =~ ^#\ ===\ (.+)\ ===$ ]]; then
      current_category="${BASH_REMATCH[1]}"
      continue
    fi

    [[ "$line" == \#* ]] && continue

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

rm -f "$DB_OUT"
sqlite3 "$DB_OUT" < "$SEED_SQL"

CUSTOM_WATCHLIST="${SCRIPT_DIR}/custom-watchlist.txt"
if [[ -f "$CUSTOM_WATCHLIST" ]]; then
  echo "Merging custom watchlist entries..."
  while IFS= read -r line; do
    [[ -z "$line" ]] && continue
    [[ "$line" == \#* ]] && continue
    escaped="${line//\'/\'\'}"
    sqlite3 "$DB_OUT" "INSERT OR IGNORE INTO companies (name, category) VALUES ('${escaped}', 'CUSTOM');"
  done < "$CUSTOM_WATCHLIST"
  CUSTOM_COUNT=$(sqlite3 "$DB_OUT" "SELECT COUNT(*) FROM companies WHERE category = 'CUSTOM';")
  echo "  Added ${CUSTOM_COUNT} custom entries."
fi

ALIASES_SQL="${SCRIPT_DIR}/seed-aliases.sql"
if [[ -f "$ALIASES_SQL" ]]; then
  echo "Loading aliases..."
  sqlite3 "$DB_OUT" < "$ALIASES_SQL"
  ALIAS_COUNT=$(sqlite3 "$DB_OUT" "SELECT COUNT(*) FROM aliases;")
  echo "  Loaded ${ALIAS_COUNT} aliases."
else
  ALIAS_COUNT=0
  echo "No seed-aliases.sql found (run seed-aliases.sh to generate)."
fi

COMPANY_COUNT=$(sqlite3 "$DB_OUT" "SELECT COUNT(*) FROM companies;")
echo ""
echo "Database created:"
echo "  Companies: ${COMPANY_COUNT}"
echo "  Aliases:   ${ALIAS_COUNT}"
echo ""

sqlite3 "$DB_OUT" "PRAGMA integrity_check;" | head -1
echo ""
echo "Done: ${DB_OUT}"
