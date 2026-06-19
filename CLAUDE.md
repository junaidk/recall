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

## Keep new UI consistent with the existing styles

When adding or changing any UI feature, make sure the CSS is consistent with the
rest of the app rather than introducing one-off styling. Reuse the existing
classes and design tokens in `internal/web/static/styles.css`:

- **Forms/cards**: wrap pages in `.card` and group fields with `.settings-field`
  (left-aligned label + control), mirroring `settings.html`.
- **Inputs/selects**: `1px solid #bbb`, `border-radius: 4px`, `font: inherit`;
  on focus use `border-color: #2d62b8` with a `0 0 0 2px #d8e3f6` ring.
- **Buttons**: primary actions use `.primary` (filled `#2d62b8`); secondary
  links use `.navbtn` or the outlined `.stats` style.
- **Accent color** is `#2d62b8` throughout (set `accent-color: #2d62b8` on
  native radios/checkboxes); muted text is `#666`/`#888`.
- Server-rendered HTML + HTMX only — no client framework. Page templates define
  `title`/`body` and end with `{{template "base" .}}`; HTMX fragments are
  separate `_partial.html` files registered in `internal/web/web.go`.

Verify UI changes by rendering the real templates (not just a static mock)
before claiming they look right.
