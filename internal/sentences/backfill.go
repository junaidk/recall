package sentences

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const source = "tatoeba"

// Candidate is one possible example sentence for a lemma.
type Candidate struct {
	PairID int64
	DE     string
	EN     string
}

// Backfill picks an example sentence for every word with NULL example_de,
// using FTS5 phrase matching on the lemma against sentence_pairs. Returns
// (matched, missed).
func Backfill(db *sql.DB) (int, int, error) {
	rows, err := db.Query(`SELECT id, lemma FROM words WHERE example_de IS NULL`)
	if err != nil {
		return 0, 0, fmt.Errorf("select words: %w", err)
	}
	type todo struct {
		id    int64
		lemma string
	}
	var todos []todo
	for rows.Next() {
		var t todo
		if err := rows.Scan(&t.id, &t.lemma); err != nil {
			rows.Close()
			return 0, 0, err
		}
		todos = append(todos, t)
	}
	rows.Close()
	if len(todos) == 0 {
		log.Printf("sentences: backfill skipped (all words already have examples)")
		return 0, 0, nil
	}

	tx, err := db.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()

	updateStmt, err := tx.Prepare(
		`UPDATE words SET example_de = ?, example_en = ?, example_source = ?, examples_at = ? WHERE id = ?`,
	)
	if err != nil {
		return 0, 0, err
	}
	defer updateStmt.Close()

	matched, missed := 0, 0
	now := time.Now()
	for _, t := range todos {
		de, en, err := findExample(tx, t.lemma)
		if errors.Is(err, sql.ErrNoRows) {
			missed++
			continue
		}
		if err != nil {
			return matched, missed, fmt.Errorf("lookup %q: %w", t.lemma, err)
		}
		if _, err := updateStmt.Exec(de, en, source, now, t.id); err != nil {
			return matched, missed, fmt.Errorf("update %q: %w", t.lemma, err)
		}
		matched++
	}

	if err := tx.Commit(); err != nil {
		return matched, missed, err
	}
	log.Printf("sentences: backfilled %d of %d words (%d had no match)", matched, len(todos), missed)
	return matched, missed, nil
}

// findExample runs an FTS5 phrase query for the lemma, applies the case
// filter, and returns the shortest matching German sentence and translation.
// Returns sql.ErrNoRows if no case-appropriate hit exists in the top window.
func findExample(tx *sql.Tx, lemma string) (string, string, error) {
	cands, err := queryCandidatesTx(tx, lemma, 50)
	if err != nil {
		return "", "", err
	}
	for _, c := range cands {
		if FilterByCase(lemma, c.DE) {
			return c.DE, c.EN, nil
		}
	}
	return "", "", sql.ErrNoRows
}

// Candidates returns up to limit ranked, case-filtered candidates for the
// lemma. Shortest German sentence first.
func Candidates(db *sql.DB, lemma string, limit int) ([]Candidate, error) {
	rows, err := db.Query(`
		SELECT sp.id, sp.de, sp.en
		FROM sentence_pairs_fts f
		JOIN sentence_pairs sp ON sp.id = f.rowid
		WHERE sentence_pairs_fts MATCH ?
		ORDER BY sp.de_len ASC, sp.id ASC
		LIMIT ?
	`, ftsPhrase(lemma), limit*3) // over-fetch since case filter may drop some
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.PairID, &c.DE, &c.EN); err != nil {
			return nil, err
		}
		if !FilterByCase(lemma, c.DE) {
			continue
		}
		out = append(out, c)
		if len(out) >= limit {
			break
		}
	}
	return out, rows.Err()
}

