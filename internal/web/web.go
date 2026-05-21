package web

import (
	"embed"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"net/http"
)

//go:embed templates/*.html
var templateFS embed.FS

//go:embed static/*
var staticFS embed.FS

// Templates holds parsed template sets keyed by page name.
type Templates struct {
	pages map[string]*template.Template
}

func MustLoadTemplates() *Templates {
	t := &Templates{pages: map[string]*template.Template{}}

	// Pages: parse base.html + the page file together so the page can
	// override "title" and "body" blocks defined in base.
	pages := []string{"login.html", "register.html", "decks.html", "review.html", "stats.html", "card_edit.html"}
	for _, name := range pages {
		ts, err := template.ParseFS(templateFS, "templates/base.html", "templates/"+name)
		if err != nil {
			panic(fmt.Errorf("parse %s: %w", name, err))
		}
		t.pages[name] = ts
	}

	// Partials returned by HTMX endpoints. Each is a single named template
	// (no base layout); we invoke them via ExecuteTemplate.
	partials := []string{"_card_front.html", "_card_back.html", "_done.html", "_example_block.html", "_example_choices.html", "_conjugations_block.html", "_plurals_block.html", "_edit_candidates.html"}
	partialSet, err := template.ParseFS(templateFS, partialNames(partials)...)
	if err != nil {
		panic(fmt.Errorf("parse partials: %w", err))
	}
	t.pages["_partials"] = partialSet

	return t
}

func partialNames(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "templates/" + n
	}
	return out
}

// RenderPage writes a full page (base layout + page body) to w.
func (t *Templates) RenderPage(w http.ResponseWriter, name string, data any) error {
	ts, ok := t.pages[name]
	if !ok {
		return fmt.Errorf("template not found: %s", name)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return ts.ExecuteTemplate(w, "base", data)
}

// RenderPartial writes a named partial fragment (no layout).
func (t *Templates) RenderPartial(w io.Writer, name string, data any) error {
	if hw, ok := w.(http.ResponseWriter); ok {
		hw.Header().Set("Content-Type", "text/html; charset=utf-8")
	}
	return t.pages["_partials"].ExecuteTemplate(w, name, data)
}

// StaticHandler serves files from the embedded static/ directory.
func StaticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.StripPrefix("/static/", http.FileServer(http.FS(sub)))
}
