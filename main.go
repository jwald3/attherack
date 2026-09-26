package main

import (
	"embed"
	"flag"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed data/exercises.json
var exercisesJSON []byte

//go:embed web/templates/*.html
var templateFS embed.FS

//go:embed web/static/*
var staticFS embed.FS

// App holds shared server state.
type App struct {
	store *Store
	lib   *ExerciseLibrary
	tmpl  *template.Template

	mu     sync.RWMutex
	apiKey string // current active key ("" = chat disabled)
	envKey bool   // true when the key came from ANTHROPIC_API_KEY (UI is read-only)
	agent  *Agent // rebuilt whenever the key changes
}

// setKey installs a new API key at runtime and (re)builds the agent. Pass
// fromEnv=true only for the boot-time environment key.
func (a *App) setKey(key string, fromEnv bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.apiKey = key
	a.envKey = fromEnv
	if key == "" {
		a.agent = nil
		return
	}
	a.agent = newAgent(key, a.store, a.lib)
}

// clearKey removes a UI-configured key. No-op if the key came from the env.
func (a *App) clearKey() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.envKey {
		return
	}
	a.apiKey = ""
	a.agent = nil
}

// templateFuncs returns the helper functions available in every template.
func templateFuncs() template.FuncMap {
	return template.FuncMap{
		"deref": strDeref,
		"nl2br": nl2br,
		"md":    renderMarkdown,
		"dur":   fmtDuration,
		"pace":  fmtPace,
		"hms":   fmtClock,
		"mph":   fmtSpeed,
		"pct":   percent,
		"icon":  icon,
	}
}

func (a *App) getAgent() *Agent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.agent
}

func (a *App) chatEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.agent != nil
}

func (a *App) envLocked() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.envKey
}

// maskedKey returns a display-safe preview like "sk-ant-…a1b2", never the full key.
func (a *App) maskedKey() string {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return maskKey(a.apiKey)
}

func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 12 {
		return "••••"
	}
	return k[:7] + "…" + k[len(k)-4:]
}

