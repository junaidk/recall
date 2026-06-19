package handlers

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/db"
	"github.com/junaidk/recall/internal/settings"
	"github.com/junaidk/recall/internal/web"
)

func TestNormalizeAnswerGrading(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"der", "der", true},
		{"  Der ", "der", true},
		{"DER", "der", true},
		{"der", "die", false},
		{"ist  gegangen", "ist gegangen", true},
		{" Ist Gegangen ", "ist gegangen", true},
		{"gemacht", "gemach", false},
	}
	for _, c := range cases {
		got := normalizeAnswer(c.a) == normalizeAnswer(c.b)
		if got != c.want {
			t.Errorf("grade(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestFirstArticle(t *testing.T) {
	cases := []struct{ in, want string }{
		{`["der"]`, "der"},
		{`["die","der"]`, "die"},
		{`[]`, ""},
		{"", ""},
		{"not json", ""},
	}
	for _, c := range cases {
		if got := firstArticle(c.in); got != c.want {
			t.Errorf("firstArticle(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestConjugationQuestionPraesens(t *testing.T) {
	// Single available form forces a deterministic pick.
	q, ok := conjugationQuestion("gehen", `{"praesens":{"ich":"gehe"}}`)
	if !ok {
		t.Fatal("expected a question")
	}
	if q.Answer != "gehe" {
		t.Errorf("answer = %q, want gehe", q.Answer)
	}
	if !strings.Contains(q.Before, "gehen") || !strings.Contains(q.Before, "Präsens") || !strings.Contains(q.Before, "ich") {
		t.Errorf("prompt = %q, want it to mention gehen/Präsens/ich", q.Before)
	}
}

func TestConjugationQuestionPerfekt(t *testing.T) {
	q, ok := conjugationQuestion("machen", `{"perfekt":{"aux":"haben","partizip2":"gemacht"}}`)
	if !ok {
		t.Fatal("expected a question")
	}
	if q.Answer != "gemacht" {
		t.Errorf("answer = %q, want gemacht", q.Answer)
	}
	if !strings.Contains(q.Before, "Partizip II") {
		t.Errorf("prompt = %q, want it to mention Partizip II", q.Before)
	}
	if !strings.Contains(q.Hint, "haben") {
		t.Errorf("hint = %q, want it to mention the auxiliary haben", q.Hint)
	}
}

func TestConjugationQuestionEmpty(t *testing.T) {
	if _, ok := conjugationQuestion("x", `{}`); ok {
		t.Error("empty payload should yield no question")
	}
}

func TestBuildArticleQuestions(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	deckID := insertDeck(t, conn, "A1")
	insertWord(t, conn, deckID, "Abend", "Substantiv", `["der"]`, 0, "")
	insertWord(t, conn, deckID, "Leute", "Substantiv", `["die"]`, 1, "")    // plural-only: excluded
	insertWord(t, conn, deckID, "Etwas", "Substantiv", `[]`, 0, "")         // no article: excluded
	insertWord(t, conn, deckID, "gehen", "Verb", `[]`, 0, gehenJSON)        // not a noun: excluded

	s := &Server{DB: conn}
	qs, err := s.buildArticleQuestions(deckID, 0)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(qs) != 1 {
		t.Fatalf("got %d questions, want 1 (only the der-noun)", len(qs))
	}
	if qs[0].Answer != "der" {
		t.Errorf("answer = %q, want der", qs[0].Answer)
	}
	if qs[0].After != "Abend" {
		t.Errorf("After = %q, want Abend (blank renders before the noun)", qs[0].After)
	}
}

func TestExamFlowPersistsResultAndLeavesFSRSUntouched(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	userID := insertUser(t, conn, "tester")
	deckID := insertDeck(t, conn, "A1")
	insertWord(t, conn, deckID, "Abend", "Substantiv", `["der"]`, 0, "")

	store := auth.NewStore(conn)
	s := New(conn, store, web.MustLoadTemplates(), nil, settings.Settings{})
	mux := http.NewServeMux()
	s.Register(mux)
	cookie := sessionCookie(t, store, userID)

	// The setup page renders.
	if page := doGet(t, mux, cookie, "/decks/1/exam"); !strings.Contains(page, "Start test") {
		t.Errorf("exam page missing setup form: %s", page)
	}

	// Start an article test over the whole deck (one eligible noun).
	startForm := url.Values{"kind": {"article"}, "count": {"all"}}
	startResp := doPost(t, mux, cookie, "/decks/1/exam/start", startForm)
	examID := regexp.MustCompile(`/exam/([0-9a-f]+)/answer`).FindStringSubmatch(startResp)
	if examID == nil {
		t.Fatalf("no exam id in start response: %s", startResp)
	}

	// Answer it correctly — feedback is shown on the same question, with the
	// result revealed and a button to continue.
	answerForm := url.Values{"answer": {"der"}}
	fbResp := doPost(t, mux, cookie, "/exam/"+examID[1]+"/answer", answerForm)
	if !strings.Contains(fbResp, "Correct") {
		t.Errorf("feedback response missing verdict: %s", fbResp)
	}
	// Nothing should be persisted until the session is finalized via /next.
	var early int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM exam_results`).Scan(&early); err != nil {
		t.Fatal(err)
	}
	if early != 0 {
		t.Fatalf("exam_results rows = %d before finalize, want 0", early)
	}

	// Advance past the last question to finalize and show the score.
	doneResp := doGet(t, mux, cookie, "/exam/"+examID[1]+"/next")
	if !strings.Contains(doneResp, "1 / 1") {
		t.Errorf("done response missing score 1/1: %s", doneResp)
	}

	// Exactly one result row recorded, with the right values.
	var rows, total, correct int
	var kind string
	if err := conn.QueryRow(`SELECT COUNT(*) FROM exam_results`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("exam_results rows = %d, want 1", rows)
	}
	if err := conn.QueryRow(`SELECT kind, total, correct FROM exam_results`).Scan(&kind, &total, &correct); err != nil {
		t.Fatal(err)
	}
	if kind != "article" || total != 1 || correct != 1 {
		t.Errorf("result = (%s,%d,%d), want (article,1,1)", kind, total, correct)
	}

	// FSRS tables must be completely untouched by the exam flow.
	for _, table := range []string{"cards", "review_logs"} {
		var n int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s has %d rows, want 0 (exam must not touch FSRS)", table, n)
		}
	}
}

// --- test helpers ---

func insertUser(t *testing.T, conn *sql.DB, name string) int64 {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO users (username, password_hash) VALUES (?, 'x')`, name)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertDeck(t *testing.T, conn *sql.DB, name string) int64 {
	t.Helper()
	res, err := conn.Exec(`INSERT INTO decks (name) VALUES (?)`, name)
	if err != nil {
		t.Fatalf("insert deck: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func insertWord(t *testing.T, conn *sql.DB, deckID int64, lemma, pos, articles string, onlyPlural int, conjugations string) int64 {
	t.Helper()
	res, err := conn.Exec(
		`INSERT INTO words (deck_id, lemma, pos, articles, only_plural, conjugations) VALUES (?, ?, ?, ?, ?, ?)`,
		deckID, lemma, pos, articles, onlyPlural, conjugations)
	if err != nil {
		t.Fatalf("insert word: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

// sessionCookie creates a session for userID and returns the cookie that
// RequireUser will accept.
func sessionCookie(t *testing.T, store *auth.Store, userID int64) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	if err := store.Create(rec, userID); err != nil {
		t.Fatalf("create session: %v", err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("no session cookie set")
	}
	return cookies[0]
}

func doGet(t *testing.T, h http.Handler, cookie *http.Cookie, path string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200; body: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}

func doPost(t *testing.T, h http.Handler, cookie *http.Cookie, path string, form url.Values) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(cookie)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST %s = %d, want 200; body: %s", path, rec.Code, rec.Body.String())
	}
	return rec.Body.String()
}
