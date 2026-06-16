package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"sync"

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

var (
	targetVersionOnce sync.Once
	targetVersion     int64
	targetVersionErr  error
)

// TargetVersion returns the highest migration version embedded in the binary —
// i.e. the schema version a fully-migrated database should report.
func TargetVersion() (int64, error) {
	targetVersionOnce.Do(func() {
		entries, err := fs.ReadDir(migrations, "migrations")
		if err != nil {
			targetVersionErr = fmt.Errorf("read embedded migrations: %w", err)
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
				continue
			}
			// Filenames are "NNNNN_description.sql"; the leading number is the version.
			name := path.Base(e.Name())
			numPart, _, _ := strings.Cut(name, "_")
			v, err := strconv.ParseInt(numPart, 10, 64)
			if err != nil {
				continue // ignore files that don't follow the goose naming convention
			}
			if v > targetVersion {
				targetVersion = v
			}
		}
	})
	return targetVersion, targetVersionErr
}

// AppliedVersion returns the schema version currently applied to db by reading
// goose's bookkeeping table directly (no goose global state). A missing
// goose_db_version table returns an error, which callers treat as "not migrated".
func AppliedVersion(ctx context.Context, db *sql.DB) (int64, error) {
	var v sql.NullInt64
	err := db.QueryRowContext(ctx,
		`SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1`).Scan(&v)
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 0, nil
	}
	return v.Int64, nil
}

// SchemaReady reports whether db has been migrated to the embedded target
// version. The provisioner / load balancer can poll /readyz, which calls this,
// to gate traffic until migrations have finished.
func SchemaReady(ctx context.Context, db *sql.DB) (bool, error) {
	target, err := TargetVersion()
	if err != nil {
		return false, err
	}
	applied, err := AppliedVersion(ctx, db)
	if err != nil {
		return false, err
	}
	return applied >= target, nil
}

func parseDSN(url string) string {
	// Strip scheme prefix: sqlite:// → remainder
	dsn := strings.TrimPrefix(url, "sqlite://")
	// sqlite:///absolute/path → /absolute/path (triple slash, first two stripped above)
	// sqlite://./relative     → ./relative
	// On Windows, sqlite:///C:/path/db → /C:/path/db — strip the leading slash so
	// the result is a valid Windows absolute path (C:/path/db).
	if len(dsn) >= 3 && dsn[0] == '/' && dsn[2] == ':' {
		dsn = dsn[1:]
	}
	return dsn
}
