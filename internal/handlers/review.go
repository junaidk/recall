package handlers

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/fsrs"
	"github.com/junaidk/recall/internal/models"
	"github.com/junaidk/recall/internal/sentences"
)

type studyPage struct {
	User          *models.User
	Deck          models.Deck
	InitialCardID int64 // when non-zero, load this card's back instead of the next-due front
}

type cardView struct {
	Card         models.Card
	DeckID       int64
	WordID       int64
	Display      string // German with article, e.g. "die Ampel"
	Pos          string
	Translation  string
	URL          string
	AudioURL     string
	ExampleDE    string
	ExampleEN    string
	Conjugations *conjugationView
	Plurals      *declensionView

	// Interval previews shown on the grade buttons ("1m", "3d", …).
	IvlAgain, IvlHard, IvlGood, IvlEasy string

	// Set on the card front served right after a grade; renders the undo
	// button (out-of-band) pointing at the review_logs row to revert.
	UndoLogID int64

	// Session counters rendered out-of-band in the study header. DueReview
	// counts learning/review cards due now; DueNew counts new cards still
	// to be introduced today (both include the card being shown).
	DueReview int
	DueNew    int
	HasCounts bool
}

// doneView feeds the "all caught up" partial.
type doneView struct {
	DeckID      int64
	NextDueText string // humanized time until the next card, "" if none soon
	PollSeconds int    // when >0, the partial auto-refreshes after this delay
	CapReached  bool
	NewWaiting  int // new cards blocked behind the daily cap until tomorrow
	UndoLogID   int64
}

// formatInterval renders a scheduling interval the way SRS users expect:
// minutes under an hour, hours under a day, then days/months/years.
func formatInterval(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	switch {
	case d < time.Minute:
		return "<1m"
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()+0.5))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()+0.5))
	}
	days := d.Hours() / 24
	switch {
	case days < 30:
		return fmt.Sprintf("%dd", int(days+0.5))
	case days < 365:
		return trimTrailingZero(fmt.Sprintf("%.1fmo", days/30.44))
	default:
		return trimTrailingZero(fmt.Sprintf("%.1fy", days/365.25))
	}
}

func trimTrailingZero(s string) string {
	return strings.Replace(s, ".0", "", 1)
}

// conjugationView is the verb conjugation panel rendered below the main card.
// Nil unless the word is a Verb and a non-empty payload was loaded. Both
// tables are rendered the same way — six rows of (pronoun, form).
type conjugationView struct {
	Praesens    []personForm // ich, du, er/sie/es, wir, ihr, sie
	Praeteritum []personForm // same order, simple past, e.g. "lief" / "machte"
	Perfekt     []personForm // same order, e.g. "bin gelaufen" / "habe gemacht"
}

type personForm struct {
	Pronoun string
	Form    string
}

// praesensOrder pins the row order rendered on the card. Tied to the JSON
// keys produced by cmd/build-conjugations.
var praesensOrder = []struct {
	Key, Label string
}{
	{"ich", "ich"},
	{"du", "du"},
	{"er", "er/sie/es"},
	{"wir", "wir"},
	{"ihr", "ihr"},
	{"sie", "sie"},
}

// auxPraesens conjugates the two German Perfekt auxiliaries. These never
// change with the main verb, so we don't ship them per-row in the corpus —
// we just compose <aux_form> + Partizip II at render time.
var auxPraesens = map[string]map[string]string{
	"haben": {"ich": "habe", "du": "hast", "er": "hat", "wir": "haben", "ihr": "habt", "sie": "haben"},
	"sein":  {"ich": "bin", "du": "bist", "er": "ist", "wir": "sind", "ihr": "seid", "sie": "sind"},
}

// declensionView is the noun declension panel rendered below the main card.
// Nil unless the word is a Substantiv (and not plural-only) and a non-empty
// payload was loaded. Rows are pinned in caseOrder; the singular column
// renders SgArticle + SgForm, the plural column renders PlArticle + PlForm.
type declensionView struct {
	Rows []declensionRow
}

