package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/fsrs"
	"github.com/junaidk/recall/internal/models"
)

// Module-level scheduler instance — FSRS is stateless, just holds params.
var scheduler = fsrs.New()

type studyPage struct {
	User *models.User
	Deck models.Deck
}

type cardView struct {
	Card        models.Card
	DeckID      int64
	Display     string // German with article, e.g. "die Ampel"
	Pos         string
	Translation string
	URL         string
	ExampleDE   string
	ExampleEN   string
}

func (s *Server) handleStudyPage(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	deckID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad deck id", http.StatusBadRequest)
		return
	}
	var d models.Deck
	if err := s.DB.QueryRow(`SELECT id, name FROM decks WHERE id = ?`, deckID).Scan(&d.ID, &d.Name); err != nil {
		http.NotFound(w, r)
		return
	}
	// Seed cards for any words in this deck the user has not seen yet.
	if _, err := s.DB.Exec(`
		INSERT OR IGNORE INTO cards (user_id, word_id, due, state)
		SELECT ?, id, CURRENT_TIMESTAMP, 0 FROM words WHERE deck_id = ?
	`, u.ID, deckID); err != nil {
		http.Error(w, "seed cards: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Templates.RenderPage(w, "review.html", studyPage{User: u, Deck: d})
}

func (s *Server) handleNextCard(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	deckID, err := strconv.ParseInt(r.PathValue("deckID"), 10, 64)
	if err != nil {
		http.Error(w, "bad deck id", http.StatusBadRequest)
		return
	}
	cv, err := s.fetchNextCardView(u.ID, deckID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.Templates.RenderPartial(w, "done", nil)
			return
		}
		http.Error(w, "fetch next: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Templates.RenderPartial(w, "card_front", cv)
}

func (s *Server) handleRevealCard(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	cardID, err := strconv.ParseInt(r.PathValue("cardID"), 10, 64)
	if err != nil {
		http.Error(w, "bad card id", http.StatusBadRequest)
		return
	}
	cv, err := s.loadCardView(u.ID, cardID)
	if err != nil {
		http.Error(w, "load card: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Templates.RenderPartial(w, "card_back", cv)
}

func (s *Server) handleGrade(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	cardID, err := strconv.ParseInt(r.PathValue("cardID"), 10, 64)
	if err != nil {
		http.Error(w, "bad card id", http.StatusBadRequest)
		return
	}
	ratingN, _ := strconv.Atoi(r.URL.Query().Get("rating"))
	rating := fsrs.RatingFromInt(ratingN)
	if rating == 0 {
		http.Error(w, "rating must be 1..4", http.StatusBadRequest)
		return
	}

	card, deckID, err := s.loadCard(u.ID, cardID)
	if err != nil {
		http.Error(w, "load card: "+err.Error(), http.StatusInternalServerError)
		return
	}

	updated, log := scheduler.Grade(card, rating, time.Now())

	tx, err := s.DB.Begin()
	if err != nil {
		http.Error(w, "tx: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`
		UPDATE cards SET
		  due = ?, stability = ?, difficulty = ?,
		  elapsed_days = ?, scheduled_days = ?,
		  reps = ?, lapses = ?, state = ?, last_review = ?
		WHERE id = ? AND user_id = ?
	`, updated.Due, updated.Stability, updated.Difficulty,
		updated.ElapsedDays, updated.ScheduledDays,
		updated.Reps, updated.Lapses, updated.State, updated.LastReview,
		cardID, u.ID,
	); err != nil {
		http.Error(w, "update card: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`
		INSERT INTO review_logs (card_id, rating, state, elapsed_days, scheduled_days, reviewed_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, cardID, int(rating), int(log.State), log.ElapsedDays, log.ScheduledDays, log.Review); err != nil {
		http.Error(w, "insert review log: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "commit: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cv, err := s.fetchNextCardView(u.ID, deckID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.Templates.RenderPartial(w, "done", nil)
			return
		}
		http.Error(w, "fetch next: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Templates.RenderPartial(w, "card_front", cv)
}

// --- helpers ---

func (s *Server) loadCard(userID, cardID int64) (models.Card, int64, error) {
	var c models.Card
	var deckID int64
	err := s.DB.QueryRow(`
		SELECT c.id, c.user_id, c.word_id, c.due, c.stability, c.difficulty,
		       c.elapsed_days, c.scheduled_days, c.reps, c.lapses, c.state, c.last_review,
		       w.deck_id
		FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.id = ? AND c.user_id = ?
	`, cardID, userID).Scan(
		&c.ID, &c.UserID, &c.WordID, &c.Due, &c.Stability, &c.Difficulty,
		&c.ElapsedDays, &c.ScheduledDays, &c.Reps, &c.Lapses, &c.State, &c.LastReview,
		&deckID,
	)
	return c, deckID, err
}

func (s *Server) loadCardView(userID, cardID int64) (cardView, error) {
	var (
		cv       cardView
		articles sql.NullString
		pos      sql.NullString
		url      sql.NullString
		trans    sql.NullString
		exDE     sql.NullString
		exEN     sql.NullString
		lemma    string
	)
	err := s.DB.QueryRow(`
		SELECT c.id, c.user_id, c.word_id, c.due, c.stability, c.difficulty,
		       c.elapsed_days, c.scheduled_days, c.reps, c.lapses, c.state, c.last_review,
		       w.deck_id, w.lemma, w.pos, w.articles, w.url, w.translation_en,
		       w.example_de, w.example_en
		FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.id = ? AND c.user_id = ?
	`, cardID, userID).Scan(
		&cv.Card.ID, &cv.Card.UserID, &cv.Card.WordID, &cv.Card.Due,
		&cv.Card.Stability, &cv.Card.Difficulty,
		&cv.Card.ElapsedDays, &cv.Card.ScheduledDays,
		&cv.Card.Reps, &cv.Card.Lapses, &cv.Card.State, &cv.Card.LastReview,
		&cv.DeckID, &lemma, &pos, &articles, &url, &trans,
		&exDE, &exEN,
	)
	if err != nil {
		return cv, err
	}
	cv.Display = displayLemma(lemma, articles.String)
	cv.Pos = pos.String
	if trans.Valid {
		cv.Translation = trans.String
	}
	if url.Valid {
		cv.URL = url.String
	}
	if exDE.Valid {
		cv.ExampleDE = exDE.String
	}
	if exEN.Valid {
		cv.ExampleEN = exEN.String
	}
	return cv, nil
}

func (s *Server) fetchNextCardView(userID, deckID int64) (cardView, error) {
	var (
		cv       cardView
		articles sql.NullString
		pos      sql.NullString
		url      sql.NullString
		trans    sql.NullString
		exDE     sql.NullString
		exEN     sql.NullString
		lemma    string
	)
	cv.DeckID = deckID
	err := s.DB.QueryRow(`
		SELECT c.id, c.user_id, c.word_id, c.due, c.stability, c.difficulty,
		       c.elapsed_days, c.scheduled_days, c.reps, c.lapses, c.state, c.last_review,
		       w.lemma, w.pos, w.articles, w.url, w.translation_en,
		       w.example_de, w.example_en
		FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.user_id = ? AND w.deck_id = ? AND c.due <= CURRENT_TIMESTAMP
		ORDER BY c.due ASC, RANDOM()
		LIMIT 1
	`, userID, deckID).Scan(
		&cv.Card.ID, &cv.Card.UserID, &cv.Card.WordID, &cv.Card.Due,
		&cv.Card.Stability, &cv.Card.Difficulty,
		&cv.Card.ElapsedDays, &cv.Card.ScheduledDays,
		&cv.Card.Reps, &cv.Card.Lapses, &cv.Card.State, &cv.Card.LastReview,
		&lemma, &pos, &articles, &url, &trans,
		&exDE, &exEN,
	)
	if err != nil {
		return cv, err
	}
	cv.Display = displayLemma(lemma, articles.String)
	cv.Pos = pos.String
	if trans.Valid {
		cv.Translation = trans.String
	}
	if url.Valid {
		cv.URL = url.String
	}
	if exDE.Valid {
		cv.ExampleDE = exDE.String
	}
	if exEN.Valid {
		cv.ExampleEN = exEN.String
	}
	return cv, nil
}

// displayLemma renders a noun with its article (e.g. "die Ampel"); other parts
// of speech are returned as-is.
func displayLemma(lemma, articlesJSON string) string {
	if articlesJSON == "" {
		return lemma
	}
	var arts []string
	if err := json.Unmarshal([]byte(articlesJSON), &arts); err != nil || len(arts) == 0 {
		return lemma
	}
	return fmt.Sprintf("%s %s", strings.Join(arts, "/"), lemma)
}

