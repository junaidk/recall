// Package conjugations loads a pre-extracted German verb conjugation corpus
// (sourced from Wiktionary via kaikki.org) into a local lookup table, then
// backfills the per-word conjugations column for verbs in the deck.
package conjugations

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const corpusFile = "de_verb_conjugations.jsonl"

// Entry is one line of the on-disk corpus. The Praesens/Perfekt maps are
// stored as-is in verb_conjugations.payload so the runtime never re-parses
// the original kaikki dump.
type Entry struct {
	Infinitive  string            `json:"infinitive"`
	Praesens    map[string]string `json:"praesens"`
	Praeteritum map[string]string `json:"praeteritum,omitempty"`
	Perfekt     Perfekt           `json:"perfekt"`
}

// Perfekt holds the compound-past auxiliary ("haben"/"sein") and Partizip II.
type Perfekt struct {
	Aux       string `json:"aux"`
	Partizip2 string `json:"partizip2"`
}

// EnsureCorpus loads the checked-in seed/de_verb_conjugations.jsonl file into
// the verb_conjugations table when the table is empty. Idempotent — once
// loaded, subsequent boots are no-ops. Missing seed file is logged and
// skipped, not an error: a deployment may legitimately ship without it
// (e.g. when the corpus has not been built yet).
func EnsureCorpus(db *sql.DB, seedDir string) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM verb_conjugations`).Scan(&n); err != nil {
		return fmt.Errorf("count verb_conjugations: %w", err)
	}
	if n > 0 {
		log.Printf("conjugations: corpus already loaded (%d entries)", n)
		return nil
	}

	path := filepath.Join(seedDir, corpusFile)
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			log.Printf("conjugations: %s missing, skipping corpus load", path)
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

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO verb_conjugations (infinitive, payload) VALUES (?, ?)`)
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
		if e.Infinitive == "" {
			continue
		}
		payload, err := json.Marshal(struct {
			Praesens    map[string]string `json:"praesens"`
			Praeteritum map[string]string `json:"praeteritum,omitempty"`
			Perfekt     Perfekt           `json:"perfekt"`
		}{e.Praesens, e.Praeteritum, e.Perfekt})
		if err != nil {
			return err
		}
		if _, err := stmt.Exec(e.Infinitive, string(payload)); err != nil {
			return fmt.Errorf("insert %q: %w", e.Infinitive, err)
		}
		inserted++
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan %s: %w", path, err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("conjugations: corpus loaded %d entries from %s", inserted, path)
	return nil
}
