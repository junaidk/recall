// anki-export builds an .apkg (Anki package) per deck, bundling the lemma,
// translation, example sentences, conjugations / plurals, and DWDS pronunciation
// MP3s. Cards are written in the "new" queue (no scheduling history) so Anki
// starts the user fresh.
//
//   go build -tags sqlite_fts5 -o anki-export ./cmd/anki-export
//   ./anki-export                       # all decks → exports/<deck>.apkg
//   ./anki-export -deck A1              # just one
//   ./anki-export -no-audio             # skip MP3 bundling
package main

import (
	"archive/zip"
	"crypto/sha1"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/junaidk/recall/internal/config"
	"github.com/junaidk/recall/internal/db"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	outDir := flag.String("out", "exports", "directory to write <deck>.apkg files")
	deckFilter := flag.String("deck", "", "only export this deck (default: all decks)")
	noAudio := flag.Bool("no-audio", false, "skip downloading and bundling MP3s")
	audioWorkers := flag.Int("audio-workers", 8, "parallel audio downloads")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("mkdir %s: %v", *outDir, err)
	}

	conn, err := db.Open(cfg.DB.Path)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer conn.Close()

	decks, err := listDecks(conn, *deckFilter)
	if err != nil {
		log.Fatalf("list decks: %v", err)
	}
	if len(decks) == 0 {
		log.Fatalf("no decks matched (filter=%q)", *deckFilter)
	}

	for _, d := range decks {
		log.Printf("exporting deck %q (id=%d)…", d.name, d.id)
		words, err := loadWords(conn, d.id)
		if err != nil {
			log.Fatalf("load words for %s: %v", d.name, err)
		}
		log.Printf("  %d words", len(words))

		if !*noAudio {
			fetchAudio(words, cfg.Audio.CacheDir, *audioWorkers)
		}

		outPath := filepath.Join(*outDir, d.name+".apkg")
		if err := writeApkg(outPath, d.name, words, cfg.Audio.CacheDir, *noAudio); err != nil {
			log.Fatalf("write %s: %v", outPath, err)
		}
		log.Printf("  wrote %s", outPath)
	}
}

// ---------------------------------------------------------------------------
// data loading
// ---------------------------------------------------------------------------

type deckRow struct {
	id   int64
	name string
}

func listDecks(conn *sql.DB, filter string) ([]deckRow, error) {
	q := `SELECT id, name FROM decks`
	args := []any{}
	if filter != "" {
		q += ` WHERE name = ?`
		args = append(args, filter)
	}
	q += ` ORDER BY name`
	rows, err := conn.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []deckRow
	for rows.Next() {
		var d deckRow
		if err := rows.Scan(&d.id, &d.name); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

type word struct {
	id           int64
	lemma        string
	pos          string
	articles     string // JSON array
	genera       string // JSON array
	url          string
	translation  string
	exampleDe    string
	exampleEn    string
	audioURL     string
	conjugations string // JSON
	plurals      string // JSON
}

func loadWords(conn *sql.DB, deckID int64) ([]word, error) {
	rows, err := conn.Query(`
		SELECT id, lemma, COALESCE(pos,''), COALESCE(articles,''), COALESCE(genera,''),
		       COALESCE(url,''), COALESCE(translation_en,''),
		       COALESCE(example_de,''), COALESCE(example_en,''),
		       COALESCE(audio_url,''), COALESCE(conjugations,''), COALESCE(plurals,'')
		FROM words WHERE deck_id = ? ORDER BY id`, deckID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []word
	for rows.Next() {
		var w word
		if err := rows.Scan(&w.id, &w.lemma, &w.pos, &w.articles, &w.genera, &w.url,
			&w.translation, &w.exampleDe, &w.exampleEn, &w.audioURL, &w.conjugations, &w.plurals); err != nil {
			return nil, err
		}
		out = append(out, w)
	}
	return out, rows.Err()
}

// ---------------------------------------------------------------------------
// audio fetch
// ---------------------------------------------------------------------------

func audioCachePath(cacheDir string, wordID int64) string {
	return filepath.Join(cacheDir, strconv.FormatInt(wordID, 10)+".mp3")
}

// fetchAudio downloads MP3s for any words that have an audio_url but no local
// cache file yet. Reuses data/audio_cache so reruns are cheap.
func fetchAudio(words []word, cacheDir string, workers int) {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		log.Fatalf("mkdir cache: %v", err)
	}
	type job struct {
		id  int64
		url string
	}
	jobs := make(chan job)
	var wg sync.WaitGroup
	var (
		mu              sync.Mutex
		downloaded, fail int
	)
	client := &http.Client{Timeout: 30 * time.Second}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if err := downloadMP3(client, j.url, audioCachePath(cacheDir, j.id)); err != nil {
					mu.Lock()
					fail++
					mu.Unlock()
					log.Printf("  audio fetch word=%d failed: %v", j.id, err)
					continue
				}
				mu.Lock()
				downloaded++
				mu.Unlock()
			}
		}()
	}

	queued := 0
	for _, w := range words {
		if w.audioURL == "" {
			continue
		}
		if _, err := os.Stat(audioCachePath(cacheDir, w.id)); err == nil {
			continue
		}
		jobs <- job{id: w.id, url: w.audioURL}
		queued++
	}
	close(jobs)
	wg.Wait()
	if queued > 0 {
		log.Printf("  audio: %d downloaded, %d failed (queued %d)", downloaded, fail, queued)
	}
}

