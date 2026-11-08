package sqlite

import (
	"database/sql"

	_ "modernc.org/sqlite" // Registers the pure-Go database/sql driver.
)

func openDriver(path string) (*sql.DB, error) {
	return sql.Open("sqlite", path)
}
