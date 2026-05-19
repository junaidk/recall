# Recall — German vocab trainer

Single-binary Go web app that drills German Goethe-Zertifikat vocabulary using the FSRS spaced-repetition algorithm. Translations are fetched from the DeepL API once and cached in SQLite forever.

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
| `import.word_list_dir` | Folder scanned for `*.json` word lists on boot |

## How it works

On every boot the server:

1. Applies the schema (idempotent `CREATE TABLE IF NOT EXISTS …`) and runs any pending migrations.
2. Scans `word-list/*.json` and upserts each into a deck named after the file stem (`A2.json` → deck `A2`).
3. Fetches a DeepL translation for any word with no translation yet (batches of 50). Cached forever.
4. On first boot only, downloads the Tatoeba German–English sentence corpus (~10 MB) and indexes it with SQLite FTS5. Picks one example sentence per word.
5. Starts the HTTP server.

> **Build tag required**: FTS5 support in `mattn/go-sqlite3` is gated behind the `sqlite_fts5` build tag — pass `-tags sqlite_fts5` to `go build` / `go run`.

Each user has their own FSRS schedule for every word. Card state is seeded lazily the first time a user opens a deck.

## Adding a new word list

Drop a JSON file shaped like `word-list/A2.json` into `word-list/` and restart. The DWDS JSON schema is documented in [api-doc.md](api-doc.md).

## Layout

```
cmd/server/main.go            entry point
internal/config/              YAML loader
internal/db/                  connection + embedded schema
internal/auth/                bcrypt + cookie sessions + middleware
internal/importer/            JSON → DB
internal/translator/          DeepL client + batch worker
internal/fsrs/                wraps github.com/open-spaced-repetition/go-fsrs/v3
internal/handlers/            HTTP routes
internal/web/                 templates + static (embed.FS)
word-list/                    source vocab JSON
```

## Keyboard shortcuts (during study)

- `Space` — show the back of the card
- `1` — Again
- `2` — Hard
- `3` — Good
- `4` — Easy
