// Package audio scrapes DWDS pronunciation MP3 URLs and stores them on words.
package audio

import (
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"regexp"
	"time"
)

const (
	requestTimeout = 10 * time.Second
	politeDelay    = 150 * time.Millisecond
)

// mp3URLRe matches the JSON-LD contentUrl field that DWDS embeds in its
// dictionary pages, e.g. "contentUrl": "https://www.dwds.de/audio/111/das_Schloss.mp3".
var mp3URLRe = regexp.MustCompile(`"contentUrl"\s*:\s*"(https?://[^"]+\.mp3)"`)

// Backfill fetches the DWDS page for every word with NULL audio_url (and a
// non-NULL url), extracts the pronunciation mp3 URL, and stores it.
// Returns (matched, missed).
func Backfill(db *sql.DB) (int, int, error) {
	rows, err := db.Query(`SELECT id, url FROM words WHERE audio_url IS NULL AND url IS NOT NULL AND url != ''`)
	if err != nil {
		return 0, 0, fmt.Errorf("select words: %w", err)
	}
	type todo struct {
		id  int64
		url string
	}
	var todos []todo
	for rows.Next() {
		var t todo
		if err := rows.Scan(&t.id, &t.url); err != nil {
			rows.Close()
			return 0, 0, err
		}
		todos = append(todos, t)
	}
	rows.Close()
	if len(todos) == 0 {
		log.Printf("audio: backfill skipped (no words need fetching)")
		return 0, 0, nil
	}

	client := &http.Client{Timeout: requestTimeout}
	log.Printf("audio: starting backfill of %d words", len(todos))

	matched, missed := 0, 0
	for i, t := range todos {
		if i > 0 {
			time.Sleep(politeDelay)
		}
		mp3, err := fetchMP3URL(client, t.url)
		if err != nil {
			log.Printf("audio: fetch %s: %v", t.url, err)
			missed++
			continue
		}
		// Empty mp3 = page loaded but no audio. Store "" so we don't retry next
		// boot. Network errors leave audio_url NULL so they remain retried.
		if _, err := db.Exec(`UPDATE words SET audio_url = ? WHERE id = ?`, mp3, t.id); err != nil {
			return matched, missed, fmt.Errorf("update word %d: %w", t.id, err)
		}
		if mp3 == "" {
			missed++
		} else {
			matched++
		}
		if (i+1)%100 == 0 {
			log.Printf("audio: progress %d/%d (matched %d, missed %d)", i+1, len(todos), matched, missed)
		}
	}

	log.Printf("audio: backfilled %d of %d words (%d had no mp3)", matched, len(todos), missed)
	return matched, missed, nil
}

func fetchMP3URL(client *http.Client, pageURL string) (string, error) {
	req, err := http.NewRequest("GET", pageURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "recall/audio-backfill (+https://github.com/junaidk/recall)")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	m := mp3URLRe.FindSubmatch(body)
	if m == nil {
		return "", nil
	}
	return string(m[1]), nil
}
