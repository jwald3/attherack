package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Limits on photos attached to a chat message. The browser downscales before
// upload, so these are backstops; the API itself caps images at 5 MB each.
const (
	maxChatImages     = 4
	maxChatImageBytes = 5 << 20
	maxChatFormBytes  = 24 << 20
)

// trainingData is the model for the Training tab (log + exercise library).
type trainingData struct {
	PageTitle        string
	Active           string
	Workouts         []Workout
	Today            string
	InitialExercises []Exercise // empty on load; the panel prompts to search
	Muscles          []Facet    // suggestion chips
	Equipment        []Facet
}

// settingsData drives the coach settings/API-key sub-panel.
type settingsData struct {
	Enabled   bool
	EnvLocked bool   // key came from ANTHROPIC_API_KEY; UI can't change it
	Masked    string // masked preview of the active key, or ""
	Msg       string // optional status message (shown after save/clear)
	Kind      string // "ok" or "err" — styles the status message
}

func (app *App) handleTrainingPage(w http.ResponseWriter, r *http.Request) {
	workouts, err := app.store.listWorkouts(30)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	muscles, equipment := app.lib.Facets()
	data := trainingData{
		PageTitle: "Training",
		Active:    "training",
		Workouts:  workouts,
		Today:     today(),
		Muscles:   muscles,
		Equipment: equipment,
	}
	if err := app.tmpl.ExecuteTemplate(w, "training.html", data); err != nil {
		log.Printf("render training: %v", err)
	}
}

// --- Coach tab (home) ---

// coachData is the model for the coach chat page.
type coachData struct {
	PageTitle   string
	Active      string
	Today       string
	ChatEnabled bool
	Settings    settingsData
	Threads     []ChatThread
	Thread      *ChatThread // nil = new, unsaved conversation
	Messages    []ChatMessage
}

// threadListData renders the sidebar list with the active thread highlighted.
type threadListData struct {
	Threads  []ChatThread
	ActiveID int64
	OOB      bool // render as an htmx out-of-band swap
}

func (d coachData) ThreadList() threadListData {
	var id int64
	if d.Thread != nil {
		id = d.Thread.ID
	}
	return threadListData{Threads: d.Threads, ActiveID: id}
}

func (app *App) renderCoach(w http.ResponseWriter, thread *ChatThread) {
	threads, _ := app.store.listThreads(200)
	data := coachData{
		PageTitle:   "Coach",
		Active:      "coach",
		Today:       today(),
		ChatEnabled: app.chatEnabled(),
		Settings:    app.settingsData(),
		Threads:     threads,
		Thread:      thread,
	}
	if thread != nil {
		data.PageTitle = thread.Title
		data.Messages, _ = app.store.listChatMessages(thread.ID, 500)
	}
	if err := app.tmpl.ExecuteTemplate(w, "coach.html", data); err != nil {
		log.Printf("render coach: %v", err)
	}
}

// handleCoachHome shows a fresh conversation (the thread is created on first send).
func (app *App) handleCoachHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	app.renderCoach(w, nil)
}

// handleCoachThread shows an existing conversation.
func (app *App) handleCoachThread(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	t, ok := app.store.getThread(id)
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	app.renderCoach(w, &t)
}

func (app *App) writeThreadList(w http.ResponseWriter, activeID int64, oob bool) {
	threads, _ := app.store.listThreads(200)
	if err := app.tmpl.ExecuteTemplate(w, "thread_list.html", threadListData{Threads: threads, ActiveID: activeID, OOB: oob}); err != nil {
		log.Printf("render thread list: %v", err)
	}
}

