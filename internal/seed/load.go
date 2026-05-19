// Package seed loads checked-in enrichment data (translations, audio URLs,
// example sentences) into words rows whose corresponding columns are NULL,
// so a fresh clone can reproduce a fully-populated reference dataset
// without re-running the expensive DeepL / DWDS / Tatoeba backfills.
package seed

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
)

const enrichmentSuffix = ".enrichment.json"

// Entry is one row's worth of harvested data, keyed by DWDS url (unique per
// dictionary entry). Fields are pointers so we can distinguish "absent" from
// "empty string" — an empty audio_url is a sentinel meaning "DWDS has no mp3".
type Entry struct {
	URL           string  `json:"url"`
	TranslationEn *string `json:"translation_en,omitempty"`
	AudioURL      *string `json:"audio_url,omitempty"`
	ExampleDe     *string `json:"example_de,omitempty"`
	ExampleEn     *string `json:"example_en,omitempty"`
	ExampleSource *string `json:"example_source,omitempty"`
}

// LoadEnrichment scans dir for *.enrichment.json files and fills NULL columns
// on matching words rows. Returns the total number of word rows actually
// updated. Existing non-NULL values are never overwritten (COALESCE in the
// UPDATE), and rows with no NULL columns to fill are skipped entirely.
func LoadEnrichment(db *sql.DB, dir string) (int, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*"+enrichmentSuffix))
	if err != nil {
		return 0, fmt.Errorf("glob %s: %w", dir, err)
	}
	if len(matches) == 0 {
		return 0, nil
	}

	pending, err := pendingURLs(db)
	if err != nil {
		return 0, fmt.Errorf("scan pending: %w", err)
	}
	if len(pending) == 0 {
		return 0, nil
	}

	// The WHERE clause also gates on "at least one column will actually
	// change", so RowsAffected reflects real work — otherwise a row with
	// a NULL column not present in the JSON would still match.
	stmt, err := db.Prepare(`
		UPDATE words SET
			translation_en = COALESCE(translation_en, ?),
			audio_url      = COALESCE(audio_url,      ?),
			example_de     = COALESCE(example_de,     ?),
			example_en     = COALESCE(example_en,     ?),
			example_source = COALESCE(example_source, ?)
		WHERE url = ?
		  AND (
		       (translation_en IS NULL AND ? IS NOT NULL)
		    OR (audio_url      IS NULL AND ? IS NOT NULL)
		    OR (example_de     IS NULL AND ? IS NOT NULL)
		    OR (example_en     IS NULL AND ? IS NOT NULL)
		    OR (example_source IS NULL AND ? IS NOT NULL)
		  )
	`)
	if err != nil {
		return 0, fmt.Errorf("prepare update: %w", err)
	}
	defer stmt.Close()

	total := 0
	for _, path := range matches {
		applied, err := loadFile(stmt, path, pending)
		if err != nil {
			return total, err
		}
		total += applied
	}
	return total, nil
}

// pendingURLs returns the set of word URLs that have at least one NULL
// enrichment column. Lets the loader skip rows that are already fully filled
// in, so logs reflect actual work done.
func pendingURLs(db *sql.DB) (map[string]struct{}, error) {
	rows, err := db.Query(`
		SELECT url FROM words
		WHERE url IS NOT NULL AND url != ''
		  AND (
		    translation_en IS NULL
		    OR audio_url IS NULL
		    OR example_de IS NULL
		    OR example_en IS NULL
		    OR example_source IS NULL
		  )
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]struct{})
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out[u] = struct{}{}
	}
	return out, rows.Err()
}

func loadFile(stmt *sql.Stmt, path string, pending map[string]struct{}) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	var entries []Entry
	if err := json.Unmarshal(data, &entries); err != nil {
		return 0, fmt.Errorf("parse %s: %w", path, err)
	}

	applied := 0
	for _, e := range entries {
		if e.URL == "" {
			continue
		}
		if _, needs := pending[e.URL]; !needs {
			continue
		}
		trans := nullString(e.TranslationEn)
		audio := nullString(e.AudioURL)
		exDe := nullString(e.ExampleDe)
		exEn := nullString(e.ExampleEn)
		exSrc := nullString(e.ExampleSource)
		res, err := stmt.Exec(
			trans, audio, exDe, exEn, exSrc, // SET COALESCE values
			e.URL,                            // WHERE url = ?
			trans, audio, exDe, exEn, exSrc, // WHERE … IS NOT NULL checks
		)
		if err != nil {
			return applied, fmt.Errorf("apply %s entry %q: %w", path, e.URL, err)
		}
		n, _ := res.RowsAffected()
		if n > 0 {
			applied++
		}
	}
	if applied > 0 {
		log.Printf("seed: applied enrichment from %s (%d of %d entries filled in NULL columns)", filepath.Base(path), applied, len(entries))
	}
	return applied, nil
}

// IsEnrichmentFile reports whether name is a seed enrichment sidecar,
// so callers (e.g. the deck importer) can skip it.
func IsEnrichmentFile(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), enrichmentSuffix)
}

func nullString(p *string) any {
	if p == nil {
		return nil
	}
	return *p
}
