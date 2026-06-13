# Recall — project notes for Claude

## Source data lives in `extra/` — check it before downloading

The maintainer-only seed builders default to downloading large upstream
dumps, but **local copies already exist in the gitignored `extra/` directory**.
Always prefer these to re-downloading (the kaikki extract alone is ~3 GB):

| File in `extra/` | Upstream source | Used by |
|---|---|---|
| `de-extract.jsonl` / `.gz` | kaikki.org German Wiktionary (`https://kaikki.org/dictionary/downloads/de/de-extract.jsonl.gz`) | `cmd/build-conjugations`, `cmd/build-noun-plurals` (pass `-src extra/de-extract.jsonl`) |
| `deu-eng.zip` / `deu.txt` | manythings.org / Tatoeba German-English pairs (`https://www.manythings.org/anki/deu-eng.zip`) | `internal/sentences` (Tatoeba sentence corpus) |

Example — rebuild the verb conjugation seed from the local extract instead of
the network default:

```bash
go run ./cmd/build-conjugations -src extra/de-extract.jsonl -out seed/de_verb_conjugations.jsonl
```

Rebuilt seeds reload automatically on the next boot (see `internal/seedmeta`),
so no manual DB reset is needed after regenerating a seed.