func main() {
	importExPath := flag.String("import-exercises", "", "path to a ChimpFitness exercises CSV to import, then continue")
	importWoPath := flag.String("import-workouts", "", "path to a ChimpFitness workouts CSV to import, then continue")
	importBwPath := flag.String("import-bodyweight", "", "path to a workouts CSV; import only the body_weight column (idempotent)")
	migrateCardio := flag.String("migrate-cardio", "", "path to a workouts CSV; move cardio into cardio_sessions and remove legacy marker sets/notes (idempotent)")
	importOnly := flag.Bool("import-only", false, "exit after running imports instead of starting the server")
	seed := flag.Bool("seed-demo", false, "fill an empty database with ~6 weeks of fake demo data (refuses if it has data)")
	dbFlag := flag.String("db", "", "SQLite database file (overrides DB_PATH; default attherack.db)")
	flag.Parse()

	loadDotEnv(".env")

	// Localhost only by default: there is no login, so anyone who can reach the
	// port can read your data and use your API key. Set ADDR=:8080 to expose it.
	addr := envOr("ADDR", "127.0.0.1:8080")
	dbPath := envOr("DB_PATH", "attherack.db")
	if *dbFlag != "" {
		dbPath = *dbFlag
	}
	apiKey := os.Getenv("ANTHROPIC_API_KEY")

	store, err := openStore(dbPath)
	if err != nil {
		log.Fatalf("open db: %v", err)
	}

	// One-shot CSV imports (exercises first so muscle/equipment metadata exists).
	if *importExPath != "" {
		log.Printf("importing exercises from %s", *importExPath)
		if err := importExercises(store, *importExPath); err != nil {
			log.Fatalf("import exercises: %v", err)
		}
	}
	if *importWoPath != "" {
		log.Printf("importing workouts from %s", *importWoPath)
		if err := importWorkouts(store, *importWoPath); err != nil {
			log.Fatalf("import workouts: %v", err)
		}
	}
	if *importBwPath != "" {
		log.Printf("importing bodyweight from %s", *importBwPath)
		if err := importBodyweight(store, *importBwPath); err != nil {
			log.Fatalf("import bodyweight: %v", err)
		}
	}
	if *migrateCardio != "" {
		log.Printf("migrating cardio from %s", *migrateCardio)
		names, err := cardioNamesFromCSV(*migrateCardio)
		if err != nil {
			log.Fatalf("read cardio names: %v", err)
		}
		if err := importCardio(store, *migrateCardio); err != nil {
			log.Fatalf("import cardio: %v", err)
		}
		if err := removeCardioMarkerSets(store, names); err != nil {
			log.Fatalf("cleanup cardio markers: %v", err)
		}
	}
	if *seed {
		if err := seedDemo(store); err != nil {
			log.Fatalf("seed demo: %v", err)
		}
		log.Printf("seeded demo data into %s", filepath.Clean(dbPath))
	}
	if *importOnly {
		log.Print("import-only: done")
		return
	}

	lib, err := loadExercises(exercisesJSON)
	if err != nil {
		log.Fatalf("load exercises: %v", err)
	}
	// Merge any user-added exercises from previous runs.
	custom, err := store.listCustomExercises()
	if err != nil {
		log.Fatalf("load custom exercises: %v", err)
	}
	lib.Add(custom...)
	log.Printf("loaded %d exercises (%d built-in, %d custom)", lib.Count(), lib.Count()-len(custom), len(custom))

	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(templateFS, "web/templates/*.html")
	if err != nil {
		log.Fatalf("parse templates: %v", err)
	}

	app := &App{store: store, lib: lib, tmpl: tmpl}

	// Key resolution: ANTHROPIC_API_KEY wins (and locks the UI); otherwise fall
	// back to a key saved via the settings panel on a previous run.
	switch {
	case apiKey != "":
		app.setKey(apiKey, true)
		log.Print("Claude chat enabled (ANTHROPIC_API_KEY set — locked in UI)")
	default:
		if saved, err := store.getSetting("anthropic_api_key"); err == nil && saved != "" {
			app.setKey(saved, false)
			log.Print("Claude chat enabled (key loaded from settings)")
		} else {
			log.Print("Claude chat DISABLED — add a key in the coach panel or set ANTHROPIC_API_KEY")
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /", app.handleCoachHome)
	mux.HandleFunc("GET /c/{id}", app.handleCoachThread)
	mux.HandleFunc("POST /threads/{id}/delete", app.handleDeleteThread)
	mux.HandleFunc("POST /threads/{id}/rename", app.handleRenameThread)
	mux.HandleFunc("GET /training", app.handleTrainingPage)
	mux.HandleFunc("GET /exercises", app.handleExerciseSearch)
	mux.HandleFunc("GET /exercise", app.handleExerciseDetail)
	mux.HandleFunc("POST /exercises", app.handleAddExercise)
	mux.HandleFunc("POST /exercises/suggest", app.handleSuggestExercise)
	mux.HandleFunc("POST /sets", app.handleAddSet)
	mux.HandleFunc("POST /sets/{id}/delete", app.handleDeleteSet)
	mux.HandleFunc("GET /log", app.handleLogFragment)
	mux.HandleFunc("GET /cardio", app.handleCardioPage)
	mux.HandleFunc("GET /cardio/type", app.handleCardioDetail)
	mux.HandleFunc("POST /cardio", app.handleAddCardio)
	mux.HandleFunc("POST /cardio/{id}/delete", app.handleDeleteCardio)
	mux.HandleFunc("GET /diet", app.handleDietPage)
	mux.HandleFunc("POST /diet", app.handleAddFood)
	mux.HandleFunc("POST /diet/{id}/delete", app.handleDeleteFood)
	mux.HandleFunc("GET /supplements", app.handleSupplementsPage)
	mux.HandleFunc("POST /supplements", app.handleAddSupplement)
	mux.HandleFunc("POST /supplements/{id}/delete", app.handleDeleteSupplement)
	mux.HandleFunc("GET /bodyweight", app.handleBodyweightPage)
	mux.HandleFunc("POST /bodyweight", app.handleAddBodyweight)
	mux.HandleFunc("POST /bodyweight/{date}/delete", app.handleDeleteBodyweight)
	mux.HandleFunc("POST /chat", app.handleChat)
	mux.HandleFunc("GET /chat/msg/{id}", app.handleChatMessage)
	mux.HandleFunc("GET /settings", app.handleSettingsFragment)
	mux.HandleFunc("POST /settings/key", app.handleSaveKey)
	mux.HandleFunc("POST /settings/key/delete", app.handleClearKey)

	// Serve embedded static assets under /static/ (files live at web/static/*).
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(mustSub(staticFS, "web/static")))))

	log.Printf("At The Rack listening on %s (db: %s)", addr, filepath.Clean(dbPath))
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatal(err)
	}
}

// loadDotEnv reads KEY=VALUE lines from path (if it exists) into the process
// environment. Variables already set in the real environment win. Blank lines
// and # comments are skipped; surrounding quotes on values are removed.
func loadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if !ok || key == "" || val == "" {
			continue
		}
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, val)
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func mustSub(f embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(f, dir)
	if err != nil {
		log.Fatalf("sub fs %s: %v", dir, err)
	}
	return sub
}

// nl2br escapes text and converts newlines to <br> for safe HTML display.
func nl2br(s string) template.HTML {
	return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(s), "\n", "<br>"))
}

// fmtDuration renders a seconds count as "43m" or "1h 12m".
func fmtDuration(seconds int) string {
	if seconds <= 0 {
		return "—"
	}
	m := seconds / 60
	if m < 60 {
		return fmt.Sprintf("%dm", m)
	}
	return fmt.Sprintf("%dh %dm", m/60, m%60)
}

// percent returns n/total as a whole-number percentage clamped to 0-100.
func percent(n, total int) int {
	if total <= 0 || n <= 0 {
		return 0
	}
	if n >= total {
		return 100
	}
	return n * 100 / total
}

// fmtClock renders seconds as "m:ss" or "h:mm:ss".
func fmtClock(seconds int) string {
	if seconds <= 0 {
		return "—"
	}
	h, m, s := seconds/3600, (seconds%3600)/60, seconds%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

// fmtPace renders a session's pace as "8:32/mi", or "" if it can't be computed.
func fmtPace(seconds int, miles float64) string {
	if seconds <= 0 || miles <= 0 {
		return ""
	}
	return fmtClock(int(float64(seconds)/miles)) + "/mi"
}

// fmtSpeed renders a session's average speed as "12.4 mph", or "".
func fmtSpeed(seconds int, miles float64) string {
	if seconds <= 0 || miles <= 0 {
		return ""
	}
	return fmt.Sprintf("%.1f mph", miles/(float64(seconds)/3600))
}
