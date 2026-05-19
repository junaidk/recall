package sentences

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"
)

const source = "tatoeba"

// Backfill picks an example sentence for every word with NULL example_de,
// using FTS5 phrase matching on the lemma against sentence_pairs. Returns
// (matched, missed).
func Backfill(db *sql.DB) (int, int, error) {
	rows, err := db.Query(`SELECT id, lemma FROM words WHERE example_de IS NULL`)
	if err != nil {
		return 0, 0, fmt.Errorf("select words: %w", err)
	}
	type todo struct {
		id    int64
		lemma string
	}
	var todos []todo
	for rows.Next() {
		var t todo
		if err := rows.Scan(&t.id, &t.lemma); err != nil {
			rows.Close()
			return 0, 0, err
		}
		todos = append(todos, t)
	}
	rows.Close()
	if len(todos) == 0 {
		log.Printf("sentences: backfill skipped (all words already have examples)")
		return 0, 0, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	updateStmt, err := tx.Prepare(
		`UPDATE words SET example_de = ?, example_en = ?, example_source = ?, examples_at = ? WHERE id = ?`,
	)
	if err != nil {
		return 0, 0, err
	}
	defer updateStmt.Close()

	matched, missed := 0, 0
	now := time.Now()
	for _, t := range todos {
		de, en, err := findExample(tx, t.lemma)
		if errors.Is(err, sql.ErrNoRows) {
			missed++
			continue
		}
		if err != nil {
			return matched, missed, fmt.Errorf("lookup %q: %w", t.lemma, err)
		}
		if _, err := updateStmt.Exec(de, en, source, now, t.id); err != nil {
			return matched, missed, fmt.Errorf("update %q: %w", t.lemma, err)
		}
		matched++
	}

	if err := tx.Commit(); err != nil {
		return matched, missed, err
	}
	log.Printf("sentences: backfilled %d of %d words (%d had no match)", matched, len(todos), missed)
	return matched, missed, nil
}

// findExample runs an FTS5 phrase query for the lemma and returns the
// shortest matching German sentence with its English translation.
func findExample(tx *sql.Tx, lemma string) (string, string, error) {
	q := ftsPhrase(lemma)
	var de, en string
	err := tx.QueryRow(`
		SELECT sp.de, sp.en
		FROM sentence_pairs_fts f
		JOIN sentence_pairs sp ON sp.id = f.rowid
		WHERE sentence_pairs_fts MATCH ?
		ORDER BY sp.de_len ASC
		LIMIT 1
	`, q).Scan(&de, &en)
	return de, en, err
}

// ftsPhrase wraps the lemma in double quotes and escapes embedded quotes,
// turning multi-word lemmas like "auf jeden Fall" into a phrase query.
func ftsPhrase(lemma string) string {
	return `"` + strings.ReplaceAll(lemma, `"`, `""`) + `"`
}
