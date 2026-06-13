package main

import (
	"flag"
	"log"
	"net/http"

	"github.com/junaidk/recall/internal/audio"
	"github.com/junaidk/recall/internal/auth"
	"github.com/junaidk/recall/internal/config"
	"github.com/junaidk/recall/internal/conjugations"
	"github.com/junaidk/recall/internal/db"
	"github.com/junaidk/recall/internal/fsrs"
	"github.com/junaidk/recall/internal/handlers"
	"github.com/junaidk/recall/internal/importer"
	"github.com/junaidk/recall/internal/plurals"
	"github.com/junaidk/recall/internal/seed"
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

	if err := importer.ScanAndImport(dbConn, cfg.Import.SeedDir); err != nil {
		log.Fatalf("import: %v", err)
	}

	if n, err := seed.LoadEnrichment(dbConn, cfg.Import.SeedDir); err != nil {
		log.Printf("seed: load failed: %v", err)
	} else if n > 0 {
		log.Printf("seed: applied enrichment to %d words", n)
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

	if changed, err := conjugations.EnsureCorpus(dbConn, cfg.Import.SeedDir); err != nil {
		log.Printf("conjugations: corpus load failed: %v", err)
	} else if _, _, err := conjugations.Backfill(dbConn, changed); err != nil {
		log.Printf("conjugations: backfill failed: %v", err)
	}

	if changed, err := plurals.EnsureCorpus(dbConn, cfg.Import.SeedDir); err != nil {
		log.Printf("plurals: corpus load failed: %v", err)
	} else if _, _, err := plurals.Backfill(dbConn, changed); err != nil {
		log.Printf("plurals: backfill failed: %v", err)
	}

	go func() {
		if _, _, err := audio.Backfill(dbConn); err != nil {
			log.Printf("audio: backfill failed: %v", err)
		}
	}()

	audioCache, err := audio.NewCache(cfg.Audio.CacheDir)
	if err != nil {
		log.Fatalf("audio cache: %v", err)
	}

	templates := web.MustLoadTemplates()
	sessions := auth.NewStore(dbConn)
	scheduler := fsrs.New(fsrs.Options{
		RequestRetention: cfg.FSRS.RequestRetention,
		MaximumInterval:  cfg.FSRS.MaximumInterval,
		EnableFuzz:       cfg.FSRS.FuzzEnabled(),
	})
	server := handlers.New(dbConn, sessions, templates, scheduler, audioCache, cfg.FSRS.NewCardsPerDay)

	mux := http.NewServeMux()
	server.Register(mux)

	log.Printf("listening on %s", cfg.Server.Addr)
	if err := http.ListenAndServe(cfg.Server.Addr, mux); err != nil {
		log.Fatalf("listen: %v", err)
	}
}
