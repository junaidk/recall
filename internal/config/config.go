package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server ServerConfig `yaml:"server"`
	DB     DBConfig     `yaml:"db"`
	DeepL  DeepLConfig  `yaml:"deepl"`
	Import ImportConfig `yaml:"import"`
	Audio  AudioConfig  `yaml:"audio"`
	FSRS   FSRSConfig   `yaml:"fsrs"`
}

type ServerConfig struct {
	Addr          string `yaml:"addr"`
	SessionSecret string `yaml:"session_secret"`
}

type DBConfig struct {
	Path string `yaml:"path"`
}

type DeepLConfig struct {
	APIKey     string `yaml:"api_key"`
	APIURL     string `yaml:"api_url"`
	SourceLang string `yaml:"source_lang"`
	TargetLang string `yaml:"target_lang"`
}

type ImportConfig struct {
	SeedDir string `yaml:"seed_dir"`
}

type AudioConfig struct {
	CacheDir string `yaml:"cache_dir"`
}

type FSRSConfig struct {
	RequestRetention float64 `yaml:"request_retention"`
	MaximumInterval  float64 `yaml:"maximum_interval"`
	EnableFuzz       bool    `yaml:"enable_fuzz"`
	NewCardsPerDay   int     `yaml:"new_cards_per_day"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var c Config
	if err := yaml.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if c.Server.Addr == "" {
		c.Server.Addr = ":8080"
	}
	if c.DB.Path == "" {
		c.DB.Path = "data/anki.db"
	}
	if c.DeepL.APIURL == "" {
		c.DeepL.APIURL = "https://api-free.deepl.com/v2/translate"
	}
	if c.DeepL.SourceLang == "" {
		c.DeepL.SourceLang = "DE"
	}
	if c.DeepL.TargetLang == "" {
		c.DeepL.TargetLang = "EN-US"
	}
	if c.Import.SeedDir == "" {
		c.Import.SeedDir = "seed"
	}
	if c.Audio.CacheDir == "" {
		c.Audio.CacheDir = "data/audio_cache"
	}
	if c.FSRS.RequestRetention <= 0 || c.FSRS.RequestRetention >= 1 {
		c.FSRS.RequestRetention = 0.9
	}
	if c.FSRS.MaximumInterval <= 0 {
		c.FSRS.MaximumInterval = 36500
	}
	if c.FSRS.NewCardsPerDay <= 0 {
		c.FSRS.NewCardsPerDay = 20
	}
	if c.Server.SessionSecret == "" {
		return nil, fmt.Errorf("server.session_secret must be set")
	}
	return &c, nil
}