// handleDeleteThread removes a conversation. Deleting the one on screen sends
// the browser back to a new chat; otherwise just the sidebar is refreshed.
func (app *App) handleDeleteThread(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := app.store.deleteThread(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	current, _ := strconv.ParseInt(r.FormValue("current"), 10, 64)
	if current == id {
		w.Header().Set("HX-Redirect", "/")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	app.writeThreadList(w, current, false)
}

// handleRenameThread sets a conversation's title.
func (app *App) handleRenameThread(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	title := strings.TrimSpace(r.FormValue("title"))
	if title != "" {
		if err := app.store.renameThread(id, truncateTitle(title, 80)); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	current, _ := strconv.ParseInt(r.FormValue("current"), 10, 64)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	app.writeThreadList(w, current, false)
}

// truncateTitle shortens s to at most n runes, cutting at a word boundary.
func truncateTitle(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

func (app *App) settingsData() settingsData {
	return settingsData{
		Enabled:   app.chatEnabled(),
		EnvLocked: app.envLocked(),
		Masked:    app.maskedKey(),
	}
}

// handleLogFragment re-renders just the workout log panel (for HTMX swaps).
func (app *App) handleLogFragment(w http.ResponseWriter, r *http.Request) {
	workouts, err := app.store.listWorkouts(30)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := app.tmpl.ExecuteTemplate(w, "log.html", workouts); err != nil {
		log.Printf("render log: %v", err)
	}
}

func (app *App) handleAddSet(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	exercise := strings.TrimSpace(r.FormValue("exercise"))
	if exercise == "" {
		http.Error(w, "exercise is required", http.StatusBadRequest)
		return
	}
	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" {
		date = today()
	}
	weight, _ := strconv.ParseFloat(r.FormValue("weight"), 64)
	reps, _ := strconv.Atoi(r.FormValue("reps"))
	var rpe *float64
	if v := strings.TrimSpace(r.FormValue("rpe")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			rpe = &f
		}
	}
	if _, err := app.store.logSet(date, exercise, weight, reps, rpe); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.handleLogFragment(w, r)
}

func (app *App) handleDeleteSet(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := app.store.deleteSet(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.handleLogFragment(w, r)
}

// --- Programs (reusable workout templates) ---

// programsData drives the Programs tab (list of programs + the create form).
type programsData struct {
	PageTitle string
	Active    string
	Today     string
	Programs  []Program
}

func (app *App) handleProgramsPage(w http.ResponseWriter, r *http.Request) {
	programs, err := app.store.listPrograms()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := programsData{
		PageTitle: "Programs",
		Active:    "programs",
		Today:     today(),
		Programs:  programs,
	}
	if err := app.tmpl.ExecuteTemplate(w, "programs.html", data); err != nil {
		log.Printf("render programs: %v", err)
	}
}

// writeProgramList re-renders just the list of program cards (for HTMX swaps).
func (app *App) writeProgramList(w http.ResponseWriter) {
	programs, err := app.store.listPrograms()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := app.tmpl.ExecuteTemplate(w, "programs_list.html", programs); err != nil {
		log.Printf("render program list: %v", err)
	}
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

	exercises := r.Form["exercise"]
	setsF := r.Form["sets"]
	repsF := r.Form["reps"]
	weightF := r.Form["weight"]
	rpeF := r.Form["rpe"]

	var exs []ProgramExercise
	for i, ex := range exercises {
		ex = strings.TrimSpace(ex)
		if ex == "" {
			continue
		}
		e := ProgramExercise{Exercise: ex, Sets: 1}
		if i < len(setsF) {
			if n, err := strconv.Atoi(strings.TrimSpace(setsF[i])); err == nil && n > 0 {
				e.Sets = n
			}
		}
		if i < len(repsF) {
			e.Reps, _ = strconv.Atoi(strings.TrimSpace(repsF[i]))
		}
		if i < len(weightF) {
			e.Weight, _ = strconv.ParseFloat(strings.TrimSpace(weightF[i]), 64)
		}
		if i < len(rpeF) {
			if v := strings.TrimSpace(rpeF[i]); v != "" {
				if f, err := strconv.ParseFloat(v, 64); err == nil {
					e.RPE = &f
				}
			}
		}
		exs = append(exs, e)
	}
	if len(exs) == 0 {
		http.Error(w, "add at least one exercise", http.StatusBadRequest)
		return
	}
	if _, err := app.store.createProgram(name, notes, exs); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	// Let the client reset the form (mirrors exercise-added / settings-changed).
	w.Header().Set("HX-Trigger", "program-added")
	app.writeProgramList(w)
}

func (app *App) handleDeleteProgram(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := app.store.deleteProgram(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.writeProgramList(w)
}

// handleStartProgram logs a program's sets to today and returns a confirmation
// fragment linking to the Training tab.
func (app *App) handleStartProgram(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	n, ok, err := app.store.startProgram(id, today())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if !ok {
		w.Write([]byte(`<span class="prog-started err">Program not found.</span>`))
		return
	}
	setWord := "sets"
	if n == 1 {
		setWord = "set"
	}
	w.Write([]byte(fmt.Sprintf(
		`<span class="prog-started">Logged %d %s to today · <a href="/training">view →</a></span>`,
		n, setWord)))
}

// bodyweightData drives the bodyweight tab (chart + list).
type bodyweightData struct {
	PageTitle string
	Active    string
	Today     string
	Entries   []BodyweightEntry // recent, newest-first (for the list)
	Latest    *BodyweightEntry
	ChangeDir string // "down" | "up" | "" — styling hint for the total change
	Chart     ChartData
}

func (app *App) bodyweightData() bodyweightData {
	listEntries, _ := app.store.listBodyweight(60)  // list shows recent
	allEntries, _ := app.store.listBodyweight(3650) // chart uses full history
	d := bodyweightData{
		PageTitle: "Bodyweight",
		Active:    "bodyweight",
		Today:     today(),
		Entries:   listEntries,
		Chart:     buildChart(allEntries),
	}
	if len(listEntries) > 0 {
		latest := listEntries[0]
		d.Latest = &latest
	}
	if d.Chart.HasData {
		switch {
		case d.Chart.Change < 0:
			d.ChangeDir = "down"
		case d.Chart.Change > 0:
			d.ChangeDir = "up"
		}
	}
	return d
}

// handleBodyweightPage renders the full Bodyweight tab.
func (app *App) handleBodyweightPage(w http.ResponseWriter, r *http.Request) {
	if err := app.tmpl.ExecuteTemplate(w, "bodyweight_page.html", app.bodyweightData()); err != nil {
		log.Printf("render bodyweight page: %v", err)
	}
}

// bodyweightContent renders just the chart+stats+list fragment (for HTMX swaps).
func (app *App) bodyweightContent(w http.ResponseWriter) {
	if err := app.tmpl.ExecuteTemplate(w, "bodyweight_content.html", app.bodyweightData()); err != nil {
		log.Printf("render bodyweight content: %v", err)
	}
}

func (app *App) handleAddBodyweight(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	weight, err := strconv.ParseFloat(strings.TrimSpace(r.FormValue("weight")), 64)
	if err != nil || weight <= 0 {
		app.bodyweightContent(w)
		return
	}
	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" {
		date = today()
	}
	if err := app.store.logBodyweight(date, weight); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.bodyweightContent(w)
}

func (app *App) handleDeleteBodyweight(w http.ResponseWriter, r *http.Request) {
	date := r.PathValue("date")
	if err := app.store.deleteBodyweight(date); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.bodyweightContent(w)
}

// exerciseDetailData drives the exercise-history drawer.
type exerciseDetailData struct {
	Name       string
	Meta       *Exercise // library metadata if known
	TotalSets  int
	Sessions   int
	BestWeight float64
	BestReps   int
	BestE1RM   float64 // Epley estimate off the best-weight set
	FirstDate  string
	LastDate   string
	Chart      ChartData
	Days       []exerciseDay // grouped, newest-first
}

// exerciseDay is one date's sets for an exercise.
type exerciseDay struct {
	Date string
	Sets []Set
}

func (app *App) exerciseDetailData(name string) exerciseDetailData {
	d := exerciseDetailData{Name: name}
	if ex, ok := app.lib.byName(name); ok {
		d.Meta = &ex
	}

	hist, _ := app.store.exerciseHistory(name, 500)
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
	top, _ := app.store.exerciseDailyTop(name)
	pts := make([]ChartPoint, 0, len(top))
	for _, t := range top {
		pts = append(pts, ChartPoint{Date: t.Date, Value: t.Set.Weight})
	}
	d.Chart = buildChartPoints(pts)
	return d
}

// handleExerciseDetail renders the history drawer for one exercise.
func (app *App) handleExerciseDetail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if err := app.tmpl.ExecuteTemplate(w, "exercise_detail.html", app.exerciseDetailData(name)); err != nil {
		log.Printf("render exercise detail: %v", err)
	}
}

// --- Cardio tab ---

type cardioData struct {
	PageTitle    string
	Active       string
	Today        string
	Sessions     []CardioSession
	WeekMiles    float64
	WeekMinutes  int
	WeekCount    int
	MonthMiles   float64
	MonthMinutes int
}

func (app *App) cardioData() cardioData {
	sessions, _ := app.store.listCardio(200)
	wMi, wSec, wN := app.store.cardioTotalsSince(daysAgo(7))
	mMi, mSec, _ := app.store.cardioTotalsSince(daysAgo(30))
	return cardioData{
		PageTitle:    "Cardio",
		Active:       "cardio",
		Today:        today(),
		Sessions:     sessions,
		WeekMiles:    wMi,
		WeekMinutes:  wSec / 60,
		WeekCount:    wN,
		MonthMiles:   mMi,
		MonthMinutes: mSec / 60,
	}
}

func (app *App) handleCardioPage(w http.ResponseWriter, r *http.Request) {
	if err := app.tmpl.ExecuteTemplate(w, "cardio_page.html", app.cardioData()); err != nil {
		log.Printf("render cardio page: %v", err)
	}
}

func (app *App) cardioContent(w http.ResponseWriter) {
	if err := app.tmpl.ExecuteTemplate(w, "cardio_content.html", app.cardioData()); err != nil {
		log.Printf("render cardio content: %v", err)
	}
}

func (app *App) handleAddCardio(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	ctype := strings.TrimSpace(r.FormValue("type"))
	if ctype == "" {
		app.cardioContent(w)
		return
	}
	// Duration accepted in minutes from the form; stored as seconds.
	minutes, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("minutes")), 64)
	miles, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("miles")), 64)
	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" {
		date = today()
	}
	if _, err := app.store.logCardio(date, ctype, int(minutes*60), miles); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.cardioContent(w)
}

