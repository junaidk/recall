# Spaced repetition (FSRS)

Recall schedules reviews with [FSRS-4.5](https://github.com/open-spaced-repetition/fsrs4anki/wiki) via
[`open-spaced-repetition/go-fsrs/v3`](https://github.com/open-spaced-repetition/go-fsrs). This doc
describes how a word reappears for revision and which knobs are exposed.

## Data model

Each user/word pair gets one row in `cards`. The row carries the full FSRS state:

| Column | Meaning |
|---|---|
| `due` | Timestamp when the card next becomes available for review. |
| `state` | `0=New`, `1=Learning`, `2=Review`, `3=Relearning`. |
| `stability` | Days the memory is expected to last at target retention. |
| `difficulty` | Per-card hardness, 1–10, mean-reverting. |
| `reps` | Total successful reviews. |
| `lapses` | Total times rated **Again** after leaving Learning. |
| `elapsed_days` / `scheduled_days` | Bookkeeping for the last interval. |
| `last_review` | Timestamp of the previous grading. |

Every grading also appends one row to `review_logs` (rating, state, elapsed/scheduled days, time).

## Lifecycle of a word

1. **Seeding.** When a user opens a deck's study page, [internal/handlers/review.go](../internal/handlers/review.go)
   runs
   ```sql
   INSERT OR IGNORE INTO cards (user_id, word_id, due, state)
   SELECT ?, id, CURRENT_TIMESTAMP, 0 FROM words WHERE deck_id = ?
   ```
   Every word in the deck gets a `state=0` (New) card due immediately. Re-opening the deck is a
   no-op for words the user has already seen.

2. **Picking the next card.** The review queue is a single SQL query:
   ```sql
   SELECT ... FROM cards c JOIN words w ON w.id = c.word_id
   WHERE c.user_id = ? AND w.deck_id = ? AND c.due <= CURRENT_TIMESTAMP
     [AND c.state != 0]   -- added when the daily new-card cap is reached
   ORDER BY c.due ASC, RANDOM()
   LIMIT 1
   ```
   Oldest-due first; ties broken randomly. When the query returns zero rows, the session ends
   ("done" partial is rendered). The `state != 0` filter is applied when
   [`new_cards_per_day`](#daily-new-card-cap) has been hit for this user+deck today — see below.

3. **Grading.** The user picks one of four ratings:

   | Button | Value | `fsrs.Rating` |
   |---|---|---|
   | Again | 1 | `Again`  |
   | Hard  | 2 | `Hard`   |
   | Good  | 3 | `Good`   |
   | Easy  | 4 | `Easy`   |

4. **Scheduling.** `Scheduler.Grade` calls `f.Repeat(card, now)` which returns four candidate
   next-states (one per rating); the one matching the user's rating is persisted along with a new
   `due`, `stability`, `difficulty`, `state`, `reps`/`lapses`, and a row in `review_logs`. The card
   then reappears the next time `due <= NOW`.

5. **State transitions.** New → Learning → Review on successful grades; Review → Relearning on
   **Again**. Within Learning/Relearning, intervals are sub-day (minutes); from Review onward they
   stretch out by `stability`.

The scheduler is a single `*fsrs.Scheduler` held on `handlers.Server` and constructed at boot in
[cmd/server/main.go](../cmd/server/main.go) from the YAML config. FSRS itself is stateless — it
just holds parameters.

## Configuration

A `fsrs:` block in `config.yaml` is optional. Omitted fields fall back to the library defaults
(matching previous behavior).

```yaml
fsrs:
  request_retention: 0.9      # 0 < x < 1
  maximum_interval: 36500     # days
  enable_fuzz: false
  new_cards_per_day: 20       # per (user, deck), local-day boundary
```

### Knobs

| Key | Default | Effect |
|---|---|---|
| `request_retention` | `0.9` | Target probability of recall at next review. **Lower** → longer intervals, fewer reviews, more forgetting. **Higher** → tighter intervals, more daily work. Typical range 0.80–0.97. Values outside `(0, 1)` are clamped to the default. |
| `maximum_interval` | `36500` (≈100 years) | Hard cap on how far out a card can be scheduled. Lower this if you want long-mature cards to still resurface periodically. |
| `enable_fuzz` | `false` | When true, intervals ≥ 2.5 days are randomly nudged ±5–15% so a big batch added on the same day doesn't all come due on the same future day. Recommended once a deck stabilizes. |
| `new_cards_per_day` | `20` | Cap on **New** (state=0) cards introduced from each deck per day. Once reached, the queue serves only Learning/Review/Relearning cards until the next local-day rollover. See below. |

### Daily new-card cap

Without a cap, opening a large deck (e.g. Goethe-A2, ~700 words) seeds every word with
`due = NOW`, so the queue picks unseen New cards before any rated card can repeat. With
`EnableShortTerm=true`, an **Again** rating only pushes a card ~1 minute into the future — but
that's still *later* than the original seed time of every unseen card, so the spaced-repetition
loop never engages until the entire deck has been touched once.

The cap fixes this:

- The handler counts how many cards from the current deck have left the New state today, via
  `SELECT COUNT(*) FROM review_logs WHERE state = 0 AND date(reviewed_at, 'localtime') = date('now', 'localtime')`
  (`review_logs.state` records the card's **prior** state, so rows with `state=0` are
  exactly the first-time gradings of New cards).
- When that count reaches `new_cards_per_day`, the next-card query gains `AND c.state != 0`,
  so only Learning/Review/Relearning cards can come up for the rest of the local day.
- The cap is per `(user, deck)` and resets at local midnight on the server.

**Practical effect.** With the default `20`, you introduce up to 20 new words per deck per day.
Failed (Again) cards from today's batch come back within the same session because they're the
only remaining due cards once the cap is hit — which is the spaced-repetition loop working as
intended.

Setting `new_cards_per_day: 0` (or any non-positive number) falls back to the default `20`;
there's no "zero new cards" mode in config — pause manually if you need that.

### Knobs we do NOT expose

The library also accepts `Decay` (forgetting-curve shape), `EnableShortTerm` (sub-day Learning
steps), and `W` (the 19 FSRS weights). These aren't surfaced because:

- `Decay` and `EnableShortTerm` rarely need changing.
- `W` is meant to be **fitted from your own review history**, not hand-edited. If/when Recall grows
  an FSRS optimizer, that's the natural place to plumb it.

### Knobs that don't exist yet

Things commonly found in other SRS apps that Recall does *not* currently have:

- No daily review cap.
- No order options (mature-first, by-difficulty, etc.) — the queue is strictly `due ASC, RANDOM()`.
- No per-user or per-deck overrides for FSRS algorithm parameters — one scheduler instance
  serves the whole server. (The `new_cards_per_day` cap *is* applied per user+deck, but the
  number itself is global.)
- No leech detection / suspension.

## Code map

| File | Role |
|---|---|
| [internal/fsrs/scheduler.go](../internal/fsrs/scheduler.go) | Wraps `go-fsrs`. `New(Options)` applies overrides; `Grade(...)` runs one step; `RatingFromInt` maps 1–4 → `Rating`. |
| [internal/handlers/review.go](../internal/handlers/review.go) | HTTP handlers: study page seeding, next-card queue, reveal, grade. |
| [internal/handlers/handlers.go](../internal/handlers/handlers.go) | Holds the `*fsrs.Scheduler` on `Server`. |
| [internal/config/config.go](../internal/config/config.go) | `FSRSConfig` and default-fill in `Load`. |
| [cmd/server/main.go](../cmd/server/main.go) | Constructs the scheduler at boot. |
| [internal/models/models.go](../internal/models/models.go) | `Card` struct mirroring the FSRS state columns. |
