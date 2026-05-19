# Recall — German vocab trainer

Single-binary Go web app that drills German Goethe-Zertifikat vocabulary using the FSRS spaced-repetition algorithm. Word translations (DeepL), example sentences (Tatoeba), and pronunciation MP3s (DWDS) are harvested once and cached in SQLite forever — the expensive lookups are also checked into `seed/` as JSON, so a clean clone reproduces the full reference dataset in seconds without re-running them.

## Quick start

```bash
cp config.example.yaml config.yaml
# Edit config.yaml — set deepl.api_key (optional but recommended)

go build -tags sqlite_fts5 -o recall ./cmd/server
./recall
```

Open http://localhost:8080, register, pick a deck, study.

## Configuration (`config.yaml`)

| Key | Notes |
|---|---|
| `server.addr` | Listen address (e.g. `:8080`) |
| `server.session_secret` | Required; long random string |
| `db.path` | SQLite file (created if absent) |
| `deepl.api_key` | DeepL API key. Leave empty to skip translation. Free-tier keys end in `:fx`. |
| `deepl.api_url` | Use `https://api-free.deepl.com/v2/translate` for free, `https://api.deepl.com/v2/translate` for paid |
| `import.seed_dir` | Folder scanned for word lists and enrichment JSON on boot (default `seed`) |

## How it works

On every boot the server:

1. Applies the schema (idempotent `CREATE TABLE IF NOT EXISTS …`) and runs any pending migrations.
2. Scans `seed/*.json` and upserts each into a deck named after the file stem (`A2.json` → deck `A2`). `*.enrichment.json` sidecars are skipped here.
3. Loads `seed/<deck>.enrichment.json` and fills NULL columns (`translation_en`, `audio_url`, `example_de`, `example_en`, `example_source`) for matching word rows. Existing non-NULL values are never overwritten (COALESCE).
4. Fetches a DeepL translation for any word still missing one (batches of 50). Cached forever.
5. On first boot only, downloads the Tatoeba German–English sentence corpus (~10 MB) and indexes it with SQLite FTS5. Picks one example sentence per word.
6. In the background, scrapes the DWDS dictionary page for each word with no pronunciation MP3 yet and stores the audio URL. Empty string = page loaded but DWDS has no audio for that lemma (so it won't be retried).
7. Starts the HTTP server.

> **Build tag required**: FTS5 support in `mattn/go-sqlite3` is gated behind the `sqlite_fts5` build tag — pass `-tags sqlite_fts5` to `go build` / `go run`.

Each user has their own FSRS schedule for every word. Card state is seeded lazily the first time a user opens a deck.

## Audio pronunciation

The card back shows a small ▶ button next to the lemma when DWDS has an MP3 for that word. The `<audio>` element hotlinks straight to `www.dwds.de` (no proxying). A per-device **Autoplay audio** checkbox on the study page (stored in `localStorage`) governs whether the MP3 plays automatically on card reveal — when off, you can still click ▶.

## Seed data: reference data without checking in the DB

The local `data/anki.db` holds three kinds of state:

- **Expensive-to-rebuild reference data** — translations (DeepL), audio URLs (DWDS scrape), example sentences (Tatoeba). Deterministic but slow to regenerate.
- **External corpus** — `sentence_pairs` (~331 K rows, auto-downloaded once from manythings.org).
- **Local-only state** — `users` (password hashes), `sessions`, `cards` (FSRS scheduling), `review_logs`.

`data/` is gitignored. The expensive reference data is instead committed as JSON under `seed/<deck>.enrichment.json` (keyed by DWDS `url`, sorted, deterministic). A clean clone boots into a fully-populated reference dataset in seconds: the importer loads `seed/<deck>.json` into the `words` table, the seed loader fills enrichment columns from the sidecar files, and the auto-backfills (translator / audio / sentences) only run against rows the seed didn't cover.

### Refreshing `seed/*.enrichment.json` after a backfill

After translating or scraping new audio locally, regenerate the enrichment files and commit them:

```bash
go build -tags sqlite_fts5 -o seed-export ./cmd/seed-export
./seed-export                                  # writes seed/<deck>.enrichment.json
git add seed/*.enrichment.json
git commit -m "refresh enrichment seed"
```

Output is deterministic (URL-sorted, indented JSON, trailing newline), so re-running without DB changes produces byte-identical files.

## Adding a new word list

Drop a JSON file shaped like `seed/A2.json` into `seed/` and restart. The DWDS JSON schema is documented in [api-doc.md](api-doc.md). The first boot will fetch translations / examples / audio for the new words; once everything is filled in, run `./seed-export` to capture the harvested data and commit the new `seed/<deck>.enrichment.json`.

## Layout

```
cmd/server/main.go            entry point
cmd/seed-export/              dumps enriched columns to seed/<deck>.enrichment.json
internal/config/              YAML loader
internal/db/                  connection + embedded schema + migrations
internal/auth/                bcrypt + cookie sessions + middleware
internal/importer/            JSON → DB (skips *.enrichment.json)
internal/seed/                loads *.enrichment.json sidecars at boot
internal/translator/          DeepL client + batch worker
internal/sentences/           Tatoeba corpus + FTS5 example picker
internal/audio/               DWDS pronunciation URL scraper
internal/fsrs/                wraps github.com/open-spaced-repetition/go-fsrs/v3
internal/handlers/            HTTP routes
internal/web/                 templates + static (embed.FS)
seed/                         source vocab JSON + *.enrichment.json sidecars
```

## Keyboard shortcuts (during study)

- `Space` — show the back of the card
- `1` — Again
- `2` — Hard
- `3` — Good
- `4` — Easy
