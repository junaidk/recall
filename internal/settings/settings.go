// Package settings stores per-user FSRS tuning. Each user has one row in the
// user_settings table, seeded from config.yaml at registration and thereafter
// editable from the /settings page, independent of config.yaml.
package settings

import (
	"database/sql"
	"fmt"
)

// Settings are the per-user FSRS knobs. They mirror config.FSRSConfig but are
// resolved (no pointers / "unset" states) and persisted per user.
type Settings struct {
	RequestRetention float64
	MaximumInterval  float64
	EnableFuzz       bool
	NewCardsPerDay   int
}

// Sanitize clamps the fields to valid ranges, applying the same defaults as the
// config loader (see internal/config). It is used both when seeding defaults
// and when saving user-submitted values.
func (s Settings) Sanitize() Settings {
	if s.RequestRetention <= 0 || s.RequestRetention >= 1 {
		s.RequestRetention = 0.9
	}
	if s.MaximumInterval <= 0 {
		s.MaximumInterval = 36500
	}
	if s.NewCardsPerDay < 0 {
		s.NewCardsPerDay = 0
	}
	return s
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Load reads a user's settings. Returns sql.ErrNoRows if the user has no row
// yet (callers fall back to defaults).
func Load(db *sql.DB, userID int64) (Settings, error) {
	var (
		s    Settings
		fuzz int
	)
	err := db.QueryRow(`
		SELECT request_retention, maximum_interval, enable_fuzz, new_cards_per_day
		FROM user_settings WHERE user_id = ?
	`, userID).Scan(&s.RequestRetention, &s.MaximumInterval, &fuzz, &s.NewCardsPerDay)
	if err != nil {
		return Settings{}, err
	}
	s.EnableFuzz = fuzz != 0
	return s, nil
}

// Save updates an existing user's settings row.
func Save(db *sql.DB, userID int64, s Settings) error {
	_, err := db.Exec(`
		UPDATE user_settings SET
		  request_retention = ?, maximum_interval = ?, enable_fuzz = ?, new_cards_per_day = ?
		WHERE user_id = ?
	`, s.RequestRetention, s.MaximumInterval, boolToInt(s.EnableFuzz), s.NewCardsPerDay, userID)
	if err != nil {
		return fmt.Errorf("save settings: %w", err)
	}
	return nil
}

// InsertForUser seeds a row for a newly-registered user from the given defaults.
// Idempotent: a no-op if the user already has a row.
func InsertForUser(db *sql.DB, userID int64, def Settings) error {
	_, err := db.Exec(`
		INSERT OR IGNORE INTO user_settings
		  (user_id, request_retention, maximum_interval, enable_fuzz, new_cards_per_day)
		VALUES (?, ?, ?, ?, ?)
	`, userID, def.RequestRetention, def.MaximumInterval, boolToInt(def.EnableFuzz), def.NewCardsPerDay)
	if err != nil {
		return fmt.Errorf("insert user settings: %w", err)
	}
	return nil
}

// BackfillAll seeds a settings row from def for every user that lacks one.
// Run once at boot so users created before this feature get defaults.
func BackfillAll(db *sql.DB, def Settings) error {
	_, err := db.Exec(`
		INSERT OR IGNORE INTO user_settings
		  (user_id, request_retention, maximum_interval, enable_fuzz, new_cards_per_day)
		SELECT id, ?, ?, ?, ? FROM users
	`, def.RequestRetention, def.MaximumInterval, boolToInt(def.EnableFuzz), def.NewCardsPerDay)
	if err != nil {
		return fmt.Errorf("backfill user settings: %w", err)
	}
	return nil
}
