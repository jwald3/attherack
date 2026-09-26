package server

import (
	"encoding/json"
	"errors"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/jwald3/attherack/internal/chart"
	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/exercise"
	"github.com/jwald3/attherack/internal/store"
)

// --- Training tab: workout log ---

// trainingData is the model for the Training tab (log + exercise library).
type trainingData struct {
	PageTitle        string
	Active           string
	Workouts         []store.Workout
	Today            string
	InitialExercises []exercise.Exercise // empty on load; the panel prompts to search
	Muscles          []exercise.Facet    // suggestion chips
	Equipment        []exercise.Facet
}

func (app *App) handleTrainingPage(w http.ResponseWriter, r *http.Request) {
	workouts, err := app.store.ListWorkouts(30)
	if err != nil {
		serverError(w, err)
		return
	}
	muscles, equipment := app.lib.Facets()
	app.render(w, "training.html", trainingData{
		PageTitle: "Training",
		Active:    "training",
		Workouts:  workouts,
		Today:     dates.Today(),
		Muscles:   muscles,
		Equipment: equipment,
	})
}

// handleLogFragment re-renders just the workout log panel (for HTMX swaps).
func (app *App) handleLogFragment(w http.ResponseWriter, r *http.Request) {
	workouts, err := app.store.ListWorkouts(30)
	if err != nil {
		serverError(w, err)
		return
	}
	app.render(w, "log.html", workouts)
}

func (app *App) handleAddSet(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("exercise"))
	if name == "" {
		http.Error(w, "exercise is required", http.StatusBadRequest)
		return
	}
	weight, _ := strconv.ParseFloat(r.FormValue("weight"), 64)
	reps, _ := strconv.Atoi(r.FormValue("reps"))
	var rpe *float64
	if v := strings.TrimSpace(r.FormValue("rpe")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			rpe = &f
		}
	}
	if _, err := app.store.LogSet(formDate(r), name, weight, reps, rpe); err != nil {
		serverError(w, err)
		return
	}
	app.handleLogFragment(w, r)
}

func (app *App) handleDeleteSet(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		badID(w)
		return
	}
	if err := app.store.DeleteSet(id); err != nil {
		serverError(w, err)
		return
	}
	app.handleLogFragment(w, r)
}

// --- Training tab: exercise library ---

func (app *App) handleExerciseSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hits := app.lib.Search(q.Get("query"), q.Get("muscle"), q.Get("equipment"), 40)
	app.render(w, "exercises.html", hits)
}

// handleAddExercise creates a user-defined exercise and returns a status note
// (the new movement is searchable immediately).
func (app *App) handleAddExercise(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		writeHTML(w, `<div class="ex-add-msg err">Name is required.</div>`)
		return
	}
	primary := exercise.SplitList(r.FormValue("primary_muscles"))
	secondary := exercise.SplitList(r.FormValue("secondary_muscles"))
	ex, err := app.store.AddCustomExercise(name, r.FormValue("equipment"), r.FormValue("level"), r.FormValue("category"), primary, secondary)
	if err != nil {
		msg := "Could not add exercise."
		if errors.Is(err, store.ErrExerciseExists) {
			msg = "You already have an exercise with that name."
		}
		writeHTML(w, `<div class="ex-add-msg err">`+html.EscapeString(msg)+`</div>`)
		return
	}
	app.lib.Add(ex)

	// Return a success note and trigger a form reset.
	w.Header().Set("HX-Trigger", "exercise-added")
	writeHTML(w, `<div class="ex-add-msg ok">Added “`+html.EscapeString(ex.Name)+`.” It's now searchable and loggable.</div>`)
}

// handleSuggestExercise uses the cheap model to infer metadata from a name and
// returns it as JSON for the add-exercise form to populate.
func (app *App) handleSuggestExercise(w http.ResponseWriter, r *http.Request) {
	agent := app.getAgent()
	w.Header().Set("Content-Type", "application/json")
	if agent == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"Add your API key (API key button on the Coach tab) to use AI fill."}`))
		return
	}
	if err := r.ParseForm(); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"Enter a name first."}`))
		return
	}
	sug, err := agent.SuggestExercise(name)
	if err != nil {
		log.Printf("suggest exercise: %v", err)
		w.WriteHeader(http.StatusBadGateway)
		_ = json.NewEncoder(w).Encode(map[string]string{"error": "AI fill failed: " + err.Error()})
		return
	}
	_ = json.NewEncoder(w).Encode(sug)
}

// --- Exercise history drawer ---

// exerciseDetailData drives the exercise-history drawer.
type exerciseDetailData struct {
	Name       string
	Meta       *exercise.Exercise // library metadata if known
	TotalSets  int
	Sessions   int
	BestWeight float64
	BestReps   int
	BestE1RM   float64 // Epley estimate off the best-weight set
	FirstDate  string
	LastDate   string
	Chart      chart.Data
	Days       []exerciseDay // grouped, newest-first
}

// exerciseDay is one date's sets for an exercise.
type exerciseDay struct {
	Date string
	Sets []store.Set
}

func (app *App) exerciseDetailData(name string) exerciseDetailData {
	d := exerciseDetailData{Name: name}
	if ex, ok := app.lib.ByName(name); ok {
		d.Meta = &ex
	}

	hist, _ := app.store.ExerciseHistory(name, 500)
	d.TotalSets = len(hist)

	// Group by date (hist is newest-first) and compute bests.
	dayIdx := map[string]int{}
	for _, h := range hist {
		if _, ok := dayIdx[h.Date]; !ok {
			dayIdx[h.Date] = len(d.Days)
			d.Days = append(d.Days, exerciseDay{Date: h.Date})
		}
		i := dayIdx[h.Date]
		d.Days[i].Sets = append(d.Days[i].Sets, h.Set)
		// Best set = heaviest weight; tie-break on reps.
		if h.Set.Weight > d.BestWeight || (h.Set.Weight == d.BestWeight && h.Set.Reps > d.BestReps) {
			d.BestWeight = h.Set.Weight
			d.BestReps = h.Set.Reps
		}
	}
	d.Sessions = len(d.Days)
	if d.BestWeight > 0 && d.BestReps > 0 {
		// Epley 1RM estimate.
		d.BestE1RM = d.BestWeight * (1 + float64(d.BestReps)/30.0)
	}
	if len(hist) > 0 {
		d.LastDate = hist[0].Date
		d.FirstDate = hist[len(hist)-1].Date
	}

	// Progression chart: heaviest set per day, oldest first.
	top, _ := app.store.ExerciseDailyTop(name)
	pts := make([]chart.Point, 0, len(top))
	for _, t := range top {
		pts = append(pts, chart.Point{Date: t.Date, Value: t.Set.Weight})
	}
	d.Chart = chart.Build(pts)
	return d
}

// handleExerciseDetail renders the history drawer for one exercise.
func (app *App) handleExerciseDetail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	app.render(w, "exercise_detail.html", app.exerciseDetailData(name))
}
