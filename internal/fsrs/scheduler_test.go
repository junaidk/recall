package fsrs

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	fsrslib "github.com/open-spaced-repetition/go-fsrs/v3"

	"github.com/junaidk/recall/internal/models"
)

func newTestScheduler() *Scheduler {
	return New(Options{RequestRetention: 0.9, MaximumInterval: 36500, EnableFuzz: false})
}

func newCard(now time.Time) models.Card {
	return models.Card{ID: 1, Due: now, State: 0}
}

func TestRatingFromInt(t *testing.T) {
	cases := map[int]fsrslib.Rating{
		1: fsrslib.Again, 2: fsrslib.Hard, 3: fsrslib.Good, 4: fsrslib.Easy,
		0: 0, 5: 0, -1: 0,
	}
	for in, want := range cases {
		if got := RatingFromInt(in); got != want {
			t.Errorf("RatingFromInt(%d) = %v, want %v", in, got, want)
		}
	}
}

func TestGradeStateTransitions(t *testing.T) {
	s := newTestScheduler()
	now := time.Now()

	// New + Good enters the learning steps.
	c, _ := s.Grade(newCard(now), fsrslib.Good, now)
	if c.State != int(fsrslib.Learning) {
		t.Errorf("New+Good: state = %d, want Learning(%d)", c.State, fsrslib.Learning)
	}
	if !c.Due.After(now.UTC().Add(-time.Second)) {
		t.Errorf("New+Good: due %v not after now %v", c.Due, now)
	}

	// New + Easy graduates straight to Review.
	c, _ = s.Grade(newCard(now), fsrslib.Easy, now)
	if c.State != int(fsrslib.Review) {
		t.Errorf("New+Easy: state = %d, want Review(%d)", c.State, fsrslib.Review)
	}

	// Review + Again lapses into Relearning and increments lapses.
	review := c
	review.Due = now
	c, _ = s.Grade(review, fsrslib.Again, now.Add(48*time.Hour))
	if c.State != int(fsrslib.Relearning) {
		t.Errorf("Review+Again: state = %d, want Relearning(%d)", c.State, fsrslib.Relearning)
	}
	if c.Lapses != review.Lapses+1 {
		t.Errorf("Review+Again: lapses = %d, want %d", c.Lapses, review.Lapses+1)
	}
}

func TestPreviewMonotonicIntervals(t *testing.T) {
	s := newTestScheduler()
	now := time.Now()

	// Build a mature Review-state card first.
	c, _ := s.Grade(newCard(now), fsrslib.Easy, now)
	p := s.Preview(c, c.Due)

	if p.Again >= p.Hard {
		t.Errorf("Again (%v) should be shorter than Hard (%v)", p.Again, p.Hard)
	}
	if p.Hard > p.Good {
		t.Errorf("Hard (%v) should not exceed Good (%v)", p.Hard, p.Good)
	}
	if p.Good > p.Easy {
		t.Errorf("Good (%v) should not exceed Easy (%v)", p.Good, p.Easy)
	}
}

func TestGradeNormalizesToUTC(t *testing.T) {
	s := newTestScheduler()
	karachi := time.FixedZone("PKT", 5*3600)
	now := time.Now().In(karachi)

	c, log := s.Grade(newCard(now), fsrslib.Good, now)

	if c.Due.Location() != time.UTC {
		t.Errorf("due location = %v, want UTC", c.Due.Location())
	}
	if c.LastReview.Valid && c.LastReview.Time.Location() != time.UTC {
		t.Errorf("last_review location = %v, want UTC", c.LastReview.Time.Location())
	}
	if log.Review.Location() != time.UTC {
		t.Errorf("review log time location = %v, want UTC", log.Review.Location())
	}
}

// TestDueComparesAgainstCurrentTimestamp reproduces the timezone bug: cards.due
// is compared to SQLite's CURRENT_TIMESTAMP (a UTC string) with plain string
// ordering, so a due stored with a non-UTC offset surfaces hours early or
// late. Grading with a +05:00 wall clock must still produce a due that the
// scheduler query sees on time.
func TestDueComparesAgainstCurrentTimestamp(t *testing.T) {
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE cards (id INTEGER PRIMARY KEY, due DATETIME NOT NULL)`); err != nil {
		t.Fatal(err)
	}

	s := newTestScheduler()
	karachi := time.FixedZone("PKT", 5*3600)
	now := time.Now().In(karachi)

	// Again on a new card schedules the shortest step (~1 minute out).
	c, _ := s.Grade(newCard(now), fsrslib.Again, now)
	if _, err := db.Exec(`INSERT INTO cards (id, due) VALUES (1, ?)`, c.Due); err != nil {
		t.Fatal(err)
	}

	// The card must be visible to the due-card query within the hour. Before
	// the UTC fix, the +05:00 offset in the stored string pushed it 5 hours
	// into the future.
	var n int
	err = db.QueryRow(
		`SELECT COUNT(*) FROM cards WHERE due <= datetime(CURRENT_TIMESTAMP, '+60 minutes')`,
	).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("card graded at %v (due %v) not visible to due <= CURRENT_TIMESTAMP comparison", now, c.Due)
	}
}
