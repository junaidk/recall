package handlers

import (
	crand "crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/models"
)

// Self-paced test/exam mode. Two independent per-deck quizzes — noun articles
// (type der/die/das) and verb conjugation (fill in a single form) — that are
// fully separate from the FSRS review workflow: they never read or write
// cards/review_logs and have no scheduling side effects. Only the final score
// of a completed session is persisted (exam_results).

// examKindArticle / examKindConjugation are the two quiz kinds, stored verbatim
// in exam_results.kind.
const (
	examKindArticle     = "article"
	examKindConjugation = "conjugation"
	examKindTranslation = "translation"
)

// scope selects which words a test draws from: scopeAll uses the whole deck,
// scopeSeen only cards the user has reviewed at least once (FSRS state != 0).
const (
	scopeAll  = "all"
	scopeSeen = "seen"
)

// examQuestion is one prompt the user must answer. The answer input is rendered
// inline between Before and After (so a noun question reads "[input] Abend" and
// a verb question reads "gehen — Präsens — wir [input]"). Answer is the expected
// response, compared with normalizeAnswer.
type examQuestion struct {
	Before      string // text shown before the answer blank
	After       string // text shown after the answer blank
	Hint        string // optional supporting text (e.g. the Perfekt auxiliary)
	Translation string // English translation, shown as context
	Answer      string // the answer revealed in feedback (and graded if Accept is empty)
	// Accept lists every acceptable response; empty means just Answer. Used so a
	// noun's answer can be shown with its article ("der Abend") while the bare
	// lemma ("Abend") still grades as correct.
	Accept []string
}

// isCorrect reports whether the user's response matches any acceptable answer,
// using normalizeAnswer (case-insensitive, whitespace-collapsed).
func (q examQuestion) isCorrect(user string) bool {
	want := q.Accept
	if len(want) == 0 {
		want = []string{q.Answer}
	}
	nu := normalizeAnswer(user)
	for _, a := range want {
		if normalizeAnswer(a) == nu {
			return true
		}
	}
	return false
}

// examSession holds the transient progress of one in-flight exam. It lives only
// in the in-memory store; nothing here is persisted until the session finishes.
type examSession struct {
	UserID    int64
	DeckID    int64
	Kind      string
	Scope     string // scopeAll or scopeSeen, carried so the done screen can retry
	Count     int    // requested count, carried so the done screen can offer a retry
	Questions []examQuestion
	Index     int // index of the next unanswered question
	Correct   int
	Created   time.Time
}

// examStore is a tiny mutex-guarded registry of in-flight sessions keyed by a
// random id. Abandoned sessions are pruned lazily on creation; a process
// restart clears everything, which is fine for this transient state.
type examStore struct {
	mu       sync.Mutex
	sessions map[string]*examSession
}

func newExamStore() *examStore {
	return &examStore{sessions: map[string]*examSession{}}
}

func (st *examStore) create(sess *examSession) string {
	var b [16]byte
	_, _ = crand.Read(b[:])
	id := hex.EncodeToString(b[:])
	st.mu.Lock()
	defer st.mu.Unlock()
	if len(st.sessions) > 256 {
		cutoff := time.Now().Add(-2 * time.Hour)
		for k, s := range st.sessions {
			if s.Created.Before(cutoff) {
				delete(st.sessions, k)
			}
		}
	}
	st.sessions[id] = sess
	return id
}

func (st *examStore) get(id string) *examSession {
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.sessions[id]
}

func (st *examStore) delete(id string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	delete(st.sessions, id)
}

// --- view models ---

type examPage struct {
	User    *models.User
	Deck    models.Deck
	History []examHistoryRow
}

type examHistoryRow struct {
	KindLabel string
	Correct   int
	Total     int
	Percent   int
	When      string
}

type examQuestionView struct {
	ExamID      string
	DeckID      int64
	Index       int // 1-based, for display
	Total       int
	Before      string
	After       string
	Hint        string
	Translation string
	Placeholder string
}

// examFeedbackView shows the just-answered question with the result revealed and
// a Next button; the session does not advance to the next question until the
// user clicks Next (or presses space).
type examFeedbackView struct {
	ExamID      string
	Index       int // 1-based, of the answered question
	Total       int
	Before      string
	After       string
	Translation string
	Answer      string
	Your        string
	Correct     bool
	Last        bool // the answered question was the final one
}

type examDoneView struct {
	DeckID    int64
	Kind      string // raw kind, for the retry form
	Scope     string // raw scope, for the retry form
	KindLabel string
	Count     int
	Correct   int
	Total     int
	Percent   int
	Empty     bool // no eligible words to test
}

