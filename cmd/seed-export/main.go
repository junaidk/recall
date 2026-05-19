// seed-export dumps each deck's harvested word data (translation, audio_url,
// example sentences) into <deck>.enrichment.json files alongside the raw word
// lists. Run after a fully-populated local backfill, then commit the result.
package main

import (
	"database/sql"
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"
	"sort"

	"github.com/junaidk/recall/internal/config"
	"github.com/junaidk/recall/internal/db"
	"github.com/junaidk/recall/internal/seed"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	outDir := flag.String("out", "", "where to write <deck>.enrichment.json (default: import.seed_dir from config)")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	dir := *outDir
	if dir == "" {
		dir = cfg.Import.SeedDir
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", dir, err)
	}

	dbConn, err := db.Open(cfg.DB.Path)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer dbConn.Close()

	decks, err := listDecks(dbConn)
	if err != nil {
		log.Fatalf("list decks: %v", err)
	}
	if len(decks) == 0 {
		log.Printf("no decks in db; nothing to export")
		return
	}

	for _, deckName := range decks {
		entries, err := exportDeck(dbConn, deckName)
		if err != nil {
			log.Fatalf("export %s: %v", deckName, err)
		}
		path := filepath.Join(dir, deckName+".enrichment.json")
		if err := writeJSON(path, entries); err != nil {
			log.Fatalf("write %s: %v", path, err)
		}
		log.Printf("seed-export: wrote %s (%d entries)", path, len(entries))
	}
}

func listDecks(dbConn *sql.DB) ([]string, error) {
	rows, err := dbConn.Query(`SELECT name FROM decks ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func exportDeck(dbConn *sql.DB, deckName string) ([]seed.Entry, error) {
	rows, err := dbConn.Query(`
		SELECT w.url, w.translation_en, w.audio_url, w.example_de, w.example_en, w.example_source
		FROM words w
		JOIN decks d ON d.id = w.deck_id
		WHERE d.name = ?
		  AND w.url IS NOT NULL AND w.url != ''
		  AND (
		    w.translation_en IS NOT NULL
		    OR w.audio_url IS NOT NULL
		    OR w.example_de IS NOT NULL
		    OR w.example_en IS NOT NULL
		    OR w.example_source IS NOT NULL
		  )
	`, deckName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var entries []seed.Entry
	for rows.Next() {
		var (
			url       string
			trans     sql.NullString
			audio     sql.NullString
			exDe      sql.NullString
			exEn      sql.NullString
			exSource  sql.NullString
		)
		if err := rows.Scan(&url, &trans, &audio, &exDe, &exEn, &exSource); err != nil {
			return nil, err
		}
		e := seed.Entry{URL: url}
		if trans.Valid {
			e.TranslationEn = ptr(trans.String)
		}
		if audio.Valid {
			e.AudioURL = ptr(audio.String)
		}
		if exDe.Valid {
			e.ExampleDe = ptr(exDe.String)
		}
		if exEn.Valid {
			e.ExampleEn = ptr(exEn.String)
		}
		if exSource.Valid {
			e.ExampleSource = ptr(exSource.String)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].URL < entries[j].URL })
	return entries, nil
}

func writeJSON(path string, entries []seed.Entry) error {
	out, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	out = append(out, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ptr(s string) *string { return &s }
