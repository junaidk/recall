package sentences

import (
	"archive/zip"
	"bufio"
	"bytes"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

// Tatoeba/ManyThings German-English sentence pairs. Each row is:
//   english TAB german TAB attribution
const tatoebaURL = "https://www.manythings.org/anki/deu-eng.zip"

// EnsureCorpus downloads and indexes the Tatoeba sentence corpus if the
// sentence_pairs table is empty. Idempotent.
func EnsureCorpus(db *sql.DB) error {
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sentence_pairs`).Scan(&n); err != nil {
		return fmt.Errorf("count pairs: %w", err)
	}
	if n > 0 {
		log.Printf("sentences: corpus already loaded (%d pairs)", n)
		return nil
	}

	log.Printf("sentences: downloading tatoeba deu-eng (~10 MB)…")
	pairs, err := downloadAndParse()
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	log.Printf("sentences: parsed %d pairs, inserting…", len(pairs))

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO sentence_pairs (de, en, de_len) VALUES (?, ?, ?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, p := range pairs {
		if _, err := stmt.Exec(p.de, p.en, utf8.RuneCountInString(p.de)); err != nil {
			return fmt.Errorf("insert pair: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	log.Printf("sentences: indexed %d pairs", len(pairs))
	return nil
}

type pair struct {
	de string
	en string
}

func downloadAndParse() ([]pair, error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequest("GET", tatoebaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "recall/1.0 (vocab trainer)")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	buf, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	zr, err := zip.NewReader(bytes.NewReader(buf), int64(len(buf)))
	if err != nil {
		return nil, fmt.Errorf("unzip: %w", err)
	}

	var f *zip.File
	for _, zf := range zr.File {
		if strings.HasSuffix(zf.Name, ".txt") && !strings.HasPrefix(zf.Name, "_") {
			f = zf
			break
		}
	}
	if f == nil {
		return nil, fmt.Errorf("no .txt in archive")
	}

	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()

	var pairs []pair
	scanner := bufio.NewScanner(rc)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		parts := strings.SplitN(line, "\t", 3)
		if len(parts) < 2 {
			continue
		}
		en := strings.TrimSpace(parts[0])
		de := strings.TrimSpace(parts[1])
		if en == "" || de == "" {
			continue
		}
		pairs = append(pairs, pair{de: de, en: en})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan: %w", err)
	}
	return pairs, nil
}