type declensionRow struct {
	Case      string // "Nominativ", "Akkusativ", "Dativ", "Genitiv"
	SgArticle string
	SgForm    string
	PlArticle string
	PlForm    string
}

// caseOrder pins the row order rendered on the noun card.
var caseOrder = []struct {
	Key, Label string
}{
	{"nom", "Nominativ"},
	{"akk", "Akkusativ"},
	{"dat", "Dativ"},
	{"gen", "Genitiv"},
}

// singularArticles maps the canonical DWDS genus to the per-case singular
// article. Plural articles are fixed (see pluralArticles).
var singularArticles = map[string]map[string]string{
	"mask.":  {"nom": "der", "akk": "den", "dat": "dem", "gen": "des"},
	"fem.":   {"nom": "die", "akk": "die", "dat": "der", "gen": "der"},
	"neutr.": {"nom": "das", "akk": "das", "dat": "dem", "gen": "des"},
}

var pluralArticles = map[string]string{
	"nom": "die",
	"akk": "die",
	"dat": "den",
	"gen": "der",
}

// articleByNominative maps a nominative-singular article (as stored in
// words.articles) back to its genus, so we can derive the full per-case
// article set even when words.genera is empty or unexpected.
var articleByNominative = map[string]string{
	"der": "mask.",
	"die": "fem.",
	"das": "neutr.",
}

