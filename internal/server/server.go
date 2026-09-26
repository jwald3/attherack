// Package server is the web UI: HTTP routes, page handlers and the HTMX
// fragments they swap in. Each tab's handlers and view models live in their
// own file.
package server

import (
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"

	"github.com/jwald3/attherack/internal/coach"
	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/exercise"
	"github.com/jwald3/attherack/internal/store"
	"github.com/jwald3/attherack/web"
)

// App holds shared server state.
type App struct {
	store   *store.Store
	lib     *exercise.Library
	tmpl    *template.Template
	baseURL string // Anthropic API base URL override, passed to the coach

	mu     sync.RWMutex
	apiKey string       // current active key ("" = chat disabled)
	envKey bool         // true when the key came from ANTHROPIC_API_KEY (UI is read-only)
	agent  *coach.Agent // rebuilt whenever the key changes
}

// New builds the app around an open store and exercise library.
// anthropicBaseURL overrides the API host ("" = the real API). The coach stays
// disabled until InitAPIKey (or the settings panel) supplies a key.
func New(st *store.Store, lib *exercise.Library, anthropicBaseURL string) (*App, error) {
	tmpl, err := parseTemplates()
	if err != nil {
		return nil, err
	}
	return &App{store: st, lib: lib, tmpl: tmpl, baseURL: anthropicBaseURL}, nil
}

// Handler returns the app's routes.
func (app *App) Handler() http.Handler {
	mux := http.NewServeMux()

	// Coach (home)
	mux.HandleFunc("GET /", app.handleCoachHome)
	mux.HandleFunc("GET /c/{id}", app.handleCoachThread)
	mux.HandleFunc("POST /threads/{id}/delete", app.handleDeleteThread)
	mux.HandleFunc("POST /threads/{id}/rename", app.handleRenameThread)
	mux.HandleFunc("POST /chat", app.handleChat)
	mux.HandleFunc("GET /chat/msg/{id}", app.handleChatMessage)
	mux.HandleFunc("GET /chat/img/{id}", app.handleChatImage)
	mux.HandleFunc("GET /settings", app.handleSettingsFragment)
	mux.HandleFunc("POST /settings/key", app.handleSaveKey)
	mux.HandleFunc("POST /settings/key/delete", app.handleClearKey)

	// Training
	mux.HandleFunc("GET /training", app.handleTrainingPage)
	mux.HandleFunc("GET /log", app.handleLogFragment)
	mux.HandleFunc("POST /sets", app.handleAddSet)
	mux.HandleFunc("POST /sets/{id}/delete", app.handleDeleteSet)
	mux.HandleFunc("GET /exercises", app.handleExerciseSearch)
	mux.HandleFunc("GET /exercise", app.handleExerciseDetail)
	mux.HandleFunc("POST /exercises", app.handleAddExercise)
	mux.HandleFunc("POST /exercises/suggest", app.handleSuggestExercise)

	// Programs
	mux.HandleFunc("GET /programs", app.handleProgramsPage)
	mux.HandleFunc("POST /programs", app.handleAddProgram)
	mux.HandleFunc("POST /programs/{id}/delete", app.handleDeleteProgram)
	mux.HandleFunc("POST /programs/{id}/start", app.handleStartProgram)

	// Cardio
	mux.HandleFunc("GET /cardio", app.handleCardioPage)
	mux.HandleFunc("GET /cardio/type", app.handleCardioDetail)
	mux.HandleFunc("POST /cardio", app.handleAddCardio)
	mux.HandleFunc("POST /cardio/{id}/delete", app.handleDeleteCardio)

	// Diet
	mux.HandleFunc("GET /diet", app.handleDietPage)
	mux.HandleFunc("POST /diet", app.handleAddFood)
	mux.HandleFunc("POST /diet/{id}/delete", app.handleDeleteFood)

	// Supplements
	mux.HandleFunc("GET /supplements", app.handleSupplementsPage)
	mux.HandleFunc("POST /supplements", app.handleAddSupplement)
	mux.HandleFunc("POST /supplements/{id}/delete", app.handleDeleteSupplement)

	// Bodyweight
	mux.HandleFunc("GET /bodyweight", app.handleBodyweightPage)
	mux.HandleFunc("POST /bodyweight", app.handleAddBodyweight)
	mux.HandleFunc("POST /bodyweight/{date}/delete", app.handleDeleteBodyweight)

	// Progress (measurements + photos)
	mux.HandleFunc("GET /progress", app.handleProgressPage)
	mux.HandleFunc("POST /measurements", app.handleAddMeasurement)
	mux.HandleFunc("POST /measurements/{site}/{date}/delete", app.handleDeleteMeasurement)
	mux.HandleFunc("POST /progress/photos", app.handleAddPhoto)
	mux.HandleFunc("POST /progress/photos/{id}/delete", app.handleDeletePhoto)
	mux.HandleFunc("GET /progress/photos/{id}/img", app.handleProgressPhoto)

	// Embedded static assets (web/static/*).
	mux.Handle("GET /static/", http.StripPrefix("/static/", http.FileServer(http.FS(web.Static))))
	return mux
}

// --- Helpers shared by the handlers ---

// render executes a named template, logging (not surfacing) failures: by the
// time a template fails, part of the response may already be written.
func (app *App) render(w http.ResponseWriter, name string, data any) {
	if err := app.tmpl.ExecuteTemplate(w, name, data); err != nil {
		log.Printf("render %s: %v", name, err)
	}
}

// writeHTML writes a small inline HTML fragment.
func writeHTML(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(s))
}

// pathID parses the {id} path segment.
func pathID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

// formDate returns the form's "date" field, defaulting to today.
func formDate(r *http.Request) string {
	if date := strings.TrimSpace(r.FormValue("date")); date != "" {
		return date
	}
	return dates.Today()
}

// serverError reports a failed store call.
func serverError(w http.ResponseWriter, err error) {
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

// badID reports an unparseable {id}.
func badID(w http.ResponseWriter) {
	http.Error(w, "bad id", http.StatusBadRequest)
}
