package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// --- Programs tab (reusable workout templates) ---

// programsData drives the Programs tab (list of programs + the create form).
type programsData struct {
	PageTitle string
	Active    string
	Today     string
	Programs  []store.Program
}

func (app *App) handleProgramsPage(w http.ResponseWriter, r *http.Request) {
	programs, err := app.store.ListPrograms()
	if err != nil {
		serverError(w, err)
		return
	}
	app.render(w, "programs.html", programsData{
		PageTitle: "Programs",
		Active:    "programs",
		Today:     dates.Today(),
		Programs:  programs,
	})
}

// writeProgramList re-renders just the list of program cards (for HTMX swaps).
func (app *App) writeProgramList(w http.ResponseWriter) {
	programs, err := app.store.ListPrograms()
	if err != nil {
		serverError(w, err)
		return
	}
	app.render(w, "programs_list.html", programs)
}

// handleAddProgram creates a program from the form. Exercise rows arrive as
// index-aligned repeated fields (exercise, sets, reps, weight, rpe); blank rows
// are skipped. Requires a name and at least one exercise.
func (app *App) handleAddProgram(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	notes := strings.TrimSpace(r.FormValue("notes"))
	exs := programExercisesFromForm(r)
	if len(exs) == 0 {
		http.Error(w, "add at least one exercise", http.StatusBadRequest)
		return
	}
	if _, err := app.store.CreateProgram(name, notes, exs); err != nil {
		serverError(w, err)
		return
	}
	// Let the client reset the form (mirrors exercise-added / settings-changed).
	w.Header().Set("HX-Trigger", "program-added")
	app.writeProgramList(w)
}

// programExercisesFromForm reads the index-aligned exercise rows.
func programExercisesFromForm(r *http.Request) []store.ProgramExercise {
	setsF := r.Form["sets"]
	repsF := r.Form["reps"]
	weightF := r.Form["weight"]
	rpeF := r.Form["rpe"]
	field := func(vals []string, i int) string {
		if i < len(vals) {
			return strings.TrimSpace(vals[i])
		}
		return ""
	}

	var exs []store.ProgramExercise
	for i, ex := range r.Form["exercise"] {
		ex = strings.TrimSpace(ex)
		if ex == "" {
			continue
		}
		e := store.ProgramExercise{Exercise: ex, Sets: 1}
		if n, err := strconv.Atoi(field(setsF, i)); err == nil && n > 0 {
			e.Sets = n
		}
		e.Reps, _ = strconv.Atoi(field(repsF, i))
		e.Weight, _ = strconv.ParseFloat(field(weightF, i), 64)
		if v := field(rpeF, i); v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				e.RPE = &f
			}
		}
		exs = append(exs, e)
	}
	return exs
}

func (app *App) handleDeleteProgram(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		badID(w)
		return
	}
	if err := app.store.DeleteProgram(id); err != nil {
		serverError(w, err)
		return
	}
	app.writeProgramList(w)
}

// handleStartProgram logs a program's sets to today and returns a confirmation
// fragment linking to the Training tab.
func (app *App) handleStartProgram(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		badID(w)
		return
	}
	n, ok, err := app.store.StartProgram(id, dates.Today())
	if err != nil {
		serverError(w, err)
		return
	}
	if !ok {
		writeHTML(w, `<span class="prog-started err">Program not found.</span>`)
		return
	}
	setWord := "sets"
	if n == 1 {
		setWord = "set"
	}
	writeHTML(w, fmt.Sprintf(
		`<span class="prog-started">Logged %d %s to today · <a href="/training">view →</a></span>`,
		n, setWord))
}
