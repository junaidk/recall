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

	"github.com/junaidk/recall/internal/seedmeta"
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
// the verb_conjugations table. The table's content hash is tracked in meta:
// the load is a no-op when the table is populated and the seed is unchanged,
// but a rebuilt seed (different hash) triggers a full reload so updates
// propagate on the next boot. Returns changed=true when the table was
// (re)loaded, signalling the caller to force a full re-backfill.
//
// A missing seed file is logged and skipped, not an error: a deployment may
// legitimately ship without it (e.g. when the corpus has not been built yet).
func EnsureCorpus(db *sql.DB, seedDir string) (changed bool, err error) {
	path := filepath.Join(seedDir, corpusFile)
	hash, exists, err := seedmeta.FileHash(path)
	if err != nil {
		return false, fmt.Errorf("hash %s: %w", path, err)
	}
	if !exists {
		log.Printf("conjugations: %s missing, skipping corpus load", path)
		return false, nil
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM verb_conjugations`).Scan(&n); err != nil {
		return false, fmt.Errorf("count verb_conjugations: %w", err)
	}
	hashKey := seedmeta.CorpusHashKey(corpusFile)
	stored, err := seedmeta.Get(db, hashKey)
	if err != nil {
		return false, fmt.Errorf("read corpus hash: %w", err)
	}
	if n > 0 && stored == hash {
		log.Printf("conjugations: corpus up to date (%d entries)", n)
		return false, nil
	}

	f, err := os.Open(path)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer f.Close()

	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	// Clear first so infinitives dropped from a rebuilt seed don't linger.
	if _, err := tx.Exec(`DELETE FROM verb_conjugations`); err != nil {
		return false, err
	}

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO verb_conjugations (infinitive, payload) VALUES (?, ?)`)
	if err != nil {
		return false, err
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
			return false, fmt.Errorf("parse %s line %d: %w", path, inserted+1, err)
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
			return false, err
		}
		if _, err := stmt.Exec(e.Infinitive, string(payload)); err != nil {
			return false, fmt.Errorf("insert %q: %w", e.Infinitive, err)
		}
		inserted++
	}
	if err := scanner.Err(); err != nil {
		return false, fmt.Errorf("scan %s: %w", path, err)
	}
	if err := seedmeta.Set(tx, hashKey, hash); err != nil {
		return false, fmt.Errorf("store corpus hash: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if stored == "" {
		log.Printf("conjugations: corpus loaded %d entries from %s", inserted, path)
	} else {
		log.Printf("conjugations: corpus reloaded %d entries from %s (seed changed)", inserted, path)
	}
	return true, nil
}
