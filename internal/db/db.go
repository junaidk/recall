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
	"github.com/junaidk/recall/internal/sentences"
)

//go:embed schema.sql
var schemaSQL string

const currentSchemaVersion = 3

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

	if version < 2 {
		// Null out examples that fail the case filter — e.g. "Schloss" matched
		// with "schloss" (verb). The boot's existing sentences.Backfill call
		// will re-pick a case-appropriate example for each affected word.
		nulled, err := nullStaleExamples(db)
		if err != nil {
			return fmt.Errorf("v2 null stale examples: %w", err)
		}
		log.Printf("migrate v2: nulled %d stale examples (case filter)", nulled)
	}

	if version < 3 {
		if _, err := db.Exec(`ALTER TABLE words ADD COLUMN audio_url TEXT`); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return fmt.Errorf("v3 add audio_url: %w", err)
		}
	}

	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, currentSchemaVersion)); err != nil {
		return err
	}
	log.Printf("migrate: applied user_version=%d (was %d)", currentSchemaVersion, version)
	return nil
}

// nullStaleExamples clears example_de/en/examples_at for any word whose
// persisted German example fails the case filter for its lemma. Returns the
// count of rows reset.
func nullStaleExamples(db *sql.DB) (int, error) {
	rows, err := db.Query(`SELECT id, lemma, example_de FROM words WHERE example_de IS NOT NULL`)
	if err != nil {
		return 0, err
	}
	type row struct {
		id    int64
		lemma string
		ex    string
	}
	var stale []int64
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.lemma, &r.ex); err != nil {
			rows.Close()
			return 0, err
		}
		if !sentences.FilterByCase(r.lemma, r.ex) {
			stale = append(stale, r.id)
		}
	}
	rows.Close()
	if len(stale) == 0 {
		return 0, nil
	}
	tx, err := db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`UPDATE words SET example_de = NULL, example_en = NULL, examples_at = NULL WHERE id = ?`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for _, id := range stale {
		if _, err := stmt.Exec(id); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(stale), nil
}
