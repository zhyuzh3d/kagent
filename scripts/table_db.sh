#!/bin/bash
# scripts/table_db.sh
# Usage: ./scripts/table_db.sh [table_name] [limit]

# Find the script's directory and project root
SCRIPT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
PROJECT_ROOT=$(dirname "$SCRIPT_DIR")

TABLE=${1:-messages}
LIMIT=${2:-20}
DB_PATH="$PROJECT_ROOT/data/kagent.db"
ANA_DIR="$PROJECT_ROOT/ana"

# Ensure ana directory exists in project root
mkdir -p "$ANA_DIR"

# Generate timestamp: yymmdd-hhmmss
TIMESTAMP=$(date +"%y%m%d-%H%M%S")
FILENAME="${TIMESTAMP}-${TABLE}-dbtable.md"
OUTPUT_FILE="${ANA_DIR}/${FILENAME}"

echo "# Database Table: $TABLE (Last $LIMIT records)" > "$OUTPUT_FILE"
echo "" >> "$OUTPUT_FILE"
echo "Generated at: $(date '+%Y-%m-%d %H:%M:%S')" >> "$OUTPUT_FILE"
echo "" >> "$OUTPUT_FILE"

# Check if DB exists
if [ ! -f "$DB_PATH" ]; then
    echo "Error: Database not found at $DB_PATH"
    echo "Looking at: $DB_PATH"
    exit 1
fi

# Use sqlite3 -markdown to export data, ordering by created_at_ms
sqlite3 -markdown "$DB_PATH" "SELECT * FROM $TABLE ORDER BY created_at_ms DESC LIMIT $LIMIT;" >> "$OUTPUT_FILE"

echo "Exported $LIMIT records from $TABLE to $OUTPUT_FILE"
