// build-noun-plurals downloads the kaikki.org German Wiktionary extract,
// filters to nouns only, and emits a compact JSONL suitable for shipping in
// seed/de_noun_plurals.jsonl. The runtime reads that small file at boot
// (see internal/plurals) — this tool is run by maintainers, not at boot.
//
// Usage:
//
//	go run ./cmd/build-noun-plurals -out seed/de_noun_plurals.jsonl
//	go run ./cmd/build-noun-plurals -src /path/to/de-extract.jsonl.gz -out seed/de_noun_plurals.jsonl
package main

import (
	"bufio"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"slices"
	"sort"
	"strings"
	"time"
)

const defaultSource = "https://kaikki.org/dictionary/downloads/de/de-extract.jsonl.gz"

// kaikkiEntry is the subset of a de.wiktionary kaikki line we consume.
// German nouns publish a forms[] array combining the Übersicht (compact
// nominative singular/plural) and the linked Flexion sub-page (full
// Nom/Akk/Dat/Gen × Sg/Pl table).
type kaikkiEntry struct {
	Word     string       `json:"word"`
	Pos      string       `json:"pos"`
	LangCode string       `json:"lang_code"`
	Tags     []string     `json:"tags"` // includes "form-of" on declined-form entries
	Forms    []kaikkiForm `json:"forms"`
}

type kaikkiForm struct {
	Form string   `json:"form"`
	Tags []string `json:"tags"`
}

// outEntry is one line of the shipped seed file. Matches the shape consumed
// by internal/plurals.Entry.
type outEntry struct {
	Lemma string   `json:"lemma"`
	Sg    outForms `json:"sg"`
	Pl    outForms `json:"pl"`
}

type outForms struct {
	Nom string `json:"nom,omitempty"`
	Akk string `json:"akk,omitempty"`
	Dat string `json:"dat,omitempty"`
	Gen string `json:"gen,omitempty"`
}

// skipTags lists kaikki tag values that mark a form as something we don't
// want to surface on a learner card (dialect, archaic, etc.). Conservative:
// only the obvious cases.
var skipTags = []string{
	"colloquial",
	"obsolete",
	"archaic",
	"rare",
	"dialectal",
	"nonstandard",
}

func main() {
	src := flag.String("src", defaultSource, "kaikki German extract: URL (gz) or local .jsonl/.jsonl.gz path")
	out := flag.String("out", "seed/de_noun_plurals.jsonl", "output JSONL path")
	dumpN := flag.Int("dump", 0, "print the first N raw noun entries to stdout and exit (diagnostic)")
	flag.Parse()

	r, err := openSource(*src)
	if err != nil {
		log.Fatalf("open source: %v", err)
	}
	defer r.Close()

	if *dumpN > 0 {
		dumpNouns(r, *dumpN)
		return
	}

	outF, err := os.Create(*out)
	if err != nil {
		log.Fatalf("create %s: %v", *out, err)
	}
	defer outF.Close()
	bw := bufio.NewWriterSize(outF, 1<<20)
	defer bw.Flush()

	seen := map[string]outEntry{} // dedupe: keep most complete entry per lemma
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 4<<20)
	lines, deNouns, kept := 0, 0, 0
	t0 := time.Now()
	for scanner.Scan() {
		lines++
		if lines%200000 == 0 {
			log.Printf("scan: %d lines (%d de nouns, %d kept) elapsed %s", lines, deNouns, kept, time.Since(t0).Round(time.Second))
		}
		var ke kaikkiEntry
		if err := json.Unmarshal(scanner.Bytes(), &ke); err != nil {
			continue
		}
		if ke.LangCode != "de" || !strings.EqualFold(ke.Pos, "noun") || ke.Word == "" {
			continue
		}
		if slices.Contains(ke.Tags, "form-of") {
			continue // declined-form entries, not the base noun
		}
		deNouns++
		entry, ok := extract(ke)
		if !ok {
			continue
		}
		if prev, exists := seen[entry.Lemma]; exists && score(prev) >= score(entry) {
			continue
		}
		seen[entry.Lemma] = entry
		kept++
	}
	if err := scanner.Err(); err != nil {
		log.Fatalf("scan: %v", err)
	}

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		b, _ := json.Marshal(seen[k])
		bw.Write(b)
		bw.WriteByte('\n')
	}
	log.Printf("done: %d lines, %d de nouns, %d unique lemmas written to %s in %s",
		lines, deNouns, len(seen), *out, time.Since(t0).Round(time.Second))
}

