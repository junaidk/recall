// Package plurals loads a pre-extracted German noun declension corpus
// (sourced from Wiktionary via kaikki.org) into a local lookup table, then
// backfills the per-word plurals column for nouns in the deck.
package plurals

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const corpusFile = "de_noun_plurals.jsonl"

// Forms holds the four case slots for a single number (singular or plural).
// All fields are optional; the renderer falls back to the lemma when a slot
// is missing.
type Forms struct {
	Nom string `json:"nom,omitempty"`
	Akk string `json:"akk,omitempty"`
	Dat string `json:"dat,omitempty"`
	Gen string `json:"gen,omitempty"`
}

// Entry is one line of the on-disk corpus. The Sg/Pl maps are stored as-is
// in noun_plurals.payload so the runtime never re-parses the original kaikki
// dump.
type Entry struct {
	Lemma string `json:"lemma"`
	Sg    Forms  `json:"sg"`
	Pl    Forms  `json:"pl"`
}

// EnsureCorpus loads the checked-in seed/de_noun_plurals.jsonl file into the
// noun_plurals table when the table is empty. Idempotent — once loaded,
// subsequent boots are no-ops. Missing seed file is logged and skipped, not
// an error: a deployment may legitimately ship without it (e.g. when the
// corpus has not been built yet).
func EnsureCorpus(db *sql.DB, seedDir string) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM noun_plurals`).Scan(&n); err != nil {
		return fmt.Errorf("count noun_plurals: %w", err)
	}
	if n > 0 {
		log.Printf("plurals: corpus already loaded (%d entries)", n)
		return nil
	}

	path := filepath.Join(seedDir, corpusFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("plurals: %s missing, skipping corpus load", path)
			return nil
		}
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO noun_plurals (lemma, payload) VALUES (?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	inserted := 0
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err != nil {
			return fmt.Errorf("parse %s line %d: %w", path, inserted+1, err)
		}
		if e.Lemma == "" {
			continue
		}
		payload, err := json.Marshal(struct {
			Sg Forms `json:"sg"`
			Pl Forms `json:"pl"`
		}{e.Sg, e.Pl})
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(e.Lemma, string(payload)); err != nil {
			return fmt.Errorf("insert %q: %w", e.Lemma, err)
		}
		inserted++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("plurals: corpus loaded %d entries from %s", inserted, path)
	return nil
}
