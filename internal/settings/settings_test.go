package settings_test

import (
	"path/filepath"
	"testing"

	"github.com/junaidk/recall/internal/db"
	"github.com/junaidk/recall/internal/settings"
)

func TestSettingsLifecycle(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer conn.Close()

	// Two pre-existing users (created before any settings rows).
	for _, name := range []string{"alice", "bob"} {
		if _, err := conn.Exec(`INSERT INTO users (username, password_hash) VALUES (?, 'x')`, name); err != nil {
			t.Fatalf("insert user %s: %v", name, err)
		}
	}

	def := settings.Settings{RequestRetention: 0.9, MaximumInterval: 36500, EnableFuzz: true, NewCardsPerDay: 20}

	// BackfillAll seeds every user lacking a row.
	if err := settings.BackfillAll(conn, def); err != nil {
		t.Fatalf("backfill: %v", err)
	}
	got, err := settings.Load(conn, 1)
	if err != nil {
		t.Fatalf("load after backfill: %v", err)
	}
	if got != def {
		t.Fatalf("backfilled = %+v, want %+v", got, def)
	}

	// Save overrides one user; the other is untouched.
	custom := settings.Settings{RequestRetention: 0.8, MaximumInterval: 365, EnableFuzz: false, NewCardsPerDay: 5}
	if err := settings.Save(conn, 1, custom); err != nil {
		t.Fatalf("save: %v", err)
	}
	if got, _ := settings.Load(conn, 1); got != custom {
		t.Fatalf("after save user 1 = %+v, want %+v", got, custom)
	}
	if got, _ := settings.Load(conn, 2); got != def {
		t.Fatalf("user 2 should be unchanged, got %+v", got)
	}

	// A newly-registered user gets defaults via InsertForUser; backfill is idempotent.
	if _, err := conn.Exec(`INSERT INTO users (username, password_hash) VALUES ('carol', 'x')`); err != nil {
		t.Fatalf("insert carol: %v", err)
	}
	if err := settings.InsertForUser(conn, 3, def); err != nil {
		t.Fatalf("insert for user: %v", err)
	}
	if err := settings.BackfillAll(conn, def); err != nil {
		t.Fatalf("backfill 2nd: %v", err)
	}
	if got, _ := settings.Load(conn, 1); got != custom {
		t.Fatalf("idempotent backfill clobbered override: %+v", got)
	}
}

func TestSanitize(t *testing.T) {
	cases := []struct{ in, want settings.Settings }{
		{settings.Settings{RequestRetention: 0, MaximumInterval: 0, NewCardsPerDay: -5},
			settings.Settings{RequestRetention: 0.9, MaximumInterval: 36500, NewCardsPerDay: 0}},
		{settings.Settings{RequestRetention: 1.5, MaximumInterval: -1, NewCardsPerDay: 10},
			settings.Settings{RequestRetention: 0.9, MaximumInterval: 36500, NewCardsPerDay: 10}},
		{settings.Settings{RequestRetention: 0.85, MaximumInterval: 365, EnableFuzz: true, NewCardsPerDay: 7},
			settings.Settings{RequestRetention: 0.85, MaximumInterval: 365, EnableFuzz: true, NewCardsPerDay: 7}},
	}
	for i, c := range cases {
		if got := c.in.Sanitize(); got != c.want {
			t.Errorf("case %d: Sanitize(%+v) = %+v, want %+v", i, c.in, got, c.want)
		}
	}
}
