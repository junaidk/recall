# Verb case government — sourcing research

Research notes for a future "object case" panel on verb cards (e.g. show "+ Dativ" for *helfen*, "+ Genitiv" for *gedenken*). Not implemented yet.

## What we want

For each German verb in the deck, the case its direct object takes:

- **Akkusativ** — the default. Most transitive verbs (*kaufen*, *sehen*, *essen*).
- **Dativ** — ~35 commonly-taught verbs (*helfen*, *danken*, *folgen*, *gefallen*, *gehören*, …).
- **Genitiv** — a handful, mostly archaic/literary (*gedenken*, *bedürfen*).
- Some verbs govern multiple cases for different senses; we'd surface the primary one.

The data point per verb is small — a single string — so storage is trivial. The hard part is sourcing it.

## Sources evaluated

### kaikki.org de-extract (already used for verbs + nouns)

Probed canonical verbs against `extra/de-extract.jsonl`. Findings:

| Verb | `senses[0].tags` | Verdict |
|---|---|---|
| `gefallen` | `["dative"]` | ✓ structured |
| `danken` | `["dative intransitive primarily"]` | ✓ structured |
| `gedenken` | `["genitive intransitive"]` | ✓ structured |
| `bedürfen` | `["gehoben genitive"]` | ✓ structured |
| `helfen` | `[]` | ✗ only in gloss: `"(mit Dativ, seltener mit Akkusativ) …"` |
| `folgen` | `[]` | ✗ only implicit (`jemandem` in gloss) |
| `kaufen` / `sehen` / `essen` | `["transitive"]` on object-taking senses | ⚠ accusative is never tagged, only implied by `transitive` |

So kaikki carries case for ~50–70 % of verbs structurally in `senses[].tags`, but misses canonical entries like *helfen*. Akkusativ is never explicit — it's the default when a sense is `transitive`. The remaining valency info lives in gloss prose (regex over `(mit Dativ|mit Genitiv|mit Akkusativ)` recovers most of the rest).

### DWDS API — ruled out

Only public endpoint is `https://www.dwds.de/api/wb/snippet/?q={lemma}`, which returns `{wortart, url, lemma}` and nothing else. The DWDS docs at `/d/api` explicitly state full entry content is blocked "aus rechtlichen Gründen." All richer endpoint guesses (`wb/json`, `wb?format=json`, …) return 404. Scraping the HTML entry pages is possible but legally murky.

### Wikidata Lexemes — ruled out

Probed L451227 (*helfen*) directly: claims include stems, forms, etymology, external IDs — **no** case-government claim at lexeme or sense level. The only related Wikidata property is `P5526 valency`, which captures the *count* of arguments (0/1/2/3), not their case. SPARQL count:

```
German verb lexemes total: 20,397
… with P5526 set:         0
```

No usable structured data on Wikidata today.

### VALBU (IDS Mannheim)

<https://grammis.ids-mannheim.de/verbvalenz>. ~640 hand-curated verbs by professional linguists. Free for non-commercial use. No bulk dump — would need a polite scraper (one GET per lemma, cache locally). Likely covers most Goethe A1–B1 verbs because they're the common ones. Highest quality of any free source.

### DBnary (Wiktionary as Lemon RDF)

<https://kaiko.getalp.org/about-dbnary/>. Re-parses the same Wiktionary source as kaikki but normalises differently; sometimes catches case-government markers kaikki strips. SPARQL-queryable. Not yet probed — worth a quick check if VALBU coverage proves insufficient.

### Re-parse the de.wiktionary XML dump ourselves

<https://dumps.wikimedia.org/dewiktionary/latest/dewiktionary-latest-pages-articles.xml.bz2>. Verb pages mark valency directly in the wikitext (`:[1] (mit Dativ)` style annotations under `{{Bedeutungen}}`). A targeted regex over the raw wikitext recovers what kaikki's gloss-cleaning silently drops. More work than any other option, but fully offline, no rate limits, and DWDS-equivalent coverage.

## Recommendation

**Phase 1: hand-curated overrides + kaikki tags + gloss regex.**

The set of non-Akkusativ verbs taught at A1–B1 is small enough to enumerate by hand from any standard grammar table (~35 dative, ~5 genitive). Combined with kaikki's structured tags as a fallback and `transitive` → Akkusativ as the default, this covers the entire deck with ~30 minutes of curation and zero new build infrastructure.

**Phase 2 (if needed):** scrape VALBU to widen coverage beyond the curated list, or re-parse the de.wiktionary dump for the same purpose.

## Implementation sketch (when picked up)

Mirror the noun-plurals pipeline. Concrete pieces:

- **`seed/de_verb_cases_overrides.json`** — hand-curated:
  ```json
  {
    "helfen":    "Dativ",
    "danken":    "Dativ",
    "folgen":    "Dativ",
    "antworten": "Dativ",
    "gehören":   "Dativ",
    "passen":    "Dativ",
    "schmecken": "Dativ",
    "gefallen":  "Dativ",
    "begegnen":  "Dativ",
    "gedenken":  "Genitiv",
    "bedürfen":  "Genitiv"
  }
  ```
  (Full A1 dative list: ~12 entries; A2 adds ~15; B1 adds the rest.)

- **`cmd/build-verb-cases/main.go`** — clone of `cmd/build-noun-plurals/main.go`. For each German verb in the kaikki extract, pick the case in this order:
  1. Override file
  2. `senses[].tags` containing `"dative"` / `"genitive"`
  3. Regex over `senses[0].glosses[0]` for `mit (Dativ|Genitiv|Akkusativ)`
  4. Any sense tagged `"transitive"` → default `Akkusativ`
  5. Else: emit nothing (no panel rendered)

  Emit `seed/de_verb_cases.jsonl`:
  ```json
  {"infinitive":"helfen","case":"Dativ","source":"override"}
  {"infinitive":"gedenken","case":"Genitiv","source":"kaikki-tag"}
  ```

- **Schema migration v6**: `words.verb_case TEXT` + `words.verb_case_at DATETIME`, plus a `verb_cases` corpus table. Same shape as `noun_plurals`.

- **`internal/verbcases/{loader,backfill}.go`** — direct clones of `internal/plurals/`. Backfill `WHERE pos = 'Verb' AND verb_case IS NULL`.

- **Render** — the existing conjugations card has space above the Präsens/Perfekt tables for a one-liner. Add `<div class="verb-case">+ {{.VerbCase}}</div>` to `_conjugations_block.html` rather than a new partial — case government is part of the same grammatical info, and a separate card would be wasteful for a single string.

- **Toggle**: the existing "Show grammar tables" toggle already covers this — no new control needed.

## Open questions for the future session

- For verbs governing multiple cases per sense (*helfen* — mostly Dativ, rarely Akkusativ): show only the primary, or both? Probably primary; a `"Dativ / (Akk.)"` rendering exists in DWDS but adds clutter.
- Should multi-object verbs (e.g. *geben* — Dativ recipient + Akkusativ direct object) get a richer rendering than a single case? For A1–B1 the answer is probably "no, it's beyond the panel's scope" and we'd just mark them `Akkusativ`.
- How to surface "source" in the UI (if at all). The build emits `source: "override" | "kaikki-tag" | "kaikki-gloss" | "default-transitive"` for our own debugging; users probably never see it.
