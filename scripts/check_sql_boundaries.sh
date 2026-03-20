#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
cd "$ROOT_DIR"

violations=0

check_pattern() {
  local pattern="$1"
  local label="$2"
  local results
  results="$(rg -n "$pattern" services hub pkg -S \
    -g '!services/sql_db/**' \
    -g '!hub/internal/app/sqlite_open.go' \
    -g '!hub/internal/app/user_store.go' \
    -g '!hub/internal/app/startup_snapshot_store.go' \
    -g '!services/sql_db/internal/app/sqlite_open.go' || true)"
  if [[ -n "$results" ]]; then
    printf 'SQL boundary violation (%s):\n%s\n' "$label" "$results"
    violations=1
  fi
}

check_pattern 'modernc\.org/sqlite' 'driver import'
check_pattern 'sql\.Open\("sqlite"' 'direct sqlite open'
check_pattern 'pkg/sqlitedriver' 'shared sqlite helper import'

if [[ "$violations" -ne 0 ]]; then
  exit 1
fi

echo "SQL boundaries OK"
