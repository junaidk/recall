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
   ORDER BY c.due ASC, RANDOM()
   LIMIT 1
   ```
   Oldest-due first; ties broken randomly. When the query returns zero rows, the session ends
   ("done" partial is rendered).

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
```

### Knobs

| Key | Default | Effect |
|---|---|---|
| `request_retention` | `0.9` | Target probability of recall at next review. **Lower** → longer intervals, fewer reviews, more forgetting. **Higher** → tighter intervals, more daily work. Typical range 0.80–0.97. Values outside `(0, 1)` are clamped to the default. |
| `maximum_interval` | `36500` (≈100 years) | Hard cap on how far out a card can be scheduled. Lower this if you want long-mature cards to still resurface periodically. |
| `enable_fuzz` | `false` | When true, intervals ≥ 2.5 days are randomly nudged ±5–15% so a big batch added on the same day doesn't all come due on the same future day. Recommended once a deck stabilizes. |

### Knobs we do NOT expose

The library also accepts `Decay` (forgetting-curve shape), `EnableShortTerm` (sub-day Learning
steps), and `W` (the 19 FSRS weights). These aren't surfaced because:

- `Decay` and `EnableShortTerm` rarely need changing.
- `W` is meant to be **fitted from your own review history**, not hand-edited. If/when Recall grows
  an FSRS optimizer, that's the natural place to plumb it.

### Knobs that don't exist yet

Things commonly found in other SRS apps that Recall does *not* currently have:

- No daily new-card cap.
- No daily review cap.
- No order options (mature-first, by-difficulty, etc.) — the queue is strictly `due ASC, RANDOM()`.
- No per-user or per-deck overrides — one scheduler instance serves the whole server.
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
