package db

import (
	_ "embed"
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schemaSQL string

const currentSchemaVersion = 1

func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_foreign_keys=on&_journal_mode=WAL", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping db: %w", err)
	}
	if _, err := db.Exec(schemaSQL); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}
	return db, nil
}

// migrate brings legacy databases (created before columns/tables were added
// to schema.sql) up to currentSchemaVersion. New databases get the latest
// columns straight from schema.sql, so the migrations are no-ops for them.
func migrate(db *sql.DB) error {
	var version int
	if err := db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version >= currentSchemaVersion {
		return nil
	}

	if version < 1 {
		// Add example columns to existing words tables. On a fresh DB these
		// already exist (from schema.sql), so swallow "duplicate column" errors.
		alters := []string{
			`ALTER TABLE words ADD COLUMN example_de TEXT`,
			`ALTER TABLE words ADD COLUMN example_en TEXT`,
			`ALTER TABLE words ADD COLUMN example_source TEXT`,
			`ALTER TABLE words ADD COLUMN examples_at DATETIME`,
		}
		for _, stmt := range alters {
			if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
				return fmt.Errorf("%s: %w", stmt, err)
			}
		}
	}

	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, currentSchemaVersion)); err != nil {
		return err
	}
	log.Printf("migrate: applied user_version=%d (was %d)", currentSchemaVersion, version)
	return nil
}
