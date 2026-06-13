// build-conjugations downloads the kaikki.org German Wiktionary extract,
// filters to verbs only, and emits a compact JSONL suitable for shipping in
// seed/de_verb_conjugations.jsonl. The runtime reads that small file at boot
// (see internal/conjugations) — this tool is run by maintainers, not at boot.
//
// Usage:
//
//	go run ./cmd/build-conjugations -out seed/de_verb_conjugations.jsonl
//	go run ./cmd/build-conjugations -src /path/to/de-extract.jsonl.gz -out seed/de_verb_conjugations.jsonl
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

// kaikkiEntry is the subset of a de.wiktionary kaikki line we actually consume.
// Each German base verb has a forms[] array combining the page's Übersicht
// (compact 3-form table) and the linked Flexion sub-page (full Indikativ +
// Konjunktiv tables).
type kaikkiEntry struct {
	Word     string       `json:"word"`
	Pos      string       `json:"pos"`
	LangCode string       `json:"lang_code"`
	Tags     []string     `json:"tags"` // includes "form-of" on conjugated-form entries
	Forms    []kaikkiForm `json:"forms"`
}

type kaikkiForm struct {
	Form     string   `json:"form"`
	Tags     []string `json:"tags"`
	Pronouns []string `json:"pronouns"`
}

// outEntry is one line of the shipped seed file. Matches the shape consumed
// by internal/conjugations.Entry.
type outEntry struct {
	Infinitive  string            `json:"infinitive"`
	Praesens    map[string]string `json:"praesens"`
	Praeteritum map[string]string `json:"praeteritum,omitempty"`
	Perfekt     outPerfekt        `json:"perfekt"`
}

type outPerfekt struct {
	Aux       string `json:"aux"`
	Partizip2 string `json:"partizip2"`
}

func main() {
	src := flag.String("src", defaultSource, "kaikki German extract: URL (gz) or local .jsonl/.jsonl.gz path")
	out := flag.String("out", "seed/de_verb_conjugations.jsonl", "output JSONL path")
	dumpN := flag.Int("dump", 0, "print the first N raw verb entries to stdout and exit (diagnostic)")
	flag.Parse()

	r, err := openSource(*src)
	if err != nil {
		log.Fatalf("open source: %v", err)
	}
	defer r.Close()

	if *dumpN > 0 {
		dumpVerbs(r, *dumpN)
		return
	}

	outF, err := os.Create(*out)
	if err != nil {
		log.Fatalf("create %s: %v", *out, err)
	}
	defer outF.Close()
	bw := bufio.NewWriterSize(outF, 1<<20)
	defer bw.Flush()

	seen := map[string]outEntry{} // dedupe: keep most complete entry per infinitive
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 256*1024), 4<<20)
	lines, deVerbs, kept := 0, 0, 0
	t0 := time.Now()
	for scanner.Scan() {
		lines++
		if lines%200000 == 0 {
			log.Printf("scan: %d lines (%d de verbs, %d kept) elapsed %s", lines, deVerbs, kept, time.Since(t0).Round(time.Second))
		}
		var ke kaikkiEntry
		if err := json.Unmarshal(scanner.Bytes(), &ke); err != nil {
			continue
		}
		if ke.LangCode != "de" || !strings.EqualFold(ke.Pos, "verb") || ke.Word == "" {
			continue
		}
		if slices.Contains(ke.Tags, "form-of") {
			continue // these are conjugated-form entries, not the base verb
		}
		deVerbs++
		entry, ok := extract(ke)
		if !ok {
			continue
		}
		if prev, exists := seen[entry.Infinitive]; exists && score(prev) >= score(entry) {
			continue
		}
		seen[entry.Infinitive] = entry
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
	log.Printf("done: %d lines, %d de verbs, %d unique infinitives written to %s in %s",
		lines, deVerbs, len(seen), *out, time.Since(t0).Round(time.Second))
}