func downloadMP3(client *http.Client, url, dest string) error {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "recall/anki-export (+https://github.com/junaidk/recall)")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	tmp := dest + ".tmp"
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
	return os.Rename(tmp, dest)
}

// ---------------------------------------------------------------------------
// card rendering (HTML for the back of the card)
// ---------------------------------------------------------------------------

// note field separator used by Anki (0x1f).
const fieldSep = "\x1f"

func displayLemma(lemma, articlesJSON string) string {
	if articlesJSON == "" {
		return lemma
	}
	var arts []string
	if err := json.Unmarshal([]byte(articlesJSON), &arts); err != nil || len(arts) == 0 {
		return lemma
	}
	return strings.Join(arts, "/") + " " + lemma
}

func frontHTML(w word) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<div class="lemma">%s</div>`, html.EscapeString(displayLemma(w.lemma, w.articles)))
	if w.pos != "" {
		fmt.Fprintf(&b, `<div class="pos">%s</div>`, html.EscapeString(w.pos))
	}
	return b.String()
}

func backHTML(w word, audioFile string) string {
	var b strings.Builder
	if w.translation != "" {
		fmt.Fprintf(&b, `<div class="translation">%s</div>`, html.EscapeString(w.translation))
	} else {
		b.WriteString(`<div class="translation"><em>(no translation)</em></div>`)
	}
	if audioFile != "" {
		fmt.Fprintf(&b, "[sound:%s]", audioFile)
	}
	if w.exampleDe != "" {
		fmt.Fprintf(&b, `<div class="example"><div class="example-de">%s</div>`, html.EscapeString(w.exampleDe))
		if w.exampleEn != "" {
			fmt.Fprintf(&b, `<div class="example-en">%s</div>`, html.EscapeString(w.exampleEn))
		}
		b.WriteString(`</div>`)
	}

	// Grammar tables (conjugations / plurals) render into their own builder so
	// we can prefix a divider only when there's actually something to separate.
	var g strings.Builder
	renderConjugations(&g, w.conjugations)
	renderPlurals(&g, w.lemma, w.articles, w.genera, w.plurals)
	if g.Len() > 0 {
		b.WriteString(`<hr class="grammar-sep">`)
		b.WriteString(`<div class="grammar">`)
		b.WriteString(g.String())
		b.WriteString(`</div>`)
	}
	return b.String()
}

func renderConjugations(b *strings.Builder, raw string) {
	if raw == "" || raw == "{}" {
		return
	}
	var p struct {
		Praesens    map[string]string `json:"praesens"`
		Praeteritum map[string]string `json:"praeteritum"`
		Perfekt     struct {
			Aux       string `json:"aux"`
			Partizip2 string `json:"partizip2"`
		} `json:"perfekt"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return
	}
	if len(p.Praesens) == 0 && len(p.Praeteritum) == 0 && p.Perfekt.Partizip2 == "" {
		return
	}

	if len(p.Praesens) > 0 {
		b.WriteString(`<div class="tense"><div class="tense-label">Pr&auml;sens</div><table class="conj-table">`)
		for _, r := range praesensOrder {
			form := p.Praesens[r.key]
			if form == "" {
				continue
			}
			fmt.Fprintf(b, `<tr><th>%s</th><td>%s</td></tr>`, html.EscapeString(r.label), html.EscapeString(form))
		}
		b.WriteString(`</table></div>`)
	}
	if len(p.Praeteritum) > 0 {
		b.WriteString(`<div class="tense"><div class="tense-label">Pr&auml;teritum</div><table class="conj-table">`)
		for _, r := range praesensOrder {
			form := p.Praeteritum[r.key]
			if form == "" {
				continue
			}
			fmt.Fprintf(b, `<tr><th>%s</th><td>%s</td></tr>`, html.EscapeString(r.label), html.EscapeString(form))
		}
		b.WriteString(`</table></div>`)
	}
	if aux, ok := auxPraesens[p.Perfekt.Aux]; ok && p.Perfekt.Partizip2 != "" {
		b.WriteString(`<div class="tense"><div class="tense-label">Perfekt</div><table class="conj-table">`)
		for _, r := range praesensOrder {
			form := aux[r.key] + " " + p.Perfekt.Partizip2
			fmt.Fprintf(b, `<tr><th>%s</th><td>%s</td></tr>`, html.EscapeString(r.label), html.EscapeString(form))
		}
		b.WriteString(`</table></div>`)
	}
}