func (app *App) handleDeleteCardio(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := app.store.deleteCardio(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.cardioContent(w)
}

// cardioDetailData drives the cardio-type history drawer.
type cardioDetailData struct {
	Name          string
	Sessions      []CardioSession // newest-first
	TotalMiles    float64
	TotalSeconds  int
	AvgPaceSec    int // seconds per mile over sessions with both distance & time
	BestPaceSec   int // fastest single-session pace
	LongestMiles  float64
	LongestSecond int
	FirstDate     string
	LastDate      string
	ChartLabel    string
	ChartFirst    string // formatted first/last chart values
	ChartLast     string
	LowerIsBetter bool // true for pace
	Improved      bool // first → last moved in the good direction
	Chart         ChartData
}

func (app *App) cardioDetailData(name string) cardioDetailData {
	d := cardioDetailData{Name: name}
	d.Sessions, _ = app.store.cardioHistoryByType(name)
	if len(d.Sessions) == 0 {
		return d
	}
	// Use the stored casing rather than whatever was in the query string.
	d.Name = d.Sessions[0].Type
	d.LastDate = d.Sessions[0].Date
	d.FirstDate = d.Sessions[len(d.Sessions)-1].Date

	var paceMiles float64
	var paceSeconds int
	for _, c := range d.Sessions {
		d.TotalMiles += c.DistanceMiles
		d.TotalSeconds += c.DurationSeconds
		if c.DistanceMiles > d.LongestMiles {
			d.LongestMiles = c.DistanceMiles
		}
		if c.DurationSeconds > d.LongestSecond {
			d.LongestSecond = c.DurationSeconds
		}
		if c.DistanceMiles > 0 && c.DurationSeconds > 0 {
			paceMiles += c.DistanceMiles
			paceSeconds += c.DurationSeconds
			p := int(float64(c.DurationSeconds) / c.DistanceMiles)
			if d.BestPaceSec == 0 || p < d.BestPaceSec {
				d.BestPaceSec = p
			}
		}
	}
	if paceMiles > 0 {
		d.AvgPaceSec = int(float64(paceSeconds) / paceMiles)
	}

	// Chart pace when at least two sessions have one (lower is better); else
	// distance per session; else duration.
	pts := make([]ChartPoint, 0, len(d.Sessions))
	for _, c := range d.Sessions {
		if c.DistanceMiles > 0 && c.DurationSeconds > 0 {
			pts = append(pts, ChartPoint{Date: c.Date, Value: float64(c.DurationSeconds) / c.DistanceMiles})
		}
	}
	isPace := len(pts) > 1
	if isPace {
		d.ChartLabel = "Pace per session (/mi)"
		d.LowerIsBetter = true
	} else if d.TotalMiles > 0 {
		pts = pts[:0]
		d.ChartLabel = "Distance per session (mi)"
		for _, c := range d.Sessions {
			if c.DistanceMiles > 0 {
				pts = append(pts, ChartPoint{Date: c.Date, Value: c.DistanceMiles})
			}
		}
	} else {
		pts = pts[:0]
		d.ChartLabel = "Duration per session (min)"
		for _, c := range d.Sessions {
			if c.DurationSeconds > 0 {
				pts = append(pts, ChartPoint{Date: c.Date, Value: float64(c.DurationSeconds) / 60})
			}
		}
	}
	// Sessions are newest-first; the chart's sort is stable, so reverse to keep
	// same-day sessions in logged order.
	for i, j := 0, len(pts)-1; i < j; i, j = i+1, j-1 {
		pts[i], pts[j] = pts[j], pts[i]
	}
	if len(pts) > 1 {
		if isPace {
			d.Chart = buildChartPointsFmt(pts, func(v float64) string { return fmtClock(int(v)) })
			d.ChartFirst, d.ChartLast = fmtClock(int(d.Chart.First)), fmtClock(int(d.Chart.Last))
		} else {
			d.Chart = buildChartPoints(pts)
			d.ChartFirst, d.ChartLast = fmt.Sprintf("%.1f", d.Chart.First), fmt.Sprintf("%.1f", d.Chart.Last)
		}
		d.Improved = (d.Chart.Change < 0) == d.LowerIsBetter
	}
	return d
}

// handleCardioDetail renders the history drawer for one cardio type.
func (app *App) handleCardioDetail(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		http.Error(w, "name required", http.StatusBadRequest)
		return
	}
	if err := app.tmpl.ExecuteTemplate(w, "cardio_detail.html", app.cardioDetailData(name)); err != nil {
		log.Printf("render cardio detail: %v", err)
	}
}

