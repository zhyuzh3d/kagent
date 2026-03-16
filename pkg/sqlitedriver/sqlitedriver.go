package sqlitedriver

import (
	"database/sql"
	"strings"

	_ "modernc.org/sqlite"
)

const DriverName = "sqlite"

func Open(path string) (*sql.DB, error) {
	return sql.Open(DriverName, strings.TrimSpace(path))
}
