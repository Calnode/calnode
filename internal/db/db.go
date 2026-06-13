package db

import (
	"database/sql"
	"embed"
	"fmt"
	"strings"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open connects to SQLite at the given URL and configures pragmas.
// URL format: sqlite://./path/to/db or sqlite:///absolute/path or just a file path.
func Open(databaseURL string) (*sql.DB, error) {
	dsn := parseDSN(databaseURL)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// SQLite performs best with a single writer connection; WAL allows
	// concurrent readers. Keeping max open conns at 1 prevents "database is
	// locked" under concurrent writes without WAL tuning.
	// SetConnMaxLifetime(0) and SetMaxIdleConns(1) ensure the single connection
	// is never recycled — PRAGMAs are connection-scoped and must not be lost.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		return nil, fmt.Errorf("set WAL mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		return nil, fmt.Errorf("set busy timeout: %w", err)
	}

	return db, nil
}

// Migrate runs any pending Goose migrations embedded in migrations/*.sql.
func Migrate(db *sql.DB) error {
	goose.SetBaseFS(migrations)

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}

	return nil
}

func parseDSN(url string) string {
	// Strip scheme prefix: sqlite:// → remainder
	dsn := strings.TrimPrefix(url, "sqlite://")
	// sqlite:///absolute/path → /absolute/path (triple slash, first two stripped above)
	// sqlite://./relative → ./relative
	return dsn
}
