package translator

import (
	"database/sql"
	"log"
	"time"
)

const batchSize = 50

// TranslateAllPending fetches translations for every word with NULL
// translation_en, in batches of up to 50. Returns the number translated.
func TranslateAllPending(db *sql.DB, c *Client) (int, error) {
	if c == nil || c.APIKey == "" {
		log.Printf("translate: no DeepL key configured; skipping")
		return 0, nil
	}
	total := 0
	for {
		rows, err := db.Query(
			`SELECT id, lemma FROM words WHERE translation_en IS NULL ORDER BY id LIMIT ?`,
			batchSize,
		)
		if err != nil {
			return total, err
		}
		var (
			ids    []int64
			lemmas []string
		)
		for rows.Next() {
			var id int64
			var lemma string
			if err := rows.Scan(&id, &lemma); err != nil {
				rows.Close()
				return total, err
			}
			ids = append(ids, id)
			lemmas = append(lemmas, lemma)
		}
		rows.Close()
		if len(ids) == 0 {
			break
		}

		translated, err := c.Translate(lemmas)
		if err != nil {
			log.Printf("translate batch (%d words) failed: %v", len(lemmas), err)
			return total, err
		}

		tx, err := db.Begin()
		if err != nil {
			return total, err
		}
		stmt, err := tx.Prepare(`UPDATE words SET translation_en = ?, translated_at = ? WHERE id = ?`)
		if err != nil {
			tx.Rollback()
			return total, err
		}
		now := time.Now()
		for i, id := range ids {
			if _, err := stmt.Exec(translated[i], now, id); err != nil {
				stmt.Close()
				tx.Rollback()
				return total, err
			}
		}
		stmt.Close()
		if err := tx.Commit(); err != nil {
			return total, err
		}
		total += len(ids)
		log.Printf("translate: %d words (running total %d)", len(ids), total)
	}
	return total, nil
}
