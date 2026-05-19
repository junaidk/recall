package handlers

import (
	"net/http"
	"strconv"
	"time"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/models"
)

type barRow struct {
	Label string
	Count int
	Pct   int
}

type dayBar struct {
	Date  string
	Count int
	Pct   int
}

type statsPage struct {
	User         *models.User
	Deck         models.Deck
	StateCounts  []barRow
	GradeCounts  []barRow
	DailyReviews []dayBar
	TotalReviews int
	RetentionPct int
	HasReviews   bool
}

func (s *Server) handleDeckStats(w http.ResponseWriter, r *http.Request) {
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

	stateCounts, err := s.statsByState(u.ID, deckID)
	if err != nil {
		http.Error(w, "state counts: "+err.Error(), http.StatusInternalServerError)
		return
	}

	gradeCounts, totalReviews, retention, err := s.statsByGrade(u.ID, deckID)
	if err != nil {
		http.Error(w, "grade counts: "+err.Error(), http.StatusInternalServerError)
		return
	}

	daily, err := s.statsDaily(u.ID, deckID, 30)
	if err != nil {
		http.Error(w, "daily reviews: "+err.Error(), http.StatusInternalServerError)
		return
	}

	s.Templates.RenderPage(w, "stats.html", statsPage{
		User:         u,
		Deck:         d,
		StateCounts:  stateCounts,
		GradeCounts:  gradeCounts,
		DailyReviews: daily,
		TotalReviews: totalReviews,
		RetentionPct: retention,
		HasReviews:   totalReviews > 0,
	})
}

func (s *Server) statsByState(userID, deckID int64) ([]barRow, error) {
	var newC, learnC, reviewC, relearnC int
	err := s.DB.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN c.state IS NULL OR c.state = 0 THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN c.state = 1 THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN c.state = 2 THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN c.state = 3 THEN 1 ELSE 0 END), 0)
		FROM words w
		LEFT JOIN cards c ON c.word_id = w.id AND c.user_id = ?
		WHERE w.deck_id = ?
	`, userID, deckID).Scan(&newC, &learnC, &reviewC, &relearnC)
	if err != nil {
		return nil, err
	}
	rows := []barRow{
		{Label: "New", Count: newC},
		{Label: "Learning", Count: learnC},
		{Label: "Review", Count: reviewC},
		{Label: "Relearning", Count: relearnC},
	}
	scaleByMax(rows)
	return rows, nil
}

func (s *Server) statsByGrade(userID, deckID int64) ([]barRow, int, int, error) {
	rows, err := s.DB.Query(`
		SELECT rl.rating, COUNT(*)
		FROM review_logs rl
		JOIN cards c ON c.id = rl.card_id
		JOIN words w ON w.id = c.word_id
		WHERE c.user_id = ? AND w.deck_id = ?
		GROUP BY rl.rating
	`, userID, deckID)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	counts := [5]int{} // index by rating 1..4
	for rows.Next() {
		var rating, n int
		if err := rows.Scan(&rating, &n); err != nil {
			return nil, 0, 0, err
		}
		if rating >= 1 && rating <= 4 {
			counts[rating] = n
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}

	out := []barRow{
		{Label: "Again", Count: counts[1]},
		{Label: "Hard", Count: counts[2]},
		{Label: "Good", Count: counts[3]},
		{Label: "Easy", Count: counts[4]},
	}
	scaleByMax(out)

	total := counts[1] + counts[2] + counts[3] + counts[4]
	retention := 0
	if total > 0 {
		retention = (counts[3] + counts[4]) * 100 / total
	}
	return out, total, retention, nil
}

func (s *Server) statsDaily(userID, deckID int64, days int) ([]dayBar, error) {
	rows, err := s.DB.Query(`
		SELECT DATE(rl.reviewed_at) AS d, COUNT(*)
		FROM review_logs rl
		JOIN cards c ON c.id = rl.card_id
		JOIN words w ON w.id = c.word_id
		WHERE c.user_id = ? AND w.deck_id = ?
		  AND rl.reviewed_at >= DATE('now', ?)
		GROUP BY DATE(rl.reviewed_at)
	`, userID, deckID, "-"+strconv.Itoa(days-1)+" days")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	byDate := map[string]int{}
	for rows.Next() {
		var date string
		var n int
		if err := rows.Scan(&date, &n); err != nil {
			return nil, err
		}
		byDate[date] = n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]dayBar, 0, days)
	today := time.Now().UTC()
	max := 0
	for i := days - 1; i >= 0; i-- {
		d := today.AddDate(0, 0, -i).Format("2006-01-02")
		c := byDate[d]
		if c > max {
			max = c
		}
		out = append(out, dayBar{Date: d, Count: c})
	}
	for i := range out {
		if max > 0 {
			out[i].Pct = out[i].Count * 100 / max
		}
	}
	return out, nil
}

func scaleByMax(rows []barRow) {
	max := 0
	for _, r := range rows {
		if r.Count > max {
			max = r.Count
		}
	}
	if max == 0 {
		return
	}
	for i := range rows {
		rows[i].Pct = rows[i].Count * 100 / max
	}
}
