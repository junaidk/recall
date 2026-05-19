package importer

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
)

// Raw JSON entry shape (see api-doc.md).
type rawEntry struct {
	Sch      []rawSch `json:"sch"`
	URL      string   `json:"url"`
	Pos      string   `json:"pos"`
	Articles []string `json:"articles"`
	Genera   []string `json:"genera"`
	OnlyPl   string   `json:"onlypl"`
}

type rawSch struct {
	Lemma string  `json:"lemma"`
	Hidx  *string `json:"hidx"`
}

// ImportFile reads a JSON word list and upserts deck + words. Returns
// (inserted, skipped). Idempotent via UNIQUE(deck_id, lemma, hidx).
func ImportFile(db *sql.DB, path, deckName string) (inserted int, skipped int, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, 0, fmt.Errorf("read %s: %w", path, err)
	}
	var entries []rawEntry
	if err := json.Unmarshal(data, &entries); err != nil {
		return 0, 0, fmt.Errorf("parse %s: %w", path, err)
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(
		`INSERT OR IGNORE INTO decks (name, source_path) VALUES (?, ?)`,
		deckName, path,
	); err != nil {
		return 0, 0, fmt.Errorf("upsert deck: %w", err)
	}
	var deckID int64
	if err := tx.QueryRow(`SELECT id FROM decks WHERE name = ?`, deckName).Scan(&deckID); err != nil {
		return 0, 0, fmt.Errorf("lookup deck: %w", err)
	}

	stmt, err := tx.Prepare(
		`INSERT OR IGNORE INTO words (deck_id, lemma, hidx, pos, articles, genera, url, only_plural)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return 0, 0, err
	}
	defer stmt.Close()

	for _, e := range entries {
		if len(e.Sch) == 0 {
			continue
		}
		lemma := e.Sch[0].Lemma
		var hidx sql.NullInt64
		if e.Sch[0].Hidx != nil {
			if n, err := strconv.ParseInt(*e.Sch[0].Hidx, 10, 64); err == nil {
				hidx = sql.NullInt64{Int64: n, Valid: true}
			}
		}
		articlesJSON, _ := json.Marshal(e.Articles)
		generaJSON, _ := json.Marshal(e.Genera)
		onlyPlural := 0
		if e.OnlyPl != "" {
			onlyPlural = 1
		}
		res, err := stmt.Exec(deckID, lemma, hidx, e.Pos, string(articlesJSON), string(generaJSON), e.URL, onlyPlural)
		if err != nil {
			return 0, 0, fmt.Errorf("insert word %q: %w", lemma, err)
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			inserted++
		} else {
			skipped++
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return inserted, skipped, nil
}