// dumpVerbs streams the source and prints the first n German base verb
// entries (lang_code "de", pos "verb", NOT a form-of entry) verbatim to
// stdout, one per line. Used to discover the actual schema.
func dumpVerbs(r io.Reader, n int) {
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
		if !strings.EqualFold(probe.Pos, "verb") || probe.LangCode != "de" {
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
		req.Header.Set("User-Agent", "recall/build-conjugations (+https://github.com/junaidk/recall)")
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

// extract pulls Präsens + Perfekt out of one kaikki German base-verb entry.
// Returns (entry, true) only when all six Präsens cells and the Partizip II
// are present — anything less would render as a half-empty table on the
// card, so we'd rather skip the verb than ship a partial entry.
//
// Schema (per de.wiktionary via kaikki):
//   - Full Präsens table lives under forms with tags
//     ["first|second|third-person", "singular|plural", "present", "active",
//     "indicative"]. The form value is the pronoun + verb (e.g. "ich liebe"
//     or "er/sie/es liebt"), which we strip back to the bare form.
//   - The Präteritum (simple past) table is identical in shape to Präsens but
//     tagged "past" instead of "present"; we require "indicative"+"active" to
//     exclude the Konjunktiv II (subjunctive-ii) and passive rows that share
//     the "past" tag. Präteritum is supplementary: a verb missing it is still
//     kept (only attached when all six cells are present).
//   - Partizip II appears with tags ["participle-2", "perfect"] in the
//     compact Übersicht section (no pronoun prefix).
//   - Perfekt auxiliary appears as a separate form with tags
//     ["auxiliary", "perfect"], where the form value is "haben" or "sein".
func extract(ke kaikkiEntry) (outEntry, bool) {
	praesens := map[string]string{}
	praeteritum := map[string]string{}
	var partizip2, aux string

	for _, f := range ke.Forms {
		if f.Form == "" {
			continue
		}
		// Full Präsens indicative active row from the Flexion table.
		if hasTag(f.Tags, "present") && hasTag(f.Tags, "indicative") && hasTag(f.Tags, "active") {
			key := personKey(f.Tags)
			if key != "" {
				if form := stripPronoun(f.Form); form != "" {
					if _, taken := praesens[key]; !taken {
						praesens[key] = form
					}
				}
			}
		}
		// Full Präteritum indicative active row — same shape as Präsens.
		if hasTag(f.Tags, "past") && hasTag(f.Tags, "indicative") && hasTag(f.Tags, "active") {
			key := personKey(f.Tags)
			if key != "" {
				if form := stripPronoun(f.Form); form != "" {
					if _, taken := praeteritum[key]; !taken {
						praeteritum[key] = form
					}
				}
			}
		}
		// Partizip II — compact form from the Übersicht, no pronoun prefix.
		if partizip2 == "" && hasTag(f.Tags, "participle-2") && hasTag(f.Tags, "perfect") {
			partizip2 = f.Form
		}
		// Perfekt auxiliary — form value is "haben" or "sein".
		if aux == "" && hasTag(f.Tags, "auxiliary") && hasTag(f.Tags, "perfect") {
			if f.Form == "haben" || f.Form == "sein" {
				aux = f.Form
			}
		}
	}

	if len(praesens) < 6 || partizip2 == "" {
		return outEntry{}, false
	}
	if aux == "" {
		aux = "haben" // safe default — vast majority of verbs take haben
	}
	out := outEntry{
		Infinitive: ke.Word,
		Praesens:   praesens,
		Perfekt:    outPerfekt{Aux: aux, Partizip2: partizip2},
	}
	if len(praeteritum) == 6 {
		out.Praeteritum = praeteritum
	}
	return out, true
}

// personKey maps kaikki person/number tags to the labels we render on the
// card. Returns "" if the form is not one of the six finite-verb slots.
func personKey(tags []string) string {
	first := hasTag(tags, "first-person")
	second := hasTag(tags, "second-person")
	third := hasTag(tags, "third-person")
	sing := hasTag(tags, "singular")
	plur := hasTag(tags, "plural")
	switch {
	case first && sing:
		return "ich"
	case second && sing:
		return "du"
	case third && sing:
		return "er"
	case first && plur:
		return "wir"
	case second && plur:
		return "ihr"
	case third && plur:
		return "sie"
	}
	return ""
}

// stripPronoun removes the leading pronoun from a de.wiktionary Präsens
// form. The Flexion table emits values like "ich liebe", "du liebst", or
// "er/sie/es liebt"; the bare verb form is what we render. For unexpected
// prefixes it returns the whole string so we never drop data silently.
func stripPronoun(form string) string {
	prefixes := []string{"er/sie/es ", "ich ", "du ", "wir ", "ihr ", "sie "}
	for _, p := range prefixes {
		if strings.HasPrefix(form, p) {
			return strings.TrimSpace(form[len(p):])
		}
	}
	return form
}

func hasTag(tags []string, want string) bool {
	return slices.Contains(tags, want)
}

// score rewards entries with more populated Präsens cells, used to pick the
// most complete entry when multiple senses share an infinitive.
func score(e outEntry) int {
	n := len(e.Praesens) + len(e.Praeteritum)
	if e.Perfekt.Partizip2 != "" {
		n++
	}
	if e.Perfekt.Aux != "" {
		n++
	}
	return n
}
