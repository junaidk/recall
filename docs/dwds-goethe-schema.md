# DWDS Goethe-Zertifikat word list — JSON schema

Recall seeds its decks from the [DWDS](https://www.dwds.de/) word lists for the Goethe-Zertifikat language levels. DWDS publishes them in both CSV and JSON; Recall imports the JSON form.

## Source files

| Level | CSV | JSON |
|---|---|---|
| A1 | [csv](https://www.dwds.de/themenglossar/Goethe-A1.csv) | [json](https://www.dwds.de/themenglossar/Goethe-A1.json) |
| A2 | [csv](https://www.dwds.de/themenglossar/Goethe-A2.csv) | [json](https://www.dwds.de/themenglossar/Goethe-A2.json) |
| B1 | [csv](https://www.dwds.de/themenglossar/Goethe-B1.csv) | [json](https://www.dwds.de/themenglossar/Goethe-B1.json) |

The CSV form has one row per spelling. When a noun has more than one possible gender or article, those values are comma-separated:

```csv
"Lemma","URL","Wortart","Genus","Artikel","nur_im_Plural"
"abschließen","https://www.dwds.de/wb/abschlie%C3%9Fen","Verb","","","0"
"Ahnung","https://www.dwds.de/wb/Ahnung","Substantiv","fem.","die","0"
"Leute","https://www.dwds.de/wb/Leute","Substantiv","","","1"
"Teil","https://www.dwds.de/wb/Teil","Substantiv","mask., neutr.","der, das","0"
```

## JSON shape

Each list is a top-level array of entries:

```json
[
  {
    "pos": "Substantiv",
    "url": "https://www.dwds.de/wb/Ahnung",
    "sch": [{ "lemma": "Ahnung", "hidx": null }],
    "articles": ["die"],
    "genera": ["fem."]
  },
  {
    "pos": "Substantiv",
    "url": "https://www.dwds.de/wb/Teil",
    "sch": [{ "lemma": "Teil", "hidx": null }],
    "articles": ["der", "das"],
    "genera": ["mask.", "neutr."]
  }
]
```

### Fields

| Field | Type | Description |
|---|---|---|
| `pos` | string | Part of speech (`Substantiv`, `Verb`, `Adjektiv`, `Adverb`, `Konjunktion`, `Mehrwortausdruck`, …). |
| `url` | string | Canonical URL of the DWDS dictionary entry. |
| `sch` | array | Spellings / forms used in the dictionary entry. |
| `sch[].lemma` | string | The lemma's spelling. |
| `sch[].hidx` | string \| null | Optional homograph index, set when the same spelling has multiple entries (e.g. ¹Bank and ²Bank). |
| `articles` | string[] | Optional; for nouns, the definite articles (`der`, `die`, `das`). |
| `genera` | string[] | Optional; the grammatical genders for the lemma (`mask.`, `fem.`, `neutr.`). |
| `onlypl` | string | Optional; literal value `"nur im Plural"` when the word is plural-only. |

Source: <https://www.dwds.de/d/api#wb-list-goethe>

## What DWDS does not provide

The Goethe themenglossar export carries only the headword, article, and gender — no declined or conjugated forms. Recall enriches the imported nouns and verbs from a second source (the de.wiktionary morphological dump on [kaikki.org](https://kaikki.org/dictionary/downloads/de/de-extract.jsonl.gz)) via two maintainer-only builders:

| Builder | Output | Powers |
|---|---|---|
| `cmd/build-conjugations` | `seed/de_verb_conjugations.jsonl` | The Präsens / Perfekt panel on verb cards |
| `cmd/build-noun-plurals` | `seed/de_noun_plurals.jsonl` | The Nom/Akk/Dat/Gen × Singular/Plural panel on noun cards |

Both seeds are loaded once at boot into corpus tables (`verb_conjugations`, `noun_plurals`) and backfilled onto the `words` rows. Nouns flagged `onlypl` are excluded from the plural backfill — the lemma itself is already a plural form.
