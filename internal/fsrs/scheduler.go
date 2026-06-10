package fsrs

import (
	"database/sql"
	"time"

	fsrslib "github.com/open-spaced-repetition/go-fsrs/v3"

	"github.com/junaidk/recall/internal/models"
)

type Scheduler struct {
	f *fsrslib.FSRS
}

// Options are the tunable FSRS knobs we surface through config.
// Zero values mean "use library defaults" — resolved by Load() in the config package.
type Options struct {
	RequestRetention float64
	MaximumInterval  float64
	EnableFuzz       bool
}

func New(opts Options) *Scheduler {
	p := fsrslib.DefaultParam()
	if opts.RequestRetention > 0 {
		p.RequestRetention = opts.RequestRetention
	}
	if opts.MaximumInterval > 0 {
		p.MaximumInterval = opts.MaximumInterval
	}
	p.EnableFuzz = opts.EnableFuzz
	return &Scheduler{f: fsrslib.NewFSRS(p)}
}

// Grade applies the rating to the card and returns the updated card and the
// review log entry to persist.
//
// All times are normalized to UTC before they reach the library. The DB
// compares cards.due to SQLite's CURRENT_TIMESTAMP (a UTC string) with plain
// string ordering, so a due written with a non-UTC offset would surface hours
// early or late depending on the server timezone.
func (s *Scheduler) Grade(c models.Card, rating fsrslib.Rating, now time.Time) (models.Card, fsrslib.ReviewLog) {
	lib := toLib(c)
	log := s.f.Repeat(lib, now.UTC())
	info := log[rating]
	rl := info.ReviewLog
	rl.Review = rl.Review.UTC()
	return fromLib(c, info.Card), rl
}

// Previews holds, per rating, the interval from now until the card would
// next be due — used to label the grade buttons before the user picks.
type Previews struct {
	Again, Hard, Good, Easy time.Duration
}

// Preview computes the four candidate intervals without mutating anything.
func (s *Scheduler) Preview(c models.Card, now time.Time) Previews {
	now = now.UTC()
	log := s.f.Repeat(toLib(c), now)
	return Previews{
		Again: log[fsrslib.Again].Card.Due.Sub(now),
		Hard:  log[fsrslib.Hard].Card.Due.Sub(now),
		Good:  log[fsrslib.Good].Card.Due.Sub(now),
		Easy:  log[fsrslib.Easy].Card.Due.Sub(now),
	}
}

func toLib(c models.Card) fsrslib.Card {
	out := fsrslib.Card{
		Due:           c.Due,
		Stability:     c.Stability,
		Difficulty:    c.Difficulty,
		ElapsedDays:   c.ElapsedDays,
		ScheduledDays: c.ScheduledDays,
		Reps:          c.Reps,
		Lapses:        c.Lapses,
		State:         fsrslib.State(c.State),
	}
	if c.LastReview.Valid {
		out.LastReview = c.LastReview.Time
	}
	return out
}

func fromLib(orig models.Card, lc fsrslib.Card) models.Card {
	out := orig
	out.Due = lc.Due.UTC()
	out.Stability = lc.Stability
	out.Difficulty = lc.Difficulty
	out.ElapsedDays = lc.ElapsedDays
	out.ScheduledDays = lc.ScheduledDays
	out.Reps = lc.Reps
	out.Lapses = lc.Lapses
	out.State = int(lc.State)
	if !lc.LastReview.IsZero() {
		out.LastReview = sql.NullTime{Time: lc.LastReview.UTC(), Valid: true}
	}
	return out
}

// RatingFromInt maps form value 1..4 to fsrs Rating, or 0 if invalid.
func RatingFromInt(n int) fsrslib.Rating {
	switch n {
	case 1:
		return fsrslib.Again
	case 2:
		return fsrslib.Hard
	case 3:
		return fsrslib.Good
	case 4:
		return fsrslib.Easy
	}
	return 0
}