func renderPlurals(b *strings.Builder, lemma, articlesJSON, generaJSON, raw string) {
	if raw == "" || raw == "{}" {
		return
	}
	var p struct {
		Sg struct {
			Nom, Akk, Dat, Gen string
		} `json:"sg"`
		Pl struct {
			Nom, Akk, Dat, Gen string
		} `json:"pl"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return
	}
	if p.Pl.Nom == "" && p.Pl.Akk == "" && p.Pl.Dat == "" && p.Pl.Gen == "" {
		return
	}

	genus := pickGenus(articlesJSON, generaJSON)
	sgArt := singularArticles[genus]

	sgForms := map[string]string{"nom": p.Sg.Nom, "akk": p.Sg.Akk, "dat": p.Sg.Dat, "gen": p.Sg.Gen}
	plForms := map[string]string{"nom": p.Pl.Nom, "akk": p.Pl.Akk, "dat": p.Pl.Dat, "gen": p.Pl.Gen}
	if plForms["akk"] == "" {
		plForms["akk"] = plForms["nom"]
	}
	if plForms["gen"] == "" {
		plForms["gen"] = plForms["nom"]
	}
	if plForms["dat"] == "" {
		plForms["dat"] = dativPlural(plForms["nom"])
	}

	b.WriteString(`<div class="tense"><div class="tense-label">Singular</div><table class="conj-table">`)
	for _, r := range caseOrder {
		sgForm := sgForms[r.key]
		if sgForm == "" {
			sgForm = lemma
		}
		art := ""
		if sgArt != nil {
			art = sgArt[r.key]
		}
		cell := sgForm
		if art != "" {
			cell = art + " " + sgForm
		}
		fmt.Fprintf(b, `<tr><th>%s</th><td>%s</td></tr>`, html.EscapeString(r.label), html.EscapeString(cell))
	}
	b.WriteString(`</table></div>`)

	b.WriteString(`<div class="tense"><div class="tense-label">Plural</div><table class="conj-table">`)
	for _, r := range caseOrder {
		fmt.Fprintf(b, `<tr><th>%s</th><td>%s %s</td></tr>`, html.EscapeString(r.label),
			html.EscapeString(pluralArticles[r.key]), html.EscapeString(plForms[r.key]))
	}
	b.WriteString(`</table></div>`)
}

// helpers below mirror internal/handlers/review.go

var praesensOrder = []struct{ key, label string }{
	{"ich", "ich"},
	{"du", "du"},
	{"er", "er/sie/es"},
	{"wir", "wir"},
	{"ihr", "ihr"},
	{"sie", "sie"},
}

var auxPraesens = map[string]map[string]string{
	"haben": {"ich": "habe", "du": "hast", "er": "hat", "wir": "haben", "ihr": "habt", "sie": "haben"},
	"sein":  {"ich": "bin", "du": "bist", "er": "ist", "wir": "sind", "ihr": "seid", "sie": "sind"},
}

var caseOrder = []struct{ key, label string }{
	{"nom", "Nominativ"},
	{"akk", "Akkusativ"},
	{"dat", "Dativ"},
	{"gen", "Genitiv"},
}

var singularArticles = map[string]map[string]string{
	"mask.":  {"nom": "der", "akk": "den", "dat": "dem", "gen": "des"},
	"fem.":   {"nom": "die", "akk": "die", "dat": "der", "gen": "der"},
	"neutr.": {"nom": "das", "akk": "das", "dat": "dem", "gen": "des"},
}

var pluralArticles = map[string]string{"nom": "die", "akk": "die", "dat": "den", "gen": "der"}

var articleByNominative = map[string]string{"der": "mask.", "die": "fem.", "das": "neutr."}

func pickGenus(articlesJSON, generaJSON string) string {
	var genera []string
	if generaJSON != "" {
		_ = json.Unmarshal([]byte(generaJSON), &genera)
	}
	for _, g := range genera {
		if _, ok := singularArticles[g]; ok {
			return g
		}
	}
	var articles []string
	if articlesJSON != "" {
		_ = json.Unmarshal([]byte(articlesJSON), &articles)
	}
	for _, a := range articles {
		if g, ok := articleByNominative[a]; ok {
			return g
		}
	}
	return ""
}

func dativPlural(nomPl string) string {
	if nomPl == "" {
		return ""
	}
	if strings.HasSuffix(nomPl, "n") || strings.HasSuffix(nomPl, "s") {
		return nomPl
	}
	return nomPl + "n"
}

// ---------------------------------------------------------------------------
// .apkg assembly
// ---------------------------------------------------------------------------

// Anki collection.anki2 schema. This is the v11 ("legacy") format which all
// modern Anki desktop / AnkiWeb / AnkiMobile versions still import cleanly.
const ankiSchema = `
CREATE TABLE col (
    id              integer primary key,
    crt             integer not null,
    mod             integer not null,
    scm             integer not null,
    ver             integer not null,
    dty             integer not null,
    usn             integer not null,
    ls              integer not null,
    conf            text not null,
    models          text not null,
    decks           text not null,
    dconf           text not null,
    tags            text not null
);
CREATE TABLE notes (
    id              integer primary key,
    guid            text not null,
    mid             integer not null,
    mod             integer not null,
    usn             integer not null,
    tags            text not null,
    flds            text not null,
    sfld            integer not null,
    csum            integer not null,
    flags           integer not null,
    data            text not null
);
CREATE TABLE cards (
    id              integer primary key,
    nid             integer not null,
    did             integer not null,
    ord             integer not null,
    mod             integer not null,
    usn             integer not null,
    type            integer not null,
    queue           integer not null,
    due             integer not null,
    ivl             integer not null,
    factor          integer not null,
    reps            integer not null,
    lapses          integer not null,
    "left"          integer not null,
    odue            integer not null,
    odid            integer not null,
    flags           integer not null,
    data            text not null
);
CREATE TABLE revlog (
    id              integer primary key,
    cid             integer not null,
    usn             integer not null,
    ease            integer not null,
    ivl             integer not null,
    lastIvl         integer not null,
    factor          integer not null,
    time            integer not null,
    type            integer not null
);
CREATE TABLE graves (
    usn             integer not null,
    oid             integer not null,
    type            integer not null
);
CREATE INDEX ix_notes_usn on notes (usn);
CREATE INDEX ix_cards_usn on cards (usn);
CREATE INDEX ix_revlog_usn on revlog (usn);
CREATE INDEX ix_cards_nid on cards (nid);
CREATE INDEX ix_cards_sched on cards (did, queue, due);
CREATE INDEX ix_revlog_cid on revlog (cid);
CREATE INDEX ix_notes_csum on notes (csum);
`

const cardCSS = `.card {
  font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Arial, sans-serif;
  text-align: center;
  --fg: #222;
  --bg: #fff;
  --muted: #888;
  --muted-strong: #555;
  --translation: #2c3e50;
  --example-de: #333;
  --example-en: #777;
  --th: #999;
  --rule: #e3e3e3;
  color: var(--fg);
  background: var(--bg);
}
.card.nightMode {
  --fg: #e8e8e8;
  --bg: #2b2b2b;
  --muted: #aaa;
  --muted-strong: #c0c0c0;
  --translation: #8ab4f8;
  --example-de: #e0e0e0;
  --example-en: #b0b0b0;
  --th: #999;
  --rule: #555;
}
.lemma { font-size: 36px; font-weight: 600; margin: 12px 0 4px; }
.pos { color: var(--muted); font-size: 13px; text-transform: uppercase; letter-spacing: 1px; margin-bottom: 12px; }
.translation { font-size: 22px; margin: 16px 0; color: var(--translation); }
.example { margin: 18px auto; max-width: 520px; }
.example-de { font-style: italic; color: var(--example-de); }
.example-en { color: var(--example-en); margin-top: 4px; font-size: 15px; }
.tense { display: inline-block; vertical-align: top; margin: 12px 18px; text-align: left; }
.tense-label { font-weight: 600; color: var(--muted-strong); margin-bottom: 6px; }
.conj-table { border-collapse: collapse; margin: 0 auto; }
.conj-table th { text-align: right; padding: 2px 8px; font-weight: normal; color: var(--th); }
.conj-table td { text-align: left; padding: 2px 8px; }
hr.grammar-sep { width: 60%; margin: 22px auto 4px; border: 0; border-top: 1px solid var(--rule); }
.grammar { margin-top: 4px; }
hr#answer { margin: 16px 0; border: 0; border-top: 1px solid var(--rule); }
`

func writeApkg(outPath, deckName string, words []word, audioCacheDir string, noAudio bool) error {
	tmpDir, err := os.MkdirTemp("", "anki-export-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "collection.anki2")
	conn, err := sql.Open("sqlite3", "file:"+dbPath+"?_journal_mode=DELETE")
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.Exec(ankiSchema); err != nil {
		return fmt.Errorf("create schema: %w", err)
	}

	now := time.Now()
	nowMs := now.UnixMilli()
	deckID := time.Now().UnixNano() / 1000 // microseconds — unique within this file
	modelID := deckID + 1

	// Audio: keep media manifest map[mediaIndex]filename, write file bytes by
	// numeric index in the zip later.
	type mediaEntry struct {
		index    int
		srcPath  string
		filename string
	}
	var media []mediaEntry
	mediaByWord := make(map[int64]string) // wordID → filename in .apkg

	if !noAudio {
		for _, w := range words {
			src := audioCachePath(audioCacheDir, w.id)
			st, err := os.Stat(src)
			if err != nil || st.Size() == 0 {
				continue
			}
			fname := fmt.Sprintf("recall_%s_%d.mp3", strings.ToLower(deckName), w.id)
			media = append(media, mediaEntry{index: len(media), srcPath: src, filename: fname})
			mediaByWord[w.id] = fname
		}
	}

	// Insert notes + cards.
	tx, err := conn.Begin()
	if err != nil {
		return err
	}
	noteStmt, err := tx.Prepare(`INSERT INTO notes (id, guid, mid, mod, usn, tags, flds, sfld, csum, flags, data) VALUES (?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer noteStmt.Close()
	cardStmt, err := tx.Prepare(`INSERT INTO cards (id, nid, did, ord, mod, usn, type, queue, due, ivl, factor, reps, lapses, "left", odue, odid, flags, data) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return err
	}
	defer cardStmt.Close()

	noteID := nowMs
	cardID := nowMs
	for pos, w := range words {
		front := frontHTML(w)
		back := backHTML(w, mediaByWord[w.id])
		fields := front + fieldSep + back
		sfld := stripHTML(front)
		csum := fieldChecksum(sfld)
		guid := makeGUID(deckName, w.id)

		if _, err := noteStmt.Exec(noteID, guid, modelID, now.Unix(), -1, "", fields, sfld, csum, 0, ""); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert note word=%d: %w", w.id, err)
		}
		if _, err := cardStmt.Exec(cardID, noteID, deckID, 0, now.Unix(), -1,
			0 /*type=new*/, 0 /*queue=new*/, pos+1 /*due=pos*/, 0, 0, 0, 0, 0, 0, 0, 0, ""); err != nil {
			tx.Rollback()
			return fmt.Errorf("insert card word=%d: %w", w.id, err)
		}
		noteID++
		cardID++
	}

	// col row.
	conf := map[string]any{
		"nextPos":       len(words) + 1,
		"estTimes":      true,
		"activeDecks":   []int64{deckID},
		"sortType":      "noteFld",
		"timeLim":       0,
		"sortBackwards": false,
		"addToCur":      true,
		"curDeck":       deckID,
		"newBury":       true,
		"newSpread":     0,
		"dueCounts":     true,
		"curModel":      strconv.FormatInt(modelID, 10),
		"collapseTime":  1200,
	}
	models := map[string]any{
		strconv.FormatInt(modelID, 10): map[string]any{
			"id":        modelID,
			"name":      "Recall German",
			"type":      0,
			"mod":       now.Unix(),
			"usn":       -1,
			"sortf":     0,
			"did":       deckID,
			"latexPre":  "\\documentclass[12pt]{article}\n\\special{papersize=3in,5in}\n\\usepackage[utf8]{inputenc}\n\\usepackage{amssymb,amsmath}\n\\pagestyle{empty}\n\\setlength{\\parindent}{0in}\n\\begin{document}\n",
			"latexPost": "\\end{document}",
			"latexsvg":  false,
			"req":       [][]any{{0, "any", []int{0}}},
			"vers":      []any{},
			"tags":      []any{},
			"flds": []map[string]any{
				{"name": "Front", "ord": 0, "sticky": false, "rtl": false, "font": "Arial", "size": 20, "media": []any{}},
				{"name": "Back", "ord": 1, "sticky": false, "rtl": false, "font": "Arial", "size": 20, "media": []any{}},
			},
			"tmpls": []map[string]any{{
				"name":  "Card 1",
				"ord":   0,
				"qfmt":  "{{Front}}",
				"afmt":  "{{FrontSide}}\n\n<hr id=answer>\n\n{{Back}}",
				"bqfmt": "",
				"bafmt": "",
				"did":   nil,
			}},
			"css": cardCSS,
		},
	}
	decks := map[string]any{
		"1": defaultDeckJSON(1, "Default"),
		strconv.FormatInt(deckID, 10): defaultDeckJSON(deckID, "Recall::"+deckName),
	}
	dconf := map[string]any{
		"1": map[string]any{
			"id": 1, "name": "Default", "usn": 0, "maxTaken": 60, "autoplay": true,
			"timer": 0, "replayq": true, "mod": 0,
			"new":   map[string]any{"bury": false, "delays": []float64{1, 10}, "initialFactor": 2500, "ints": []int{1, 4, 7}, "order": 1, "perDay": 20, "separate": true},
			"rev":   map[string]any{"bury": false, "ease4": 1.3, "fuzz": 0.05, "ivlFct": 1.0, "maxIvl": 36500, "perDay": 200, "hardFactor": 1.2},
			"lapse": map[string]any{"delays": []int{10}, "leechAction": 0, "leechFails": 8, "minInt": 1, "mult": 0.0},
		},
	}

	confJSON, _ := json.Marshal(conf)
	modelsJSON, _ := json.Marshal(models)
	decksJSON, _ := json.Marshal(decks)
	dconfJSON, _ := json.Marshal(dconf)

	if _, err := tx.Exec(`INSERT INTO col (id, crt, mod, scm, ver, dty, usn, ls, conf, models, decks, dconf, tags) VALUES (1, ?, ?, ?, 11, 0, 0, 0, ?, ?, ?, ?, '{}')`,
		now.Unix(), nowMs, nowMs, string(confJSON), string(modelsJSON), string(decksJSON), string(dconfJSON)); err != nil {
		tx.Rollback()
		return fmt.Errorf("insert col: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	if err := conn.Close(); err != nil {
		return err
	}

	// Bundle the SQLite + media into the .apkg zip.
	zipFile, err := os.Create(outPath)
	if err != nil {
		return err
	}
	defer zipFile.Close()
	zw := zip.NewWriter(zipFile)
	defer zw.Close()

	if err := addFileToZip(zw, "collection.anki2", dbPath); err != nil {
		return err
	}
	manifest := make(map[string]string, len(media))
	for _, m := range media {
		idx := strconv.Itoa(m.index)
		manifest[idx] = m.filename
		if err := addFileToZip(zw, idx, m.srcPath); err != nil {
			return fmt.Errorf("add media %s: %w", m.filename, err)
		}
	}
	manifestJSON, _ := json.Marshal(manifest)
	mw, err := zw.Create("media")
	if err != nil {
		return err
	}
	if _, err := mw.Write(manifestJSON); err != nil {
		return err
	}
	return nil
}

func defaultDeckJSON(id int64, name string) map[string]any {
	return map[string]any{
		"id":         id,
		"name":       name,
		"extendRev":  50,
		"usn":        0,
		"collapsed":  false,
		"newToday":   []int{0, 0},
		"timeToday":  []int{0, 0},
		"dyn":        0,
		"extendNew":  10,
		"conf":       1,
		"revToday":   []int{0, 0},
		"lrnToday":   []int{0, 0},
		"mod":        0,
		"desc":       "",
		"browserCollapsed": false,
	}
}

func addFileToZip(zw *zip.Writer, name, srcPath string) error {
	w, err := zw.Create(name)
	if err != nil {
		return err
	}
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, f)
	return err
}

// fieldChecksum is sha1(stripHTML(field0))[:8] interpreted as a hex int — used
// by Anki's duplicate-detection index. We strip HTML on the front field (the
// sort field) the same way Anki does.
func fieldChecksum(s string) int64 {
	h := sha1.Sum([]byte(s))
	hex8 := hex.EncodeToString(h[:])[:8]
	n, _ := strconv.ParseInt(hex8, 16, 64)
	return n
}

// stripHTML is a tiny HTML stripper good enough for the sort field. Anki's own
// implementation removes tags and decodes entities; for our content (no entities
// beyond &amp; in lemmas, no nested markup) this is sufficient.
func stripHTML(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch {
		case r == '<':
			depth++
		case r == '>' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(r)
		}
	}
	return html.UnescapeString(b.String())
}

// makeGUID returns a stable GUID for (deck, wordID). Anki uses random 10-char
// base91 strings; ours just needs to be unique. We hash deck+id and take 10 hex
// chars — Anki accepts any non-empty ASCII as a GUID.
func makeGUID(deck string, wordID int64) string {
	h := sha1.Sum([]byte(fmt.Sprintf("recall:%s:%d", deck, wordID)))
	return "r" + hex.EncodeToString(h[:])[:10]
}