// --- Supplements tab ---

type supplementsData struct {
	PageTitle  string
	Active     string
	Today      string
	Regulars   []SupplementSummary // everything ever logged, for quick-log + consistency
	TodayCount int
	Days       []supplementDay // recent history grouped by date, newest first
}

// supplementDay is one date's doses.
type supplementDay struct {
	Date string
	Logs []SupplementLog
}

func (app *App) supplementsData() supplementsData {
	d := supplementsData{PageTitle: "Supplements", Active: "supplements", Today: today()}
	// Consistency window: the last 30 days including today.
	d.Regulars, _ = app.store.supplementSummaries(daysAgo(29))
	for _, r := range d.Regulars {
		if r.TakenToday {
			d.TodayCount++
		}
	}
	logs, _ := app.store.listSupplementLogs(300)
	idx := map[string]int{}
	for _, l := range logs {
		i, ok := idx[l.Date]
		if !ok {
			i = len(d.Days)
			idx[l.Date] = i
			d.Days = append(d.Days, supplementDay{Date: l.Date})
		}
		d.Days[i].Logs = append(d.Days[i].Logs, l)
	}
	return d
}

func (app *App) handleSupplementsPage(w http.ResponseWriter, r *http.Request) {
	if err := app.tmpl.ExecuteTemplate(w, "supplements_page.html", app.supplementsData()); err != nil {
		log.Printf("render supplements page: %v", err)
	}
}

