package handlers

import (
	"net/http"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/models"
)

type decksPage struct {
	User  *models.User
	Decks []models.DeckProgress
}

func (s *Server) handleDecks(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	rows, err := s.DB.Query(`
		SELECT
		  d.id, d.name,
		  COUNT(w.id) AS total,
		  COALESCE(SUM(CASE WHEN c.state IS NULL OR c.state = 0 THEN 1 ELSE 0 END), 0) AS new_count,
		  COALESCE(SUM(CASE WHEN c.state IN (1, 3) THEN 1 ELSE 0 END), 0) AS learning,
		  COALESCE(SUM(CASE WHEN c.state = 2 THEN 1 ELSE 0 END), 0) AS review,
		  COALESCE(SUM(CASE WHEN c.id IS NULL OR c.due <= CURRENT_TIMESTAMP THEN 1 ELSE 0 END), 0) AS due_now
		FROM decks d
		LEFT JOIN words w ON w.deck_id = d.id
		LEFT JOIN cards c ON c.word_id = w.id AND c.user_id = ?
		GROUP BY d.id, d.name
		ORDER BY d.name
	`, u.ID)
	if err != nil {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	var out []models.DeckProgress
	for rows.Next() {
		var dp models.DeckProgress
		if err := rows.Scan(&dp.Deck.ID, &dp.Deck.Name, &dp.Total, &dp.New, &dp.Learning, &dp.Review, &dp.DueNow); err != nil {
			http.Error(w, "scan error", http.StatusInternalServerError)
			return
		}
		out = append(out, dp)
	}
	s.Templates.RenderPage(w, "decks.html", decksPage{User: u, Decks: out})
}