// dumpNouns streams the source and prints the first n German base noun
// entries verbatim to stdout. Diagnostic only.
func dumpNouns(r io.Reader, n int) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 4<<20)
	printed := 0
	for scanner.Scan() && printed < n {
		var probe struct {
			Pos      string   `json:"pos"`
			LangCode string   `json:"lang_code"`
			Tags     []string `json:"tags"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &probe); err != nil {
			continue
		}
		if !strings.EqualFold(probe.Pos, "noun") || probe.LangCode != "de" {
			continue
		}
		if slices.Contains(probe.Tags, "form-of") {
			continue
		}
		os.Stdout.Write(scanner.Bytes())
		os.Stdout.Write([]byte("\n"))
		printed++
	}
}

func openSource(src string) (io.ReadCloser, error) {
	if strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://") {
		log.Printf("fetching %s …", src)
		req, _ := http.NewRequest("GET", src, nil)
		req.Header.Set("User-Agent", "recall/build-noun-plurals (+https://github.com/junaidk/recall)")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			return nil, fmt.Errorf("status %d", resp.StatusCode)
		}
		if strings.HasSuffix(src, ".gz") {
			gz, err := gzip.NewReader(resp.Body)
			if err != nil {
				resp.Body.Close()
				return nil, err
			}
			return &teeCloser{Reader: gz, closers: []io.Closer{gz, resp.Body}}, nil
		}
		return resp.Body, nil
	}
	f, err := os.Open(src)
	if err != nil {
		return nil, err
	}
	if strings.HasSuffix(src, ".gz") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			f.Close()
			return nil, err
		}
		return &teeCloser{Reader: gz, closers: []io.Closer{gz, f}}, nil
	}
	return f, nil
}

type teeCloser struct {
	io.Reader
	closers []io.Closer
}

func (t *teeCloser) Close() error {
	var first error
	for _, c := range t.closers {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// extract pulls Nom/Akk/Dat/Gen × Sg/Pl out of one kaikki German base-noun
// entry. Returns (entry, true) only when a Nominativ Plural is present —
// that's the headline data we're missing from DWDS, and a card with no
// plural would render an empty Plural column.
//
// Schema (per de.wiktionary via kaikki):
//   - Each form has a `tags` array including the case ("nominative",
//     "genitive", "dative", "accusative") and number ("singular", "plural").
//   - We reject forms tagged "definite" (those carry the article inline,
//     e.g. "der Junge") and skip dialect/archaic variants.
//   - The first matching form per (case, number) wins so we prefer the
//     compact Übersicht over the Flexion sub-page (which sometimes lists
//     alternative inflections).
func extract(ke kaikkiEntry) (outEntry, bool) {
	var sg, pl outForms

	pick := func(target *string, form string) {
		if *target == "" {
			*target = form
		}
	}

	for _, f := range ke.Forms {
		if f.Form == "" {
			continue
		}
		if hasTag(f.Tags, "definite") {
			continue
		}
		if hasAnyTag(f.Tags, skipTags) {
			continue
		}

		isSg := hasTag(f.Tags, "singular")
		isPl := hasTag(f.Tags, "plural")
		if !isSg && !isPl {
			continue
		}
		switch {
		case hasTag(f.Tags, "nominative"):
			if isSg {
				pick(&sg.Nom, f.Form)
			} else {
				pick(&pl.Nom, f.Form)
			}
		case hasTag(f.Tags, "accusative"):
			if isSg {
				pick(&sg.Akk, f.Form)
			} else {
				pick(&pl.Akk, f.Form)
			}
		case hasTag(f.Tags, "dative"):
			if isSg {
				pick(&sg.Dat, f.Form)
			} else {
				pick(&pl.Dat, f.Form)
			}
		case hasTag(f.Tags, "genitive"):
			if isSg {
				pick(&sg.Gen, f.Form)
			} else {
				pick(&pl.Gen, f.Form)
			}
		}
	}

	if pl.Nom == "" {
		return outEntry{}, false
	}
	if sg.Nom == "" {
		sg.Nom = ke.Word
	}
	return outEntry{Lemma: ke.Word, Sg: sg, Pl: pl}, true
}

func hasTag(tags []string, want string) bool {
	return slices.Contains(tags, want)
}

func hasAnyTag(tags, wants []string) bool {
	for _, w := range wants {
		if slices.Contains(tags, w) {
			return true
		}
	}
	return false
}

// score rewards entries with more populated case slots, used to pick the
// most complete entry when multiple senses share a lemma.
func score(e outEntry) int {
	n := 0
	for _, s := range []string{e.Sg.Nom, e.Sg.Akk, e.Sg.Dat, e.Sg.Gen, e.Pl.Nom, e.Pl.Akk, e.Pl.Dat, e.Pl.Gen} {
		if s != "" {
			n++
		}
	}
	return n
}