func (app *App) supplementsContent(w http.ResponseWriter) {
	if err := app.tmpl.ExecuteTemplate(w, "supplements_content.html", app.supplementsData()); err != nil {
		log.Printf("render supplements content: %v", err)
	}
}

func (app *App) handleAddSupplement(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		app.supplementsContent(w)
		return
	}
	amount, _ := strconv.ParseFloat(strings.TrimSpace(r.FormValue("amount")), 64)
	date := strings.TrimSpace(r.FormValue("date"))
	if date == "" {
		date = today()
	}
	if _, err := app.store.logSupplement(date, name, amount, strings.TrimSpace(r.FormValue("unit"))); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.supplementsContent(w)
}

func (app *App) handleDeleteSupplement(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := app.store.deleteSupplementLog(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.supplementsContent(w)
}

// --- Diet tab ---

type dietData struct {
	PageTitle string
	Active    string
	Today     string
	Meal      string // meal to preselect in the form, based on time of day
	Names     []string
	Days      []dietDay // last 30 days, newest first
}

// dietDay is one date's food with macro totals over the entries that have them.
type dietDay struct {
	Date      string
	Foods     []FoodLog
	HasMacros bool
	Calories  float64
	Protein   float64
	Carbs     float64
	Fat       float64
}

func (app *App) dietData() dietData {
	d := dietData{PageTitle: "Diet", Active: "diet", Today: today(), Meal: mealForNow(), Names: app.store.recentFoodNames(200)}
	foods, _ := app.store.listFoodSince(daysAgo(29))
	idx := map[string]int{}
	for _, f := range foods {
		i, ok := idx[f.Date]
		if !ok {
			i = len(d.Days)
			idx[f.Date] = i
			d.Days = append(d.Days, dietDay{Date: f.Date})
		}
		day := &d.Days[i]
		day.Foods = append(day.Foods, f)
		for _, m := range []struct {
			v   *float64
			sum *float64
		}{{f.Calories, &day.Calories}, {f.Protein, &day.Protein}, {f.Carbs, &day.Carbs}, {f.Fat, &day.Fat}} {
			if m.v != nil {
				*m.sum += *m.v
				day.HasMacros = true
			}
		}
	}
	return d
}

// TotalsSummary renders the day's macro totals, e.g.
// "1850 kcal · 140g protein · 60g fat", skipping macros nobody recorded.
func (d dietDay) TotalsSummary() string {
	var parts []string
	for _, m := range []struct {
		v      float64
		suffix string
	}{{d.Calories, " kcal"}, {d.Protein, "g protein"}, {d.Carbs, "g carbs"}, {d.Fat, "g fat"}} {
		if m.v > 0 {
			parts = append(parts, fmt.Sprintf("%.0f%s", m.v, m.suffix))
		}
	}
	return strings.Join(parts, " · ")
}

// mealForNow guesses which meal is being logged from the local time.
func mealForNow() string {
	switch h := time.Now().Hour(); {
	case h >= 4 && h < 11:
		return "breakfast"
	case h >= 11 && h < 15:
		return "lunch"
	case h >= 17 && h < 22:
		return "dinner"
	default:
		return "snack"
	}
}

func (app *App) handleDietPage(w http.ResponseWriter, r *http.Request) {
	if err := app.tmpl.ExecuteTemplate(w, "diet_page.html", app.dietData()); err != nil {
		log.Printf("render diet page: %v", err)
	}
}

func (app *App) dietContent(w http.ResponseWriter) {
	if err := app.tmpl.ExecuteTemplate(w, "diet_content.html", app.dietData()); err != nil {
		log.Printf("render diet content: %v", err)
	}
}

// optFloat parses an optional numeric form field; blank or invalid means nil.
func optFloat(s string) *float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 {
		return nil
	}
	return &f
}

