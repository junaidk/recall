package audio

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"golang.org/x/sync/singleflight"
)

const downloadTimeout = 30 * time.Second

// Cache stores pronunciation MP3s on disk keyed by word ID. Files are written
// atomically (temp + rename) so partially-downloaded files are never served.
// Concurrent downloads of the same word ID are deduplicated via singleflight.
type Cache struct {
	dir string
	sf  singleflight.Group
	hc  *http.Client
}

func NewCache(dir string) (*Cache, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("audio cache: mkdir %s: %w", dir, err)
	}
	return &Cache{
		dir: dir,
		hc:  &http.Client{Timeout: downloadTimeout},
	}, nil
}

func (c *Cache) Path(wordID int64) string {
	return filepath.Join(c.dir, strconv.FormatInt(wordID, 10)+".mp3")
}

func (c *Cache) Has(wordID int64) bool {
	info, err := os.Stat(c.Path(wordID))
	return err == nil && !info.IsDir() && info.Size() > 0
}

// FetchAsync kicks off a background download. Returns immediately. Concurrent
// calls for the same wordID coalesce into a single HTTP fetch.
func (c *Cache) FetchAsync(wordID int64, url string) {
	go func() {
		key := strconv.FormatInt(wordID, 10)
		_, _, _ = c.sf.Do(key, func() (any, error) {
			if c.Has(wordID) {
				return nil, nil
			}
			if err := c.fetch(wordID, url); err != nil {
				log.Printf("audio: cache fetch word=%d: %v", wordID, err)
				return nil, err
			}
			return nil, nil
		})
	}()
}

func (c *Cache) fetch(wordID int64, url string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "recall/audio-cache (+https://github.com/junaidk/recall)")
	resp, err := c.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}

	final := c.Path(wordID)
	tmp := final + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
