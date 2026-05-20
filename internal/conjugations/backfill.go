package conjugations

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

// emptyPayload is written when a verb has no conjugation match in the corpus,
// so future boots do not retry the same lookups indefinitely. The template
// gate (Conjugations != nil) prevents this from rendering an empty panel.
const emptyPayload = "{}"

// Backfill populates words.conjugations for every verb that does not yet
// have it. It joins by lemma against the verb_conjugations corpus loaded
// by EnsureCorpus. Words with no corpus match get an empty JSON object so
// they are not re-attempted on later boots. Returns (matched, missed).
//
// If the corpus is empty (no seed file shipped yet), backfill is a no-op so
// that the next boot — once the corpus is populated — still gets a chance
// at every verb.
func Backfill(db *sql.DB) (int, int, error) {
	var corpus int
	if err := db.QueryRow(`SELECT COUNT(*) FROM verb_conjugations`).Scan(&corpus); err != nil {
		return 0, 0, fmt.Errorf("count verb_conjugations: %w", err)
	}
	if corpus == 0 {
		log.Printf("conjugations: backfill skipped (corpus empty)")
		return 0, 0, nil
	}

	rows, err := db.Query(`
		SELECT w.id, w.lemma, vc.payload
		FROM words w
		LEFT JOIN verb_conjugations vc ON vc.infinitive = w.lemma
		WHERE w.pos = 'Verb' AND w.conjugations IS NULL
	`)
	if err != nil {
		return 0, 0, fmt.Errorf("select verbs: %w", err)
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
		log.Printf("conjugations: backfill skipped (all verbs already filled)")
		return 0, 0, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`UPDATE words SET conjugations = ?, conjugations_at = ? WHERE id = ?`)
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
	log.Printf("conjugations: backfilled %d of %d verbs (%d had no match)", matched, len(todos), missed)
	return matched, missed, nil
}