func (app *App) handleAddFood(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	f := FoodLog{
		Date:     strings.TrimSpace(r.FormValue("date")),
		Meal:     strings.TrimSpace(r.FormValue("meal")),
		Name:     strings.TrimSpace(r.FormValue("name")),
		Notes:    strings.TrimSpace(r.FormValue("notes")),
		Calories: optFloat(r.FormValue("calories")),
		Protein:  optFloat(r.FormValue("protein")),
		Carbs:    optFloat(r.FormValue("carbs")),
		Fat:      optFloat(r.FormValue("fat")),
	}
	if f.Name == "" {
		app.dietContent(w)
		return
	}
	if _, err := app.store.logFood(f); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.dietContent(w)
}

func (app *App) handleDeleteFood(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, "bad id", http.StatusBadRequest)
		return
	}
	if err := app.store.deleteFood(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	app.dietContent(w)
}

func (app *App) handleExerciseSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	hits := app.lib.Search(q.Get("query"), q.Get("muscle"), q.Get("equipment"), 40)
	if err := app.tmpl.ExecuteTemplate(w, "exercises.html", hits); err != nil {
		log.Printf("render exercises: %v", err)
	}
}

// handleAddExercise creates a user-defined exercise and returns the updated
// search results (so the new movement appears immediately).
func (app *App) handleAddExercise(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<div class="ex-add-msg err">Name is required.</div>`))
		return
	}
	primary := splitList(r.FormValue("primary_muscles"))
	secondary := splitList(r.FormValue("secondary_muscles"))
	ex, err := app.store.addCustomExercise(name, r.FormValue("equipment"), r.FormValue("level"), r.FormValue("category"), primary, secondary)
	if err != nil {
		msg := "Could not add exercise."
		if err == errExerciseExists {
			msg = "You already have an exercise with that name."
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<div class="ex-add-msg err">` + html.EscapeString(msg) + `</div>`))
		return
	}
	app.lib.Add(ex)

	// Return the new exercise card plus a success note, and trigger a form reset.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", "exercise-added")
	w.Write([]byte(`<div class="ex-add-msg ok">Added “` + html.EscapeString(ex.Name) + `.” It's now searchable and loggable.</div>`))
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

