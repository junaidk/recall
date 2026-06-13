// Package seedmeta tracks the content hash of each loaded seed corpus in the
// meta table, so a changed seed file triggers a corpus reload (and full
// re-backfill) on the next boot. Without it, the corpus loaders are gated on
// the corpus table being empty and a rebuilt seed would be silently ignored.
package seedmeta

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"os"
)

// CorpusHashKey is the meta key under which a corpus file's hash is stored.
// Keyed by the bare seed filename so each corpus is tracked independently.
func CorpusHashKey(corpusFile string) string {
	return "corpus_hash:" + corpusFile
}

// FileHash returns the hex-encoded SHA-256 of the file at path. The bool is
// false (with a nil error) when the file does not exist, letting callers skip
// a missing seed without treating it as a failure.
func FileHash(path string) (hash string, exists bool, err error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", false, err
	}
	return hex.EncodeToString(h.Sum(nil)), true, nil
}

// Get returns the stored value for key, or "" if the key is absent.
func Get(db *sql.DB, key string) (string, error) {
	var v string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// execer is satisfied by both *sql.DB and *sql.Tx, so Set can run inside the
// same transaction that loads the corpus (keeping hash and data atomic).
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// Set upserts key=value.
func Set(e execer, key, value string) error {
	_, err := e.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