// kindLabel maps a stored kind to its human label.
func kindLabel(kind string) string {
	switch kind {
	case examKindArticle:
		return "Articles"
	case examKindConjugation:
		return "Verb conjugation"
	case examKindTranslation:
		return "Translation"
	default:
		return kind
	}
}

// normalizeAnswer lowercases, trims, and collapses internal whitespace so that
// "  Der " matches "der" and "ist  gegangen" matches "ist gegangen".
func normalizeAnswer(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

func pct(correct, total int) int {
	if total <= 0 {
		return 0
	}
	return correct * 100 / total
}

// handleExamPage renders the exam setup page (kind + count picker) plus this
// user's recent score history for the deck.
func (s *Server) handleExamPage(w http.ResponseWriter, r *http.Request) {
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

	rows, err := s.DB.Query(`
		SELECT kind, total, correct, created_at FROM exam_results
		WHERE user_id = ? AND deck_id = ?
		ORDER BY created_at DESC LIMIT 20`, u.ID, deckID)
	if err != nil {
		http.Error(w, "load history: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var history []examHistoryRow
	for rows.Next() {
		var kind string
		var total, correct int
		var when time.Time
		if err := rows.Scan(&kind, &total, &correct, &when); err != nil {
			http.Error(w, "scan history: "+err.Error(), http.StatusInternalServerError)
			return
		}
		history = append(history, examHistoryRow{
			KindLabel: kindLabel(kind),
			Correct:   correct,
			Total:     total,
			Percent:   pct(correct, total),
			When:      when.Local().Format("2006-01-02 15:04"),
		})
	}

	s.Templates.RenderPage(w, "exam.html", examPage{User: u, Deck: d, History: history})
}

// handleExamStart builds the question set for the chosen kind/count, creates an
// in-memory session, and returns the first question fragment.
func (s *Server) handleExamStart(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	deckID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad deck id", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form: "+err.Error(), http.StatusBadRequest)
		return
	}
	kind := r.FormValue("kind")
	if kind != examKindArticle && kind != examKindConjugation && kind != examKindTranslation {
		http.Error(w, "bad kind", http.StatusBadRequest)
		return
	}
	scope := scopeAll
	if r.FormValue("scope") == scopeSeen {
		scope = scopeSeen
	}
	count := parseCount(r.FormValue("count"))

	var questions []examQuestion
	switch kind {
	case examKindArticle:
		questions, err = s.buildArticleQuestions(deckID, u.ID, scope, count)
	case examKindConjugation:
		questions, err = s.buildConjugationQuestions(deckID, u.ID, scope, count)
	case examKindTranslation:
		questions, err = s.buildTranslationQuestions(deckID, u.ID, scope, count)
	}
	if err != nil {
		http.Error(w, "build questions: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if len(questions) == 0 {
		s.Templates.RenderPartial(w, "exam_done", examDoneView{
			DeckID: deckID, Kind: kind, Scope: scope, KindLabel: kindLabel(kind), Count: count, Empty: true,
		})
		return
	}

	sess := &examSession{
		UserID: u.ID, DeckID: deckID, Kind: kind, Scope: scope, Count: count,
		Questions: questions, Created: time.Now(),
	}
	id := s.Exams.create(sess)

	s.Templates.RenderPartial(w, "exam_question", s.questionView(id, sess, 0))
}

// questionView builds the view for the question at index i (0-based).
func (s *Server) questionView(examID string, sess *examSession, i int) examQuestionView {
	q := sess.Questions[i]
	return examQuestionView{
		ExamID:      examID,
		DeckID:      sess.DeckID,
		Index:       i + 1,
		Total:       len(sess.Questions),
		Before:      q.Before,
		After:       q.After,
		Hint:        q.Hint,
		Translation: q.Translation,
		Placeholder: placeholderFor(sess.Kind),
	}
}

// handleExamAnswer grades the current question and returns the feedback view
// (result revealed + Next button). The session does not advance to the next
// question until handleExamNext.
func (s *Server) handleExamAnswer(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	examID := r.PathValue("examID")
	sess := s.Exams.get(examID)
	if sess == nil || sess.UserID != u.ID {
		http.Error(w, "exam session not found", http.StatusNotFound)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form: "+err.Error(), http.StatusBadRequest)
		return
	}
	if sess.Index >= len(sess.Questions) {
		http.Error(w, "exam already finished", http.StatusBadRequest)
		return
	}

	q := sess.Questions[sess.Index]
	answer := r.FormValue("answer")
	correct := q.isCorrect(answer)
	if correct {
		sess.Correct++
	}
	answered := sess.Index
	sess.Index++

	s.Templates.RenderPartial(w, "exam_feedback", examFeedbackView{
		ExamID:      examID,
		Index:       answered + 1,
		Total:       len(sess.Questions),
		Before:      q.Before,
		After:       q.After,
		Translation: q.Translation,
		Answer:      q.Answer,
		Your:        strings.TrimSpace(answer),
		Correct:     correct,
		Last:        sess.Index >= len(sess.Questions),
	})
}

// handleExamNext serves the next question, or finalizes the session (persisting
// the score) and serves the done screen when the questions are exhausted.
func (s *Server) handleExamNext(w http.ResponseWriter, r *http.Request) {
	u := auth.CurrentUser(r)
	examID := r.PathValue("examID")
	sess := s.Exams.get(examID)
	if sess == nil || sess.UserID != u.ID {
		http.Error(w, "exam session not found", http.StatusNotFound)
		return
	}

	if sess.Index < len(sess.Questions) {
		s.Templates.RenderPartial(w, "exam_question", s.questionView(examID, sess, sess.Index))
		return
	}

	// Session finished: persist the score and drop the in-memory session.
	if _, err := s.DB.Exec(`
		INSERT INTO exam_results (user_id, deck_id, kind, total, correct)
		VALUES (?, ?, ?, ?, ?)`,
		sess.UserID, sess.DeckID, sess.Kind, len(sess.Questions), sess.Correct); err != nil {
		http.Error(w, "save result: "+err.Error(), http.StatusInternalServerError)
		return
	}
	s.Exams.delete(examID)

	s.Templates.RenderPartial(w, "exam_done", examDoneView{
		DeckID:    sess.DeckID,
		Kind:      sess.Kind,
		Scope:     sess.Scope,
		KindLabel: kindLabel(sess.Kind),
		Count:     sess.Count,
		Correct:   sess.Correct,
		Total:     len(sess.Questions),
		Percent:   pct(sess.Correct, len(sess.Questions)),
	})
}

// parseCount turns the count form value into a question cap. "all" or any
// non-positive/unparseable value means "every eligible word".
func parseCount(s string) int {
	if s == "all" || s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

// limitFor maps a count cap to a SQL LIMIT value (0 == all → a high ceiling).
func limitFor(count int) int {
	if count <= 0 {
		return 1 << 30
	}
	return count
}

// scopeJoin returns the JOIN/WHERE fragments (and the user-id arg) that restrict a
// question query to "seen" cards (FSRS state != 0). For scopeAll it returns empty
// fragments. The words table must be aliased "w" in the caller's query, and the
// returned args (if any) precede the query's other placeholders.
func scopeJoin(scope string, userID int64) (join, where string, args []any) {
	if scope == scopeSeen {
		return " JOIN cards c ON c.word_id = w.id AND c.user_id = ?", " AND c.state != 0", []any{userID}
	}
	return "", "", nil
}

func placeholderFor(kind string) string {
	switch kind {
	case examKindArticle:
		return "der/die/das"
	case examKindTranslation:
		return "German word"
	default:
		return "answer"
	}
}

// buildArticleQuestions samples nouns with a clear nominative article and asks
// the user to supply der/die/das. Plural-only and article-less nouns are
// excluded by the query, and any noun whose first article is not der/die/das
// is skipped by the parse guard below.
func (s *Server) buildArticleQuestions(deckID, userID int64, scope string, count int) ([]examQuestion, error) {
	join, where, args := scopeJoin(scope, userID)
	q := `SELECT w.lemma, w.articles, w.translation_en FROM words w` + join + `
		WHERE w.deck_id = ? AND w.pos = 'Substantiv' AND w.only_plural = 0
		  AND w.articles IS NOT NULL AND w.articles NOT IN ('', '[]')` + where + `
		ORDER BY RANDOM() LIMIT ?`
	args = append(args, deckID, limitFor(count))
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var qs []examQuestion
	for rows.Next() {
		var lemma, articlesJSON string
		var translation sql.NullString
		if err := rows.Scan(&lemma, &articlesJSON, &translation); err != nil {
			return nil, err
		}
		article := firstArticle(articlesJSON)
		if _, ok := articleByNominative[article]; !ok {
			continue // not a der/die/das article (e.g. plural "die" only, or junk)
		}
		qs = append(qs, examQuestion{
			After:       lemma, // renders "[input] Abend"
			Translation: translation.String,
			Answer:      article,
		})
	}
	return qs, rows.Err()
}

// firstArticle returns the first article from a words.articles JSON array, or
// "" if the payload is empty/invalid.
func firstArticle(articlesJSON string) string {
	var articles []string
	if articlesJSON != "" {
		_ = json.Unmarshal([]byte(articlesJSON), &articles)
	}
	if len(articles) == 0 {
		return ""
	}
	return articles[0]
}

// buildConjugationQuestions samples verbs and, for each, picks one random target
// form to fill in. Präsens/Präteritum questions ask for a specific pronoun's
// form; Perfekt questions ask for the Partizip II (with the auxiliary as a hint).
func (s *Server) buildConjugationQuestions(deckID, userID int64, scope string, count int) ([]examQuestion, error) {
	join, where, args := scopeJoin(scope, userID)
	q := `SELECT w.lemma, w.conjugations, w.translation_en FROM words w` + join + `
		WHERE w.deck_id = ? AND w.pos = 'Verb'
		  AND w.conjugations IS NOT NULL AND w.conjugations NOT IN ('', '{}')` + where + `
		ORDER BY RANDOM() LIMIT ?`
	args = append(args, deckID, limitFor(count))
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var qs []examQuestion
	for rows.Next() {
		var lemma, raw string
		var translation sql.NullString
		if err := rows.Scan(&lemma, &raw, &translation); err != nil {
			return nil, err
		}
		if q, ok := conjugationQuestion(lemma, raw); ok {
			q.Translation = translation.String
			qs = append(qs, q)
		}
	}
	return qs, rows.Err()
}

// buildTranslationQuestions samples words that have an English translation and asks
// the user to supply the German lemma. The English translation is the prompt (line 1,
// via Before) with the part of speech (pos, e.g. "Substantiv"/"Verb") as a hint; the
// input is on line 2. For nouns with a clear article the revealed answer includes it
// ("der Abend"), but the bare lemma is also accepted. Matching is case-insensitive via
// normalizeAnswer.
func (s *Server) buildTranslationQuestions(deckID, userID int64, scope string, count int) ([]examQuestion, error) {
	join, where, args := scopeJoin(scope, userID)
	q := `SELECT w.lemma, w.pos, w.articles, w.translation_en FROM words w` + join + `
		WHERE w.deck_id = ? AND w.translation_en IS NOT NULL AND w.translation_en != ''` + where + `
		ORDER BY RANDOM() LIMIT ?`
	args = append(args, deckID, limitFor(count))
	rows, err := s.DB.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var qs []examQuestion
	for rows.Next() {
		var lemma string
		var pos, articles, translation sql.NullString
		if err := rows.Scan(&lemma, &pos, &articles, &translation); err != nil {
			return nil, err
		}
		eq := examQuestion{
			Before: translation.String, // English prompt on line 1, input on line 2
			Hint:   pos.String,          // part of speech, e.g. "Substantiv" / "Verb"
			Answer: lemma,
		}
		// Nouns: show the article with the answer; still accept the bare lemma.
		if pos.String == "Substantiv" {
			if art := firstArticle(articles.String); articleByNominative[art] != "" {
				withArticle := art + " " + lemma
				eq.Answer = withArticle
				eq.Accept = []string{withArticle, lemma}
			}
		}
		qs = append(qs, eq)
	}
	return qs, rows.Err()
}

// conjugationCandidate is one fillable form discovered in a verb's payload.
type conjugationCandidate struct {
	prompt string
	hint   string
	answer string
}

// conjugationQuestion parses a words.conjugations payload (same shape as
// parseConjugations) and returns one randomly chosen fill-in question, or
// ok=false when the payload yields no usable form.
func conjugationQuestion(lemma, raw string) (examQuestion, bool) {
	var p struct {
		Praesens    map[string]string `json:"praesens"`
		Praeteritum map[string]string `json:"praeteritum"`
		Perfekt     struct {
			Aux       string `json:"aux"`
			Partizip2 string `json:"partizip2"`
		} `json:"perfekt"`
	}
	if err := json.Unmarshal([]byte(raw), &p); err != nil {
		return examQuestion{}, false
	}

	var cands []conjugationCandidate
	addTense := func(label string, forms map[string]string) {
		for _, row := range praesensOrder {
			if form := forms[row.Key]; form != "" {
				cands = append(cands, conjugationCandidate{
					prompt: lemma + " — " + label + " — " + row.Label,
					answer: form,
				})
			}
		}
	}
	addTense("Präsens", p.Praesens)
	addTense("Präteritum", p.Praeteritum)
	if p.Perfekt.Partizip2 != "" {
		hint := ""
		if p.Perfekt.Aux != "" {
			hint = "Hilfsverb: " + p.Perfekt.Aux
		}
		cands = append(cands, conjugationCandidate{
			prompt: lemma + " — Perfekt (Partizip II)",
			hint:   hint,
			answer: p.Perfekt.Partizip2,
		})
	}
	if len(cands) == 0 {
		return examQuestion{}, false
	}

	c := cands[rand.IntN(len(cands))]
	return examQuestion{Before: c.prompt, Hint: c.hint, Answer: c.answer}, true
}
