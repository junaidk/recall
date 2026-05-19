package models

import (
	"database/sql"
	"time"
)

type User struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

type Deck struct {
	ID         int64
	Name       string
	SourcePath sql.NullString
	CreatedAt  time.Time
}

type Word struct {
	ID            int64
	DeckID        int64
	Lemma         string
	Hidx          sql.NullInt64
	Pos           sql.NullString
	Articles      string // JSON-encoded array
	Genera        string // JSON-encoded array
	URL           sql.NullString
	OnlyPlural    bool
	TranslationEN sql.NullString
	TranslatedAt  sql.NullTime
}

type Card struct {
	ID            int64
	UserID        int64
	WordID        int64
	Due           time.Time
	Stability     float64
	Difficulty    float64
	ElapsedDays   uint64
	ScheduledDays uint64
	Reps          uint64
	Lapses        uint64
	State         int
	LastReview    sql.NullTime
}

type DeckProgress struct {
	Deck     Deck
	New      int
	Learning int
	Review   int
	DueNow   int
	Total    int
}
