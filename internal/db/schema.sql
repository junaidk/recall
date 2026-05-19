PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;

CREATE TABLE IF NOT EXISTS users (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  username TEXT UNIQUE NOT NULL,
  password_hash TEXT NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS decks (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT UNIQUE NOT NULL,
  source_path TEXT,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS words (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  deck_id INTEGER NOT NULL REFERENCES decks(id) ON DELETE CASCADE,
  lemma TEXT NOT NULL,
  hidx INTEGER,
  pos TEXT,
  articles TEXT,
  genera TEXT,
  url TEXT,
  only_plural INTEGER NOT NULL DEFAULT 0,
  translation_en TEXT,
  translated_at DATETIME,
  example_de TEXT,
  example_en TEXT,
  example_source TEXT,
  examples_at DATETIME,
  audio_url TEXT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_words_unique
  ON words(deck_id, lemma, COALESCE(hidx, 0));
CREATE INDEX IF NOT EXISTS idx_words_untranslated
  ON words(id) WHERE translation_en IS NULL;

CREATE TABLE IF NOT EXISTS cards (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  word_id INTEGER NOT NULL REFERENCES words(id) ON DELETE CASCADE,
  due DATETIME NOT NULL,
  stability REAL NOT NULL DEFAULT 0,
  difficulty REAL NOT NULL DEFAULT 0,
  elapsed_days INTEGER NOT NULL DEFAULT 0,
  scheduled_days INTEGER NOT NULL DEFAULT 0,
  reps INTEGER NOT NULL DEFAULT 0,
  lapses INTEGER NOT NULL DEFAULT 0,
  state INTEGER NOT NULL DEFAULT 0,
  last_review DATETIME,
  UNIQUE(user_id, word_id)
);
CREATE INDEX IF NOT EXISTS idx_cards_user_due ON cards(user_id, due);

CREATE TABLE IF NOT EXISTS review_logs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  card_id INTEGER NOT NULL REFERENCES cards(id) ON DELETE CASCADE,
  rating INTEGER NOT NULL,
  state INTEGER NOT NULL,
  elapsed_days INTEGER NOT NULL,
  scheduled_days INTEGER NOT NULL,
  reviewed_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sessions (
  token TEXT PRIMARY KEY,
  user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  expires_at DATETIME NOT NULL,
  created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sentence_pairs (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  de TEXT NOT NULL,
  en TEXT NOT NULL,
  de_len INTEGER NOT NULL
);

CREATE VIRTUAL TABLE IF NOT EXISTS sentence_pairs_fts
  USING fts5(de, content='sentence_pairs', content_rowid='id', tokenize='unicode61');

CREATE TRIGGER IF NOT EXISTS sentence_pairs_ai
  AFTER INSERT ON sentence_pairs BEGIN
    INSERT INTO sentence_pairs_fts(rowid, de) VALUES (new.id, new.de);
  END;
