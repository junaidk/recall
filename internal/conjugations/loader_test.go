package conjugations

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/mattn/go-sqlite3"
)

const (
	liebenV1 = `{"infinitive":"lieben","praesens":{"ich":"liebe","du":"liebst","er":"liebt","wir":"lieben","ihr":"liebt","sie":"lieben"},"perfekt":{"aux":"haben","partizip2":"geliebt"}}` + "\n"
	// Same verb rebuilt with a Präteritum table — the realistic "seed changed" case.
	liebenV2 = `{"infinitive":"lieben","praesens":{"ich":"liebe","du":"liebst","er":"liebt","wir":"lieben","ihr":"liebt","sie":"lieben"},"praeteritum":{"ich":"liebte","du":"liebtest","er":"liebte","wir":"liebten","ihr":"liebtet","sie":"liebten"},"perfekt":{"aux":"haben","partizip2":"geliebt"}}` + "\n"
)

func testDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`
		CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE verb_conjugations (infinitive TEXT PRIMARY KEY, payload TEXT NOT NULL);
		CREATE TABLE words (id INTEGER PRIMARY KEY, lemma TEXT, pos TEXT, conjugations TEXT, conjugations_at DATETIME);
	`); err != nil {
		t.Fatal(err)
	}
	return db
}

func writeSeed(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, corpusFile), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureCorpusReloadsOnSeedChange(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	writeSeed(t, dir, liebenV1)

	if changed, err := EnsureCorpus(db, dir); err != nil || !changed {
		t.Fatalf("first load: changed=%v err=%v, want changed=true", changed, err)
	}
	// Unchanged file → no reload.
	if changed, err := EnsureCorpus(db, dir); err != nil || changed {
		t.Fatalf("unchanged seed: changed=%v err=%v, want changed=false", changed, err)
	}
	// Rebuilt seed → reload, and the new payload must be visible.
	writeSeed(t, dir, liebenV2)
	if changed, err := EnsureCorpus(db, dir); err != nil || !changed {
		t.Fatalf("changed seed: changed=%v err=%v, want changed=true", changed, err)
	}
	var payload string
	if err := db.QueryRow(`SELECT payload FROM verb_conjugations WHERE infinitive='lieben'`).Scan(&payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload, "praeteritum") {
		t.Errorf("reloaded payload missing praeteritum: %s", payload)
	}
}

func TestEnsureCorpusMissingSeed(t *testing.T) {
	db := testDB(t)
	changed, err := EnsureCorpus(db, t.TempDir()) // empty dir, no seed file
	if err != nil {
		t.Fatalf("missing seed should not error: %v", err)
	}
	if changed {
		t.Error("missing seed: changed=true, want false")
	}
}

// A changed corpus must re-derive words that were already backfilled (stale
// payload) — the propagation the seed-hash check exists to guarantee.
func TestBackfillForceRederivesStaleWords(t *testing.T) {
	db := testDB(t)
	dir := t.TempDir()
	if _, err := db.Exec(`INSERT INTO words (id, lemma, pos) VALUES (1, 'lieben', 'Verb')`); err != nil {
		t.Fatal(err)
	}

	// Boot 1: load V1 (no Präteritum) and backfill the word.
	writeSeed(t, dir, liebenV1)
	changed, err := EnsureCorpus(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := Backfill(db, changed); err != nil {
		t.Fatal(err)
	}
	var got string
	db.QueryRow(`SELECT conjugations FROM words WHERE id=1`).Scan(&got)
	if strings.Contains(got, "praeteritum") {
		t.Fatalf("V1 payload unexpectedly has praeteritum: %s", got)
	}

	// Boot 2: rebuilt seed with Präteritum; changed=true forces re-derive even
	// though words.conjugations is already non-NULL.
	writeSeed(t, dir, liebenV2)
	changed, err = EnsureCorpus(db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected changed=true after seed rebuild")
	}
	matched, _, err := Backfill(db, changed)
	if err != nil {
		t.Fatal(err)
	}
	if matched != 1 {
		t.Errorf("re-backfill matched=%d, want 1", matched)
	}
	db.QueryRow(`SELECT conjugations FROM words WHERE id=1`).Scan(&got)
	if !strings.Contains(got, "praeteritum") {
		t.Errorf("force re-backfill did not propagate praeteritum: %s", got)
	}
}
