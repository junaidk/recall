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

func New() *Scheduler {
	return &Scheduler{f: fsrslib.NewFSRS(fsrslib.DefaultParam())}
}

// Grade applies the rating to the card and returns the updated card and the
// review log entry to persist.
func (s *Scheduler) Grade(c models.Card, rating fsrslib.Rating, now time.Time) (models.Card, fsrslib.ReviewLog) {
	lib := toLib(c)
	log := s.f.Repeat(lib, now)
	info := log[rating]
	return fromLib(c, info.Card), info.ReviewLog
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
	out.Due = lc.Due
	out.Stability = lc.Stability
	out.Difficulty = lc.Difficulty
	out.ElapsedDays = lc.ElapsedDays
	out.ScheduledDays = lc.ScheduledDays
	out.Reps = lc.Reps
	out.Lapses = lc.Lapses
	out.State = int(lc.State)
	if !lc.LastReview.IsZero() {
		out.LastReview = sql.NullTime{Time: lc.LastReview, Valid: true}
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
