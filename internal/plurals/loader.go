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

	"github.com/junaidk/recall/internal/seedmeta"
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
// noun_plurals table. The table's content hash is tracked in meta: the load
// is a no-op when the table is populated and the seed is unchanged, but a
// rebuilt seed (different hash) triggers a full reload so updates propagate on
// the next boot. Returns changed=true when the table was (re)loaded,
// signalling the caller to force a full re-backfill.
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
		log.Printf("plurals: %s missing, skipping corpus load", path)
		return false, nil
	}

	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM noun_plurals`).Scan(&n); err != nil {
		return false, fmt.Errorf("count noun_plurals: %w", err)
	}
	hashKey := seedmeta.CorpusHashKey(corpusFile)
	stored, err := seedmeta.Get(db, hashKey)
	if err != nil {
		return false, fmt.Errorf("read corpus hash: %w", err)
	}
	if n > 0 && stored == hash {
		log.Printf("plurals: corpus up to date (%d entries)", n)
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

	// Clear first so lemmas dropped from a rebuilt seed don't linger.
	if _, err := tx.Exec(`DELETE FROM noun_plurals`); err != nil {
		return false, err
	}

	stmt, err := tx.Prepare(`INSERT OR REPLACE INTO noun_plurals (lemma, payload) VALUES (?, ?)`)
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
		if e.Lemma == "" {
			continue
		}
		payload, err := json.Marshal(struct {
			Sg Forms `json:"sg"`
			Pl Forms `json:"pl"`
		}{e.Sg, e.Pl})
		if err != nil {
			return false, err
		}
		if _, err := stmt.Exec(e.Lemma, string(payload)); err != nil {
			return false, fmt.Errorf("insert %q: %w", e.Lemma, err)
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
		log.Printf("plurals: corpus loaded %d entries from %s", inserted, path)
	} else {
		log.Printf("plurals: corpus reloaded %d entries from %s (seed changed)", inserted, path)
	}
	return true, nil
}
