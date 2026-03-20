package app

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

const sqliteDriverName = "sqlite"

func OpenSQLite(path string) (*sql.DB, error) {
	return sql.Open(sqliteDriverName, strings.TrimSpace(path))
}
