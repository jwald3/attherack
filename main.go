// Command attherack is a self-hosted training tracker with an AI coach. This
// file only wires things together; the app lives in internal/ (see README).
package main

import (
	"flag"
	"log"
	"net/http"
	"path/filepath"

	"github.com/jwald3/attherack/internal/config"
	"github.com/jwald3/attherack/internal/exercise"
	"github.com/jwald3/attherack/internal/importer"
	"github.com/jwald3/attherack/internal/seed"
	"github.com/jwald3/attherack/internal/server"
	"github.com/jwald3/attherack/internal/store"
)

func main() {
	importExPath := flag.String("import-exercises", "", "path to a ChimpFitness exercises CSV to import, then continue")
	importWoPath := flag.String("import-workouts", "", "path to a ChimpFitness workouts CSV to import, then continue")
	importBwPath := flag.String("import-bodyweight", "", "path to a workouts CSV; import only the body_weight column (idempotent)")
	migrateCardio := flag.String("migrate-cardio", "", "path to a workouts CSV; move cardio into cardio_sessions and remove legacy marker sets/notes (idempotent)")
	importOnly := flag.Bool("import-only", false, "exit after running imports instead of starting the server")
	seedDemo := flag.Bool("seed-demo", false, "fill an empty database with ~6 weeks of fake demo data (refuses if it has data)")
	dbFlag := flag.String("db", "", "SQLite database file (overrides DB_PATH; default attherack.db)")
	flag.Parse()

	config.LoadDotEnv(".env")
	cfg := config.FromEnv()
	if *dbFlag != "" {
		cfg.DBPath = *dbFlag
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	// One-shot CSV imports (exercises first so muscle/equipment metadata exists).
	for _, imp := range []struct {
		path, what string
		run        func(*store.Store, string) error
	}{
		{*importExPath, "exercises", importer.Exercises},
		{*importWoPath, "workouts", importer.Workouts},
		{*importBwPath, "bodyweight", importer.Bodyweight},
		{*migrateCardio, "cardio", importer.MigrateCardio},
	} {
		if imp.path == "" {
			continue
		}
		log.Printf("importing %s from %s", imp.what, imp.path)
		if err := imp.run(st, imp.path); err != nil {
			log.Fatalf("import %s: %v", imp.what, err)
		}
	}
	if *seedDemo {
		if err := seed.Demo(st); err != nil {
			log.Fatalf("seed demo: %v", err)
		}
		log.Printf("seeded demo data into %s", filepath.Clean(cfg.DBPath))
	}
	if *importOnly {
		log.Print("import-only: done")
		return
	}

	lib, err := exercise.LoadBuiltin()
	if err != nil {
		log.Fatalf("load exercises: %v", err)
	}
	// Merge any user-added exercises from previous runs.
	custom, err := st.ListCustomExercises()
	if err != nil {
		log.Fatalf("load custom exercises: %v", err)
	}
	lib.Add(custom...)
	log.Printf("loaded %d exercises (%d built-in, %d custom)", lib.Count(), lib.Count()-len(custom), len(custom))

	app, err := server.New(st, lib, cfg.AnthropicBaseURL)
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}
	app.InitAPIKey(cfg.APIKey)

	log.Printf("At The Rack listening on %s (db: %s)", cfg.Addr, filepath.Clean(cfg.DBPath))
	if err := http.ListenAndServe(cfg.Addr, app.Handler()); err != nil {
		log.Fatal(err)
	}
}
