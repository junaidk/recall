package handlers

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/junaidk/recall/internal/web"
)

// gehenJSON is a representative irregular verb (sein-auxiliary) carrying all
// three tenses, used to exercise the Präteritum path end to end.
const gehenJSON = `{"praesens":{"ich":"gehe","du":"gehst","er":"geht","wir":"gehen","ihr":"geht","sie":"gehen"},` +
	`"praeteritum":{"ich":"ging","du":"gingst","er":"ging","wir":"gingen","ihr":"gingt","sie":"gingen"},` +
	`"perfekt":{"aux":"sein","partizip2":"gegangen"}}`

func TestParseConjugationsPraeteritum(t *testing.T) {
	cv := parseConjugations(gehenJSON)
	if cv == nil {
		t.Fatal("parseConjugations returned nil")
	}
	if len(cv.Praeteritum) != 6 {
		t.Fatalf("Praeteritum rows = %d, want 6", len(cv.Praeteritum))
	}
	want := []personForm{
		{"ich", "ging"}, {"du", "gingst"}, {"er/sie/es", "ging"},
		{"wir", "gingen"}, {"ihr", "gingt"}, {"sie", "gingen"},
	}
	for i, w := range want {
		if cv.Praeteritum[i] != w {
			t.Errorf("Praeteritum[%d] = %+v, want %+v", i, cv.Praeteritum[i], w)
		}
	}
}

// Verbs predating the Präteritum corpus have no praeteritum key; the panel
// must still render Präsens + Perfekt without an empty Präteritum table.
func TestParseConjugationsNoPraeteritum(t *testing.T) {
	raw := `{"praesens":{"ich":"mache","du":"machst","er":"macht","wir":"machen","ihr":"macht","sie":"machen"},` +
		`"perfekt":{"aux":"haben","partizip2":"gemacht"}}`
	cv := parseConjugations(raw)
	if cv == nil {
		t.Fatal("parseConjugations returned nil")
	}
	if len(cv.Praeteritum) != 0 {
		t.Errorf("Praeteritum rows = %d, want 0", len(cv.Praeteritum))
	}
}

func TestConjugationsBlockRendersPraeteritum(t *testing.T) {
	cv := parseConjugations(gehenJSON)
	var buf bytes.Buffer
	data := struct{ Conjugations *conjugationView }{cv}
	if err := web.MustLoadTemplates().RenderPartial(&buf, "conjugations_block", data); err != nil {
		t.Fatalf("render: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"Präsens", "Präteritum", "Perfekt", "ging", "gingst", "bin gegangen"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered card missing %q", want)
		}
	}
	// Conventional German tense order: Präsens → Präteritum → Perfekt.
	pres, pret, perf := strings.Index(out, "Präsens"), strings.Index(out, "Präteritum"), strings.Index(out, "Perfekt")
	if !(pres < pret && pret < perf) {
		t.Errorf("tense order wrong: Präsens@%d Präteritum@%d Perfekt@%d", pres, pret, perf)
	}
}

func TestFormatInterval(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{-5 * time.Second, "<1m"},
		{30 * time.Second, "<1m"},
		{time.Minute, "1m"},
		{9*time.Minute + 40*time.Second, "10m"},
		{59 * time.Minute, "59m"},
		{90 * time.Minute, "2h"},
		{23 * time.Hour, "23h"},
		{36 * time.Hour, "2d"},
		{29 * 24 * time.Hour, "29d"},
		{61 * 24 * time.Hour, "2mo"},
		{45 * 24 * time.Hour, "1.5mo"},
		{400 * 24 * time.Hour, "1.1y"},
		{730 * 24 * time.Hour, "2y"},
	}
	for _, c := range cases {
		if got := formatInterval(c.in); got != c.want {
			t.Errorf("formatInterval(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}
