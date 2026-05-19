package importer

import (
	"database/sql"
	"log"
	"os"
	"path/filepath"
	"strings"
)

// ScanAndImport walks dir for *.json files and imports each as a deck
// whose name is the file stem (e.g. "A2.json" -> "A2").
func ScanAndImport(db *sql.DB, dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		stem := strings.TrimSuffix(name, filepath.Ext(name))
		path := filepath.Join(dir, name)
		ins, skip, err := ImportFile(db, path, stem)
		if err != nil {
			return err
		}
		log.Printf("import %s: %d new words, %d already present", stem, ins, skip)
	}
	return nil
}