func queryCandidatesTx(tx *sql.Tx, lemma string, limit int) ([]Candidate, error) {
	rows, err := tx.Query(`
		SELECT sp.id, sp.de, sp.en
		FROM sentence_pairs_fts f
		JOIN sentence_pairs sp ON sp.id = f.rowid
		WHERE sentence_pairs_fts MATCH ?
		ORDER BY sp.de_len ASC, sp.id ASC
		LIMIT ?
	`, ftsPhrase(lemma), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Candidate
	for rows.Next() {
		var c Candidate
		if err := rows.Scan(&c.PairID, &c.DE, &c.EN); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// SwapToNext moves the word's example to the next candidate in rank order
// (current → next-shortest, wrapping at end). Returns the new example.
func SwapToNext(db *sql.DB, wordID int64) (Candidate, error) {
	var lemma, currentDE string
	var currentDENull sql.NullString
	if err := db.QueryRow(`SELECT lemma, example_de FROM words WHERE id = ?`, wordID).
		Scan(&lemma, &currentDENull); err != nil {
		return Candidate{}, err
	}
	currentDE = currentDENull.String

	cands, err := Candidates(db, lemma, 30)
	if err != nil {
		return Candidate{}, err
	}
	if len(cands) == 0 {
		return Candidate{}, sql.ErrNoRows
	}

	nextIdx := 0
	for i, c := range cands {
		if c.DE == currentDE {
			nextIdx = (i + 1) % len(cands)
			break
		}
	}
	next := cands[nextIdx]
	if _, err := db.Exec(
		`UPDATE words SET example_de = ?, example_en = ?, example_source = ?, examples_at = ? WHERE id = ?`,
		next.DE, next.EN, source, time.Now(), wordID,
	); err != nil {
		return Candidate{}, err
	}
	return next, nil
}

// SwapToPairID sets the word's example to a specific sentence_pair, after
// re-verifying the pair text passes the case filter for the lemma.
func SwapToPairID(db *sql.DB, wordID, pairID int64) (Candidate, error) {
	var lemma string
	if err := db.QueryRow(`SELECT lemma FROM words WHERE id = ?`, wordID).Scan(&lemma); err != nil {
		return Candidate{}, err
	}
	var c Candidate
	c.PairID = pairID
	if err := db.QueryRow(`SELECT de, en FROM sentence_pairs WHERE id = ?`, pairID).
		Scan(&c.DE, &c.EN); err != nil {
		return Candidate{}, err
	}
	if !FilterByCase(lemma, c.DE) {
		return Candidate{}, fmt.Errorf("pair %d does not match lemma %q case", pairID, lemma)
	}
	if _, err := db.Exec(
		`UPDATE words SET example_de = ?, example_en = ?, example_source = ?, examples_at = ? WHERE id = ?`,
		c.DE, c.EN, source, time.Now(), wordID,
	); err != nil {
		return Candidate{}, err
	}
	return c, nil
}

// FilterByCase returns true if the sentence is an acceptable example for the
// lemma. Capitalized lemmas (German nouns) must appear in the sentence with
// that exact case as a whole word; lowercase lemmas accept any case (so
// sentence-initial capitalization still works).
//
//	"Schloss" requires "Schloss" (rejects "Ich schloss die Tür.")
//	"schließen" accepts any case
func FilterByCase(lemma, sentence string) bool {
	r, _ := utf8.DecodeRuneInString(lemma)
	if !unicode.IsUpper(r) {
		return true
	}
	return caseFilterRegex(lemma).MatchString(sentence)
}

var caseFilterCache sync.Map // map[string]*regexp.Regexp

func caseFilterRegex(lemma string) *regexp.Regexp {
	if v, ok := caseFilterCache.Load(lemma); ok {
		return v.(*regexp.Regexp)
	}
	// Whole-word match using Unicode letter class so ä/ö/ü/ß are letters.
	re := regexp.MustCompile(`(^|[^\pL])` + regexp.QuoteMeta(lemma) + `($|[^\pL])`)
	caseFilterCache.Store(lemma, re)
	return re
}

// ftsPhrase wraps the lemma in double quotes and escapes embedded quotes,
// turning multi-word lemmas like "auf jeden Fall" into a phrase query.
func ftsPhrase(lemma string) string {
	return `"` + strings.ReplaceAll(lemma, `"`, `""`) + `"`
}
