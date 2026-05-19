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
	WordListDir string `yaml:"word_list_dir"`
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
	if c.Import.WordListDir == "" {
		c.Import.WordListDir = "word-list"
	}
	if c.Server.SessionSecret == "" {
		return nil, fmt.Errorf("server.session_secret must be set")
	}
	return &c, nil
}
