package plurals

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// emptyPayload is written when a noun has no declension match in the corpus,
// so future boots do not retry the same lookups indefinitely. The template
// gate (Plurals != nil) prevents this from rendering an empty panel.
const emptyPayload = "{}"

// Backfill populates words.plurals for nouns (pos='Substantiv' and not
// plural-only) by joining by lemma against the noun_plurals corpus loaded by
// EnsureCorpus. Nouns with no corpus match get an empty JSON object so they
// are not re-attempted on later boots. Returns (matched, missed).
//
// When force is false, only nouns missing a payload (plurals IS NULL) are
// filled — the cheap incremental path for newly imported words. When force is
// true (the seed corpus changed this boot), every noun is re-derived so the
// new data — and any previously missed lemma now present — propagates.
//
// If the corpus is empty (no seed file shipped yet), backfill is a no-op so
// that the next boot — once the corpus is populated — still gets a chance at
// every noun.
func Backfill(db *sql.DB, force bool) (int, int, error) {
	var corpus int
	if err := db.QueryRow(`SELECT COUNT(*) FROM noun_plurals`).Scan(&corpus); err != nil {
		return 0, 0, fmt.Errorf("count noun_plurals: %w", err)
	}
	if corpus == 0 {
		log.Printf("plurals: backfill skipped (corpus empty)")
		return 0, 0, nil
	}

	query := `
		SELECT w.id, w.lemma, np.payload
		FROM words w
		LEFT JOIN noun_plurals np ON np.lemma = w.lemma
		WHERE w.pos = 'Substantiv' AND w.only_plural = 0`
	if !force {
		query += ` AND w.plurals IS NULL`
	}
	rows, err := db.Query(query)
	if err != nil {
		return 0, 0, fmt.Errorf("select nouns: %w", err)
	}
	type todo struct {
		id      int64
		lemma   string
		payload sql.NullString
	}
	var todos []todo
	for rows.Next() {
		var t todo
		if err := rows.Scan(&t.id, &t.lemma, &t.payload); err != nil {
			rows.Close()
			return 0, 0, err
		}
		todos = append(todos, t)
	}
	rows.Close()
	if len(todos) == 0 {
		log.Printf("plurals: backfill skipped (all nouns already filled)")
		return 0, 0, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE words SET plurals = ?, plurals_at = ? WHERE id = ?`)
	if err != nil {
		return 0, 0, err
	}
	defer stmt.Close()

	matched, missed := 0, 0
	now := time.Now()
	for _, t := range todos {
		payload := emptyPayload
		if t.payload.Valid && t.payload.String != "" {
			payload = t.payload.String
			matched++
		} else {
			missed++
		}
		if _, err := stmt.Exec(payload, now, t.id); err != nil {
			return matched, missed, fmt.Errorf("update %q: %w", t.lemma, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return matched, missed, err
	}
	log.Printf("plurals: backfilled %d of %d nouns (%d had no match)", matched, len(todos), missed)
	return matched, missed, nil
}
