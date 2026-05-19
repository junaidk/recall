package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/config"
	"github.com/junaidk/recall/internal/db"
	"github.com/junaidk/recall/internal/handlers"
	"github.com/junaidk/recall/internal/importer"
	"github.com/junaidk/recall/internal/sentences"
	"github.com/junaidk/recall/internal/translator"
	"github.com/junaidk/recall/internal/web"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to config file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	dbConn, err := db.Open(cfg.DB.Path)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer dbConn.Close()
	log.Printf("db: opened %s", cfg.DB.Path)

	if err := importer.ScanAndImport(dbConn, cfg.Import.WordListDir); err != nil {
		log.Fatalf("import: %v", err)
	}

	deeplClient := translator.NewClient(cfg.DeepL.APIKey, cfg.DeepL.APIURL, cfg.DeepL.SourceLang, cfg.DeepL.TargetLang)
	if n, err := translator.TranslateAllPending(dbConn, deeplClient); err != nil {
		log.Printf("translate: stopped with error: %v (translated %d so far)", err, n)
	} else {
		log.Printf("translate: %d words translated this boot", n)
	}

	if err := sentences.EnsureCorpus(dbConn); err != nil {
		log.Printf("sentences: corpus load failed: %v", err)
	} else if _, _, err := sentences.Backfill(dbConn); err != nil {
		log.Printf("sentences: backfill failed: %v", err)
	}

	templates := web.MustLoadTemplates()
	sessions := auth.NewStore(dbConn)
	server := handlers.New(dbConn, sessions, templates)

	mux := http.NewServeMux()
	server.Register(mux)

	log.Printf("listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
