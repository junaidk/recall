package handlers

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/models"
	"github.com/junaidk/recall/internal/sentences"
)

type cardEditView struct {
	User          *models.User
	CardID        int64
	WordID        int64
	DeckID        int64
	Display       string
	TranslationEN string
	ExampleDE     string
	ExampleEN     string
}

type editCandidatesView struct {
	CardID     int64
	Candidates []sentences.Candidate
}

func (s *Server) handleEditCardForm(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	cardID, err := strconv.ParseInt(r.PathValue("cardID"), 10, 64)
	if err != nil {
		http.Error(w, "bad card id", http.StatusBadRequest)
		return
	}

	var (
		view     = cardEditView{User: u, CardID: cardID}
		lemma    string
		articles sql.NullString
		trans    sql.NullString
		exDE     sql.NullString
		exEN     sql.NullString
	)
	err = s.DB.QueryRow(`
		SELECT w.id, w.deck_id, w.lemma, w.articles, w.translation_en, w.example_de, w.example_en
		FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.id = ? AND c.user_id = ?
	`, cardID, u.ID).Scan(&view.WordID, &view.DeckID, &lemma, &articles, &trans, &exDE, &exEN)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "load card: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view.Display = displayLemma(lemma, articles.String)
	view.TranslationEN = trans.String
	view.ExampleDE = exDE.String
	view.ExampleEN = exEN.String

	s.Templates.RenderPage(w, "card_edit.html", view)
}

func (s *Server) handleEditCard(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	cardID, err := strconv.ParseInt(r.PathValue("cardID"), 10, 64)
	if err != nil {
		http.Error(w, "bad card id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form: "+err.Error(), http.StatusBadRequest)
		return
	}

	var wordID, deckID int64
	err = s.DB.QueryRow(`
		SELECT w.id, w.deck_id
		FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.id = ? AND c.user_id = ?
	`, cardID, u.ID).Scan(&wordID, &deckID)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "load card: "+err.Error(), http.StatusInternalServerError)
		return
	}

	trans := nullIfBlank(r.FormValue("translation_en"))
	exDE := nullIfBlank(r.FormValue("example_de"))
	exEN := nullIfBlank(r.FormValue("example_en"))

	if _, err := s.DB.Exec(
		`UPDATE words SET translation_en = ?, example_de = ?, example_en = ? WHERE id = ?`,
		trans, exDE, exEN, wordID,
	); err != nil {
		http.Error(w, "update word: "+err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, fmt.Sprintf("/decks/%d/study?card=%d", deckID, cardID), http.StatusSeeOther)
}

func (s *Server) handleEditCandidates(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	cardID, err := strconv.ParseInt(r.PathValue("cardID"), 10, 64)
	if err != nil {
		http.Error(w, "bad card id", http.StatusBadRequest)
		return
	}

	var lemma string
	err = s.DB.QueryRow(`
		SELECT w.lemma
		FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.id = ? AND c.user_id = ?
	`, cardID, u.ID).Scan(&lemma)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "load card: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cands, err := sentences.Candidates(s.DB, lemma, 20)
	if err != nil {
		http.Error(w, "candidates: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Templates.RenderPartial(w, "edit_candidates", editCandidatesView{CardID: cardID, Candidates: cands})
}

func nullIfBlank(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	return s
}
