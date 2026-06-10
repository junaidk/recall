package handlers

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/models"
)

// browseRowLimit caps the table size; searches narrow the rest.
const browseRowLimit = 300

var stateLabels = [...]string{"New", "Learning", "Review", "Relearning"}

type browseRow struct {
	CardID      int64 // 0 when the word has no card yet (deck never studied)
	Display     string
	Pos         string
	Translation string
	State       string
	DueText     string
	Reps        uint64
	Lapses      uint64
}

type browsePage struct {
	User      *models.User
	Deck      models.Deck
	Query     string
	Rows      []browseRow
	Total     int // matching words, before the row limit
	Truncated bool
}

func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
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

	query := strings.TrimSpace(r.URL.Query().Get("q"))
	where := `w.deck_id = ?`
	args := []any{u.ID, deckID}
	if query != "" {
		where += ` AND (w.lemma LIKE ? OR w.translation_en LIKE ?)`
		pat := "%" + query + "%"
		args = append(args, pat, pat)
	}

	var total int
	countArgs := args[1:] // the user_id placeholder belongs to the JOIN below
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM words w WHERE `+where, countArgs...).Scan(&total); err != nil {
		http.Error(w, "count: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rows, err := s.DB.Query(`
		SELECT c.id, c.due, c.state, c.reps, c.lapses,
		       w.lemma, w.pos, w.articles, w.translation_en
		FROM words w
		LEFT JOIN cards c ON c.word_id = w.id AND c.user_id = ?
		WHERE `+where+`
		ORDER BY w.id
		LIMIT ?
	`, append(args, browseRowLimit)...)
	if err != nil {
		http.Error(w, "browse: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	page := browsePage{User: u, Deck: d, Query: query, Total: total}
	now := time.Now()
	for rows.Next() {
		var (
			cardID       sql.NullInt64
			due          sql.NullTime
			state        sql.NullInt64
			reps, lapses sql.NullInt64
			lemma        string
			pos          sql.NullString
			articles     sql.NullString
			trans        sql.NullString
		)
		if err := rows.Scan(&cardID, &due, &state, &reps, &lapses, &lemma, &pos, &articles, &trans); err != nil {
			http.Error(w, "scan: "+err.Error(), http.StatusInternalServerError)
			return
		}
		row := browseRow{
			CardID:      cardID.Int64,
			Display:     displayLemma(lemma, articles.String),
			Pos:         pos.String,
			Translation: trans.String,
			Reps:        uint64(reps.Int64),
			Lapses:      uint64(lapses.Int64),
		}
		st := int(state.Int64)
		if !state.Valid {
			st = 0
		}
		if st >= 0 && st < len(stateLabels) {
			row.State = stateLabels[st]
		}
		switch {
		case !due.Valid || st == 0:
			row.DueText = "—"
		case !due.Time.After(now):
			row.DueText = "due now"
		default:
			row.DueText = "in " + formatInterval(due.Time.Sub(now))
		}
		page.Rows = append(page.Rows, row)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, "rows: "+err.Error(), http.StatusInternalServerError)
		return
	}
	page.Truncated = total > len(page.Rows)

	s.Templates.RenderPage(w, "browse.html", page)
}