func (app *App) handleChat(w http.ResponseWriter, r *http.Request) {
	agent := app.getAgent()
	if agent == nil {
		app.writeChatBubble(w, "assistant", "Chat is disabled. Add your Anthropic API key (API key button, bottom left) to enable Claude.", false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatFormBytes)
	if err := r.ParseMultipartForm(maxChatFormBytes); err != nil {
		// Plain (non-multipart) posts still work, e.g. from tests or curl.
		if !errors.Is(err, http.ErrNotMultipart) {
			http.Error(w, "That upload is too large. Try fewer or smaller photos.", http.StatusRequestEntityTooLarge)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	userMsg := strings.TrimSpace(r.FormValue("message"))
	images, err := readChatImages(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if userMsg == "" && len(images) == 0 {
		return
	}

	// Resolve the thread, creating one on the first message of a new chat.
	threadID, _ := strconv.ParseInt(r.FormValue("thread_id"), 10, 64)
	isNew := false
	if _, ok := app.store.getThread(threadID); !ok {
		title := truncateTitle(userMsg, 40)
		if title == "" {
			title = "Photo"
		}
		id, err := app.store.createThread(title)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		threadID, isNew = id, true
	}

	history, _ := app.store.listChatMessages(threadID, 40)
	msgID, err := app.store.addChatMessageWithImages(threadID, "user", userMsg, images)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	saved, _ := app.store.getChatMessage(msgID)

	// Insert a pending assistant reply and generate it in the background, so the
	// answer completes and is saved even if the user navigates away or reloads.
	// The UI polls /chat/msg/{id} until it flips to done/error.
	pendingID, err := app.store.addPendingAssistant(threadID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	go app.generateReply(agent, threadID, pendingID, history, userMsg, images, isNew)

	if isNew {
		// Put the new thread in the URL so reload/back work like a normal page.
		w.Header().Set("HX-Push-Url", "/c/"+strconv.FormatInt(threadID, 10))
	}
	// The user bubble was shown optimistically client-side; re-render it
	// authoritatively, then a polling placeholder for the reply. Refresh the
	// sidebar and thread id out-of-band so follow-ups land in the same thread.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	app.writeUserBubble(w, saved)
	app.writePendingBubble(w, pendingID)
	w.Write([]byte(`<input type="hidden" name="thread_id" id="thread-id" value="` + strconv.FormatInt(threadID, 10) + `" hx-swap-oob="true">`))
	app.writeThreadList(w, threadID, true)
}

// readChatImages pulls the "images" file parts off a multipart chat post,
// sniffing each one's real type (the browser's claimed type is ignored).
func readChatImages(r *http.Request) ([]ChatImage, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	files := r.MultipartForm.File["images"]
	if len(files) > maxChatImages {
		return nil, fmt.Errorf("You can attach up to %d photos per message.", maxChatImages)
	}
	var out []ChatImage
	for _, fh := range files {
		if fh.Size > maxChatImageBytes {
			return nil, fmt.Errorf("%s is too large (max 5 MB per photo).", fh.Filename)
		}
		f, err := fh.Open()
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(io.LimitReader(f, maxChatImageBytes+1))
		f.Close()
		if err != nil {
			return nil, err
		}
		if len(data) == 0 {
			continue
		}
		if len(data) > maxChatImageBytes {
			return nil, fmt.Errorf("%s is too large (max 5 MB per photo).", fh.Filename)
		}
		mt := http.DetectContentType(data)
		switch mt {
		case "image/jpeg", "image/png", "image/gif", "image/webp":
		default:
			return nil, fmt.Errorf("%s isn't a supported image (use JPEG, PNG, GIF or WebP).", fh.Filename)
		}
		out = append(out, ChatImage{MediaType: mt, Data: data})
	}
	return out, nil
}

// handleChatImage serves an attached photo for display in the conversation.
func (app *App) handleChatImage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	img, ok := app.store.getChatImage(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", img.MediaType)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(img.Data)
}

// generateReply runs the coach's tool-use loop off the request path and writes
// the result into the pending assistant row. It deliberately uses no request
// context, so a client disconnect (tab switch, reload, close) never cancels it.
func (app *App) generateReply(agent *Agent, threadID, pendingID int64, history []ChatMessage, userMsg string, images []ChatImage, isNew bool) {
	reply, mutated, err := agent.Chat(history, userMsg, images)
	status := "done"
	if err != nil {
		log.Printf("chat: %v", err)
		reply = "Something went wrong talking to Claude: " + err.Error()
		status = "error"
	}
	if e := app.store.finishChatMessage(pendingID, reply, status, mutated); e != nil {
		log.Printf("chat: save reply: %v", e)
	}
	if isNew && status == "done" {
		if userMsg == "" {
			userMsg = "(sent a photo)"
		}
		if title := agent.TitleFor(userMsg, reply); title != "" {
			_ = app.store.renameThread(threadID, title)
		}
	}
}

// handleChatMessage is the poll endpoint for a single assistant reply. While the
// reply is pending it returns the same placeholder (which keeps polling); once
// it's done or errored it returns the final bubble with no poll trigger, so
// polling stops, plus an out-of-band sidebar refresh to pick up a new title.
func (app *App) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	m, ok := app.store.getChatMessage(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if m.Pending() {
		app.writePendingBubble(w, id)
		return
	}
	app.writeChatBubble(w, "assistant", m.Content, m.Mutated)
	// The thread may have just been auto-titled; refresh the sidebar so it shows.
	app.writeThreadList(w, m.ThreadID, true)
}

// --- Settings / API key ---

// handleSettingsFragment re-renders just the settings sub-panel.
func (app *App) handleSettingsFragment(w http.ResponseWriter, r *http.Request) {
	if err := app.tmpl.ExecuteTemplate(w, "settings.html", app.settingsData()); err != nil {
		log.Printf("render settings: %v", err)
	}
}

func (app *App) handleSaveKey(w http.ResponseWriter, r *http.Request) {
	if app.envLocked() {
		app.renderSettings(w, "The key is set via ANTHROPIC_API_KEY and can't be changed here.", "err")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(r.FormValue("api_key"))
	if key == "" {
		app.renderSettings(w, "Paste a key first.", "err")
		return
	}
	if !strings.HasPrefix(key, "sk-ant-") {
		app.renderSettings(w, "That doesn't look like an Anthropic key (should start with \"sk-ant-\").", "err")
		return
	}
	if err := app.store.setSetting("anthropic_api_key", key); err != nil {
		app.renderSettings(w, "Couldn't save the key: "+err.Error(), "err")
		return
	}
	app.setKey(key, false)
	app.renderSettings(w, "Key saved — the coach is now enabled.", "ok")
}

func (app *App) handleClearKey(w http.ResponseWriter, r *http.Request) {
	if app.envLocked() {
		app.renderSettings(w, "The key is set via ANTHROPIC_API_KEY; unset the env var and restart to remove it.", "err")
		return
	}
	_ = app.store.deleteSetting("anthropic_api_key")
	app.clearKey()
	app.renderSettings(w, "Key removed. The coach is disabled.", "ok")
}

// renderSettings re-renders the settings panel with a status message. The JS
// listener on "settings-changed" reloads the page so the coach's enabled state
// (chat input, empty note) flips to match.
func (app *App) renderSettings(w http.ResponseWriter, msg, kind string) {
	data := app.settingsData()
	data.Msg = msg
	data.Kind = kind
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", "settings-changed")
	if err := app.tmpl.ExecuteTemplate(w, "settings.html", data); err != nil {
		log.Printf("render settings: %v", err)
	}
}

// writeChatBubble emits a single chat message bubble. Assistant messages are
// rendered as Markdown; user messages stay plain text. If mutated is true, it
// includes an HTMX trigger that refreshes the workout log panel.
func (app *App) writeChatBubble(w http.ResponseWriter, role, content string, mutated bool) {
	var body string
	if role == "assistant" {
		body = string(renderMarkdown(content))
	} else {
		body = strings.ReplaceAll(html.EscapeString(content), "\n", "<br>")
	}
	trigger := ""
	if mutated {
		// This attribute nudges the log panel to reload via HTMX.
		trigger = ` data-refresh-log="1"`
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<div class="bubble ` + role + ` md"` + trigger + `>` + body + `</div>`))
}

// writeUserBubble renders a stored user message, thumbnails first.
func (app *App) writeUserBubble(w http.ResponseWriter, m ChatMessage) {
	body := imagesHTML(m.Images) + strings.ReplaceAll(html.EscapeString(m.Content), "\n", "<br>")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(`<div class="bubble user md">` + body + `</div>`))
}

// imagesHTML renders the thumbnail strip for a message's attached photos.
func imagesHTML(imgs []ChatImage) string {
	if len(imgs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<div class="bubble-images">`)
	for _, img := range imgs {
		src := "/chat/img/" + strconv.FormatInt(img.ID, 10)
		sb.WriteString(`<a href="` + src + `" target="_blank" rel="noopener"><img src="` + src + `" alt="Attached photo" loading="lazy"></a>`)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

// writePendingBubble emits the "thinking" placeholder for an in-flight reply.
// It polls GET /chat/msg/{id} every 1.5s and replaces itself with the result;
// because it re-renders from the database, it resumes automatically after a
// reload or when the user returns to the tab. The id lets the poller find it.
func (app *App) writePendingBubble(w http.ResponseWriter, id int64) {
	sid := strconv.FormatInt(id, 10)
	w.Write([]byte(`<div class="bubble assistant pending" ` +
		`hx-get="/chat/msg/` + sid + `" hx-trigger="load delay:1500ms" ` +
		`hx-swap="outerHTML" hx-target="this">` +
		`<span class="typing"><i></i><i></i><i></i></span></div>`))
}