// parsePlurals turns the JSON payload stored in words.plurals into the
// view-ready struct. Returns nil for empty/missing/invalid input — the
// template gate (Plurals != nil) then suppresses the panel. The lemma is
// used as the fallback singular form whenever the payload omits a slot
// (common for feminine nouns where kaikki only lists Nom Sg explicitly).
func parsePlurals(raw, lemma, articlesJSON, generaJSON string) *declensionView {
	if raw == "" || raw == "{}" {
		return nil
	}
	var p struct {
		Sg struct {
			Nom, Akk, Dat, Gen string
		} `json:"sg"`
		Pl struct {
			Nom, Akk, Dat, Gen string
		} `json:"pl"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil
	}
	if p.Pl.Nom == "" && p.Pl.Akk == "" && p.Pl.Dat == "" && p.Pl.Gen == "" {
		return nil
	}

	genus := pickGenus(articlesJSON, generaJSON)
	sgArt := singularArticles[genus]

	sgForms := map[string]string{"nom": p.Sg.Nom, "akk": p.Sg.Akk, "dat": p.Sg.Dat, "gen": p.Sg.Gen}
	plForms := map[string]string{"nom": p.Pl.Nom, "akk": p.Pl.Akk, "dat": p.Pl.Dat, "gen": p.Pl.Gen}
	// Plural cases that share the form of Nominativ in German morphology.
	if plForms["akk"] == "" {
		plForms["akk"] = plForms["nom"]
	}
	if plForms["gen"] == "" {
		plForms["gen"] = plForms["nom"]
	}
	if plForms["dat"] == "" {
		plForms["dat"] = dativPlural(plForms["nom"])
	}

	dv := &declensionView{}
	for _, row := range caseOrder {
		sgForm := sgForms[row.Key]
		if sgForm == "" {
			sgForm = lemma
		}
		plForm := plForms[row.Key]
		sgArticle := ""
		if sgArt != nil {
			sgArticle = sgArt[row.Key]
		}
		dv.Rows = append(dv.Rows, declensionRow{
			Case:      row.Label,
			SgArticle: sgArticle,
			SgForm:    sgForm,
			PlArticle: pluralArticles[row.Key],
			PlForm:    plForm,
		})
	}
	return dv
}

// pickGenus returns the first usable DWDS genus label from genera, falling
// back to the gender implied by the first nominative article. Returns "" if
// neither yields a recognised label — the renderer then leaves the singular
// article blank rather than fabricating one.
func pickGenus(articlesJSON, generaJSON string) string {
	var genera []string
	if generaJSON != "" {
		_ = json.Unmarshal([]byte(generaJSON), &genera)
	}
	for _, g := range genera {
		if _, ok := singularArticles[g]; ok {
			return g
		}
	}
	var articles []string
	if articlesJSON != "" {
		_ = json.Unmarshal([]byte(articlesJSON), &articles)
	}
	for _, a := range articles {
		if g, ok := articleByNominative[a]; ok {
			return g
		}
	}
	return ""
}

// dativPlural applies the German Dat Pl -n suffix rule. The dative plural of
// every noun ends in -n unless the nominative plural already ends in -n or
// -s (loanwords like Autos take no extra n).
func dativPlural(nomPl string) string {
	if nomPl == "" {
		return ""
	}
	if strings.HasSuffix(nomPl, "n") || strings.HasSuffix(nomPl, "s") {
		return nomPl
	}
	return nomPl + "n"
}

// parseConjugations turns the JSON payload stored in words.conjugations into
// the view-ready struct. Returns nil for empty/missing/invalid input — the
// template gate (Conjugations != nil) then suppresses the panel.
func parseConjugations(raw string) *conjugationView {
	if raw == "" || raw == "{}" {
		return nil
	}
	var p struct {
		Praesens    map[string]string `json:"praesens"`
		Praeteritum map[string]string `json:"praeteritum"`
		Perfekt     struct {
			Aux       string `json:"aux"`
			Partizip2 string `json:"partizip2"`
		} `json:"perfekt"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return nil
	}
	if len(p.Praesens) == 0 && len(p.Praeteritum) == 0 && p.Perfekt.Partizip2 == "" {
		return nil
	}
	cv := &conjugationView{}
	for _, row := range praesensOrder {
		if form, ok := p.Praesens[row.Key]; ok && form != "" {
			cv.Praesens = append(cv.Praesens, personForm{Pronoun: row.Label, Form: form})
		}
	}
	for _, row := range praesensOrder {
		if form, ok := p.Praeteritum[row.Key]; ok && form != "" {
			cv.Praeteritum = append(cv.Praeteritum, personForm{Pronoun: row.Label, Form: form})
		}
	}
	if aux, ok := auxPraesens[p.Perfekt.Aux]; ok && p.Perfekt.Partizip2 != "" {
		for _, row := range praesensOrder {
			cv.Perfekt = append(cv.Perfekt, personForm{
				Pronoun: row.Label,
				Form:    aux[row.Key] + " " + p.Perfekt.Partizip2,
			})
		}
	}
	return cv
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
	var initialCardID int64
	if raw := r.URL.Query().Get("card"); raw != "" {
		if id, err := strconv.ParseInt(raw, 10, 64); err == nil && id > 0 {
			initialCardID = id
		}
	}
	s.Templates.RenderPage(w, "review.html", studyPage{User: u, Deck: d, InitialCardID: initialCardID})
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
			s.renderDone(w, u.ID, deckID, 0)
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
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
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
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load card: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sch, err := s.schedulerFor(u.ID)
	if err != nil {
		http.Error(w, "load settings: "+err.Error(), http.StatusInternalServerError)
		return
	}
	updated, log := sch.Grade(card, rating, time.Now())

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
	res, err := tx.Exec(`
		INSERT INTO review_logs (
		  card_id, rating, state, elapsed_days, scheduled_days, reviewed_at,
		  prev_due, prev_stability, prev_difficulty, prev_elapsed_days,
		  prev_scheduled_days, prev_reps, prev_lapses, prev_state, prev_last_review
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`, cardID, int(rating), int(log.State), log.ElapsedDays, log.ScheduledDays, log.Review,
		card.Due.UTC(), card.Stability, card.Difficulty, card.ElapsedDays,
		card.ScheduledDays, card.Reps, card.Lapses, card.State, card.LastReview,
	)
	if err != nil {
		http.Error(w, "insert review log: "+err.Error(), http.StatusInternalServerError)
		return
	}
	logID, err := res.LastInsertId()
	if err != nil {
		http.Error(w, "log id: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "commit: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cv, err := s.fetchNextCardView(u.ID, deckID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.renderDone(w, u.ID, deckID, logID)
			return
		}
		http.Error(w, "fetch next: "+err.Error(), http.StatusInternalServerError)
		return
	}
	cv.UndoLogID = logID
	s.Templates.RenderPartial(w, "card_front", cv)
}

// handleUndo reverts the most recent grade of a card: the card row is
// restored from the snapshot on the review_logs row, the row is deleted, and
// the card's back is served again so the user can re-grade it.
func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	logID, err := strconv.ParseInt(r.PathValue("logID"), 10, 64)
	if err != nil {
		http.Error(w, "bad log id", http.StatusBadRequest)
		return
	}

	var (
		cardID                        int64
		prevDue, prevLastReview       sql.NullTime
		prevStability, prevDifficulty sql.NullFloat64
		prevElapsed, prevScheduled    sql.NullInt64
		prevReps, prevLapses          sql.NullInt64
		prevState                     sql.NullInt64
	)
	// Only the newest log row of a card may be undone — reverting an older
	// one would clobber state from the reviews that came after it.
	err = s.DB.QueryRow(`
		SELECT rl.card_id, rl.prev_due, rl.prev_stability, rl.prev_difficulty,
		       rl.prev_elapsed_days, rl.prev_scheduled_days, rl.prev_reps,
		       rl.prev_lapses, rl.prev_state, rl.prev_last_review
		FROM review_logs rl
		JOIN cards c ON c.id = rl.card_id
		WHERE rl.id = ? AND c.user_id = ?
		  AND rl.id = (SELECT MAX(id) FROM review_logs WHERE card_id = rl.card_id)
	`, logID, u.ID).Scan(
		&cardID, &prevDue, &prevStability, &prevDifficulty,
		&prevElapsed, &prevScheduled, &prevReps,
		&prevLapses, &prevState, &prevLastReview,
	)
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "load log: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !prevDue.Valid {
		// Row predates the snapshot columns (schema v6).
		http.Error(w, "this review cannot be undone", http.StatusConflict)
		return
	}

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
	`, prevDue.Time, prevStability.Float64, prevDifficulty.Float64,
		prevElapsed.Int64, prevScheduled.Int64,
		prevReps.Int64, prevLapses.Int64, prevState.Int64, prevLastReview,
		cardID, u.ID,
	); err != nil {
		http.Error(w, "restore card: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := tx.Exec(`DELETE FROM review_logs WHERE id = ?`, logID); err != nil {
		http.Error(w, "delete log: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tx.Commit(); err != nil {
		http.Error(w, "commit: "+err.Error(), http.StatusInternalServerError)
		return
	}

	cv, err := s.loadCardView(u.ID, cardID)
	if err != nil {
		http.Error(w, "load card: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Templates.RenderPartial(w, "card_back", cv)
	// The grade was reverted, so retire the undo button (out-of-band swap).
	fmt.Fprint(w, `<div id="undo-slot" hx-swap-oob="true"></div>`)
}

// renderDone serves the "all caught up" partial, with next-due countdown and
// daily-cap context so the session doesn't just dead-end.
func (s *Server) renderDone(w http.ResponseWriter, userID, deckID, undoLogID int64) {
	dv, err := s.buildDoneView(userID, deckID)
	if err != nil {
		http.Error(w, "done view: "+err.Error(), http.StatusInternalServerError)
		return
	}
	dv.UndoLogID = undoLogID
	s.Templates.RenderPartial(w, "done", dv)
}

func (s *Server) buildDoneView(userID, deckID int64) (doneView, error) {
	dv := doneView{DeckID: deckID}

	introducedToday, err := s.countNewIntroducedToday(userID, deckID)
	if err != nil {
		return dv, err
	}
	set, err := s.settingsFor(userID)
	if err != nil {
		return dv, err
	}
	if set.NewCardsPerDay-introducedToday <= 0 {
		var waiting int
		err := s.DB.QueryRow(`
			SELECT COUNT(*) FROM cards c
			JOIN words w ON w.id = c.word_id
			WHERE c.user_id = ? AND w.deck_id = ? AND c.state = 0
			  AND c.due <= CURRENT_TIMESTAMP
		`, userID, deckID).Scan(&waiting)
		if err != nil {
			return dv, err
		}
		dv.CapReached = waiting > 0
		dv.NewWaiting = waiting
	}

	// Selecting the column (not MIN(due)) keeps the DATETIME decltype, which
	// the driver needs to parse the value into a time.Time.
	var next time.Time
	err = s.DB.QueryRow(`
		SELECT c.due FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.user_id = ? AND w.deck_id = ? AND c.due > CURRENT_TIMESTAMP
		ORDER BY c.due ASC
		LIMIT 1
	`, userID, deckID).Scan(&next)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return dv, err
	}
	if err == nil {
		until := max(time.Until(next), 0)
		// Only show a countdown (and auto-resume) for cards landing within
		// the hour — typically learning steps. Anything later is tomorrow's
		// problem.
		if until <= 65*time.Minute {
			dv.NextDueText = formatInterval(until)
			dv.PollSeconds = max(int(until.Seconds())+5, 10)
		}
	}
	return dv, nil
}

// --- helpers ---

// countNewIntroducedToday counts cards from this deck that the user graded out
// of the New state today. review_logs.state captures the card's prior state,
// so rows with state=0 are exactly the "first-time" gradings of New cards.
func (s *Server) countNewIntroducedToday(userID, deckID int64) (int, error) {
	var n int
	err := s.DB.QueryRow(`
		SELECT COUNT(*) FROM review_logs rl
		JOIN cards c ON c.id = rl.card_id
		JOIN words w ON w.id = c.word_id
		WHERE c.user_id = ? AND w.deck_id = ?
		  AND rl.state = 0
		  AND date(rl.reviewed_at, 'localtime') = date('now', 'localtime')
	`, userID, deckID).Scan(&n)
	return n, err
}

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
		genera   sql.NullString
		pos      sql.NullString
		url      sql.NullString
		audio    sql.NullString
		trans    sql.NullString
		exDE     sql.NullString
		exEN     sql.NullString
		conj     sql.NullString
		plur     sql.NullString
		lemma    string
	)
	err := s.DB.QueryRow(`
		SELECT c.id, c.user_id, c.word_id, c.due, c.stability, c.difficulty,
		       c.elapsed_days, c.scheduled_days, c.reps, c.lapses, c.state, c.last_review,
		       w.deck_id, w.lemma, w.pos, w.articles, w.genera, w.url, w.audio_url, w.translation_en,
		       w.example_de, w.example_en, w.conjugations, w.plurals
		FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.id = ? AND c.user_id = ?
	`, cardID, userID).Scan(
		&cv.Card.ID, &cv.Card.UserID, &cv.Card.WordID, &cv.Card.Due,
		&cv.Card.Stability, &cv.Card.Difficulty,
		&cv.Card.ElapsedDays, &cv.Card.ScheduledDays,
		&cv.Card.Reps, &cv.Card.Lapses, &cv.Card.State, &cv.Card.LastReview,
		&cv.DeckID, &lemma, &pos, &articles, &genera, &url, &audio, &trans,
		&exDE, &exEN, &conj, &plur,
	)
	if err != nil {
		return cv, err
	}
	cv.WordID = cv.Card.WordID
	cv.Display = displayLemma(lemma, articles.String)
	cv.Pos = pos.String
	if trans.Valid {
		cv.Translation = trans.String
	}
	if url.Valid {
		cv.URL = url.String
	}
	if audio.Valid {
		cv.AudioURL = audio.String
	}
	if exDE.Valid {
		cv.ExampleDE = exDE.String
	}
	if exEN.Valid {
		cv.ExampleEN = exEN.String
	}
	if cv.Pos == "Verb" && conj.Valid {
		cv.Conjugations = parseConjugations(conj.String)
	}
	if cv.Pos == "Substantiv" && plur.Valid {
		cv.Plurals = parsePlurals(plur.String, lemma, articles.String, genera.String)
	}
	sch, err := s.schedulerFor(userID)
	if err != nil {
		return cv, err
	}
	p := sch.Preview(cv.Card, time.Now())
	cv.IvlAgain = formatInterval(p.Again)
	cv.IvlHard = formatInterval(p.Hard)
	cv.IvlGood = formatInterval(p.Good)
	cv.IvlEasy = formatInterval(p.Easy)
	return cv, nil
}

func (s *Server) fetchNextCardView(userID, deckID int64) (cardView, error) {
	var (
		cv       cardView
		articles sql.NullString
		genera   sql.NullString
		pos      sql.NullString
		url      sql.NullString
		audio    sql.NullString
		trans    sql.NullString
		exDE     sql.NullString
		exEN     sql.NullString
		conj     sql.NullString
		plur     sql.NullString
		lemma    string
	)
	cv.DeckID = deckID

	// Pick the next card by weighted random across two tracks (new vs review)
	// so new cards are sprinkled through the session instead of being front-
	// loaded. Once the daily new-card cap is hit, the new track has zero weight
	// and only reviews are served.
	introducedToday, err := s.countNewIntroducedToday(userID, deckID)
	if err != nil {
		return cv, err
	}
	set, err := s.settingsFor(userID)
	if err != nil {
		return cv, err
	}
	remainingNew := max(set.NewCardsPerDay-introducedToday, 0)

	var newDue, reviewDue int
	err = s.DB.QueryRow(`
		SELECT
		  COALESCE(SUM(CASE WHEN c.state = 0 THEN 1 ELSE 0 END), 0),
		  COALESCE(SUM(CASE WHEN c.state != 0 THEN 1 ELSE 0 END), 0)
		FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.user_id = ? AND w.deck_id = ? AND c.due <= CURRENT_TIMESTAMP
	`, userID, deckID).Scan(&newDue, &reviewDue)
	if err != nil {
		return cv, err
	}

	effectiveNew := min(newDue, remainingNew)
	if effectiveNew+reviewDue == 0 {
		return cv, sql.ErrNoRows
	}
	cv.DueReview = reviewDue
	cv.DueNew = effectiveNew
	cv.HasCounts = true
	stateFilter := " AND c.state != 0"
	if rand.IntN(effectiveNew+reviewDue) < effectiveNew {
		stateFilter = " AND c.state = 0"
	}

	err = s.DB.QueryRow(`
		SELECT c.id, c.user_id, c.word_id, c.due, c.stability, c.difficulty,
		       c.elapsed_days, c.scheduled_days, c.reps, c.lapses, c.state, c.last_review,
		       w.lemma, w.pos, w.articles, w.genera, w.url, w.audio_url, w.translation_en,
		       w.example_de, w.example_en, w.conjugations, w.plurals
		FROM cards c
		JOIN words w ON w.id = c.word_id
		WHERE c.user_id = ? AND w.deck_id = ? AND c.due <= CURRENT_TIMESTAMP`+stateFilter+`
		ORDER BY c.due ASC, RANDOM()
		LIMIT 1
	`, userID, deckID).Scan(
		&cv.Card.ID, &cv.Card.UserID, &cv.Card.WordID, &cv.Card.Due,
		&cv.Card.Stability, &cv.Card.Difficulty,
		&cv.Card.ElapsedDays, &cv.Card.ScheduledDays,
		&cv.Card.Reps, &cv.Card.Lapses, &cv.Card.State, &cv.Card.LastReview,
		&lemma, &pos, &articles, &genera, &url, &audio, &trans,
		&exDE, &exEN, &conj, &plur,
	)
	if err != nil {
		return cv, err
	}
	cv.WordID = cv.Card.WordID
	cv.Display = displayLemma(lemma, articles.String)
	cv.Pos = pos.String
	if trans.Valid {
		cv.Translation = trans.String
	}
	if url.Valid {
		cv.URL = url.String
	}
	if audio.Valid {
		cv.AudioURL = audio.String
	}
	if exDE.Valid {
		cv.ExampleDE = exDE.String
	}
	if exEN.Valid {
		cv.ExampleEN = exEN.String
	}
	if cv.Pos == "Verb" && conj.Valid {
		cv.Conjugations = parseConjugations(conj.String)
	}
	if cv.Pos == "Substantiv" && plur.Valid {
		cv.Plurals = parsePlurals(plur.String, lemma, articles.String, genera.String)
	}
	return cv, nil
}

// --- example-swap handlers (card-back ↻ / ⋯) ---

// exampleBlockView is what _example_block.html expects.
type exampleBlockView struct {
	WordID    int64
	ExampleDE string
	ExampleEN string
}

// exampleChoicesView is what _example_choices.html expects.
type exampleChoicesView struct {
	WordID     int64
	Candidates []sentences.Candidate
}

func (s *Server) handleNextExample(w http.ResponseWriter, r *http.Request) {
	wordID, err := strconv.ParseInt(r.PathValue("wordID"), 10, 64)
	if err != nil {
		http.Error(w, "bad word id", http.StatusBadRequest)
		return
	}
	c, err := sentences.SwapToNext(s.DB, wordID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "swap example: "+err.Error(), http.StatusInternalServerError)
		return
	}
	// On ErrNoRows: word has no candidates; re-render whatever is persisted.
	view, err := s.loadExampleBlock(wordID)
	if err != nil {
		http.Error(w, "load example: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = c
	s.Templates.RenderPartial(w, "example_block", view)
}

func (s *Server) handleExampleChoices(w http.ResponseWriter, r *http.Request) {
	wordID, err := strconv.ParseInt(r.PathValue("wordID"), 10, 64)
	if err != nil {
		http.Error(w, "bad word id", http.StatusBadRequest)
		return
	}
	var lemma string
	if err := s.DB.QueryRow(`SELECT lemma FROM words WHERE id = ?`, wordID).Scan(&lemma); err != nil {
		http.Error(w, "load word: "+err.Error(), http.StatusInternalServerError)
		return
	}
	cands, err := sentences.Candidates(s.DB, lemma, 20)
	if err != nil {
		http.Error(w, "candidates: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Templates.RenderPartial(w, "example_choices", exampleChoicesView{WordID: wordID, Candidates: cands})
}

func (s *Server) handleSetExample(w http.ResponseWriter, r *http.Request) {
	wordID, err := strconv.ParseInt(r.PathValue("wordID"), 10, 64)
	if err != nil {
		http.Error(w, "bad word id", http.StatusBadRequest)
		return
	}
	pairID, err := strconv.ParseInt(r.URL.Query().Get("pair"), 10, 64)
	if err != nil || pairID <= 0 {
		http.Error(w, "bad pair id", http.StatusBadRequest)
		return
	}
	if _, err := sentences.SwapToPairID(s.DB, wordID, pairID); err != nil {
		http.Error(w, "set example: "+err.Error(), http.StatusInternalServerError)
		return
	}
	view, err := s.loadExampleBlock(wordID)
	if err != nil {
		http.Error(w, "load example: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Templates.RenderPartial(w, "example_block", view)
}

func (s *Server) handleExampleBlock(w http.ResponseWriter, r *http.Request) {
	wordID, err := strconv.ParseInt(r.PathValue("wordID"), 10, 64)
	if err != nil {
		http.Error(w, "bad word id", http.StatusBadRequest)
		return
	}
	view, err := s.loadExampleBlock(wordID)
	if err != nil {
		http.Error(w, "load example: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Templates.RenderPartial(w, "example_block", view)
}

func (s *Server) loadExampleBlock(wordID int64) (exampleBlockView, error) {
	var de, en sql.NullString
	err := s.DB.QueryRow(`SELECT example_de, example_en FROM words WHERE id = ?`, wordID).Scan(&de, &en)
	if err != nil {
		return exampleBlockView{}, err
	}
	return exampleBlockView{WordID: wordID, ExampleDE: de.String, ExampleEN: en.String}, nil
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
