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
	Forecast     []dayBar
	TotalReviews int
	RetentionPct int
	HasReviews   bool
	HasForecast  bool
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

	forecast, err := s.statsForecast(u.ID, deckID, 14)
	if err != nil {
		http.Error(w, "forecast: "+err.Error(), http.StatusInternalServerError)
		return
	}
	hasForecast := false
	for _, f := range forecast {
		if f.Count > 0 {
			hasForecast = true
			break
		}
	}

	s.Templates.RenderPage(w, "stats.html", statsPage{
		User:         u,
		Deck:         d,
		StateCounts:  stateCounts,
		GradeCounts:  gradeCounts,
		DailyReviews: daily,
		Forecast:     forecast,
		TotalReviews: totalReviews,
		RetentionPct: retention,
		HasReviews:   totalReviews > 0,
		HasForecast:  hasForecast,
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
		SELECT rl.rating, rl.state, COUNT(*)
		FROM review_logs rl
		JOIN cards c ON c.id = rl.card_id
		JOIN words w ON w.id = c.word_id
		WHERE c.user_id = ? AND w.deck_id = ?
		GROUP BY rl.rating, rl.state
	`, userID, deckID)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()

	counts := [5]int{}       // all reviews, index by rating 1..4 — grade bars
	reviewCounts := [5]int{} // reviews of Review-state cards only — retention
	for rows.Next() {
		var rating, state, n int
		if err := rows.Scan(&rating, &state, &n); err != nil {
			return nil, 0, 0, err
		}
		if rating >= 1 && rating <= 4 {
			counts[rating] += n
			if state == 2 {
				reviewCounts[rating] += n
			}
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
	// True retention: of cards in the Review state (where the scheduler's
	// retention target applies), how many were recalled. Hard is a pass —
	// the card was remembered, just with effort.
	reviewTotal := reviewCounts[1] + reviewCounts[2] + reviewCounts[3] + reviewCounts[4]
	retention := 0
	if reviewTotal > 0 {
		retention = (reviewCounts[2] + reviewCounts[3] + reviewCounts[4]) * 100 / reviewTotal
	}
	return out, total, retention, nil
}

func (s *Server) statsDaily(userID, deckID int64, days int) ([]dayBar, error) {
	// Bucket by local calendar day, matching the daily new-card cap rollover
	// (the extra '-1 days' on the prefilter absorbs the UTC/local offset).
	rows, err := s.DB.Query(`
		SELECT DATE(rl.reviewed_at, 'localtime') AS d, COUNT(*)
		FROM review_logs rl
		JOIN cards c ON c.id = rl.card_id
		JOIN words w ON w.id = c.word_id
		WHERE c.user_id = ? AND w.deck_id = ?
		  AND rl.reviewed_at >= DATE('now', ?)
		GROUP BY d
	`, userID, deckID, "-"+strconv.Itoa(days)+" days")
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
	today := time.Now()
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

// statsForecast counts cards coming due per local day over the next `days`
// days. Already-overdue cards land in today's bucket; New cards are excluded
// because their introduction is governed by the daily cap, not by due date.
func (s *Server) statsForecast(userID, deckID int64, days int) ([]dayBar, error) {
	rows, err := s.DB.Query(`
		SELECT DATE(c.due, 'localtime') AS d, COUNT(*)
		FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.user_id = ? AND w.deck_id = ? AND c.state != 0
		  AND DATE(c.due, 'localtime') <= DATE('now', 'localtime', ?)
		GROUP BY d
	`, userID, deckID, "+"+strconv.Itoa(days-1)+" days")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	today := time.Now().Format("2006-01-02")
	byDate := map[string]int{}
	for rows.Next() {
		var date string
		var n int
		if err := rows.Scan(&date, &n); err != nil {
			return nil, err
		}
		if date < today {
			date = today // overdue → due today
		}
		byDate[date] += n
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]dayBar, 0, days)
	now := time.Now()
	max := 0
	for i := range days {
		d := now.AddDate(0, 0, i).Format("2006-01-02")
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
