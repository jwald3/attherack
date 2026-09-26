package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/jwald3/attherack/internal/chart"
	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// --- Cardio tab ---

type cardioData struct {
	PageTitle    string
	Active       string
	Today        string
	Sessions     []store.CardioSession
	WeekMiles    float64
	WeekMinutes  int
	WeekCount    int
	MonthMiles   float64
	MonthMinutes int
}

func (app *App) cardioData() cardioData {
	sessions, _ := app.store.ListCardio(200)
	wMi, wSec, wN := app.store.CardioTotalsSince(dates.DaysAgo(7))
	mMi, mSec, _ := app.store.CardioTotalsSince(dates.DaysAgo(30))
	return cardioData{
		PageTitle:    "Cardio",
		Active:       "cardio",
		Today:        dates.Today(),
		Sessions:     sessions,
		WeekMiles:    wMi,
		WeekMinutes:  wSec / 60,
		WeekCount:    wN,
		MonthMiles:   mMi,
		MonthMinutes: mSec / 60,
	}
}

func (app *App) handleCardioPage(w http.ResponseWriter, r *http.Request) {
	app.render(w, "cardio_page.html", app.cardioData())
}

// cardioContent renders just the stats+list fragment (for HTMX swaps).
func (app *App) cardioContent(w http.ResponseWriter) {
	app.render(w, "cardio_content.html", app.cardioData())
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
	if _, err := app.store.LogCardio(formDate(r), ctype, int(minutes*60), miles); err != nil {
		serverError(w, err)
		return
	}
	app.cardioContent(w)
}

func (app *App) handleDeleteCardio(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		badID(w)
		return
	}
	if err := app.store.DeleteCardio(id); err != nil {
		serverError(w, err)
		return
	}
	app.cardioContent(w)
}

// --- Cardio-type history drawer ---

// cardioDetailData drives the cardio-type history drawer.
type cardioDetailData struct {
	Name          string
	Sessions      []store.CardioSession // newest-first
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
	Chart         chart.Data
}

func (app *App) cardioDetailData(name string) cardioDetailData {
	d := cardioDetailData{Name: name}
	d.Sessions, _ = app.store.CardioHistoryByType(name)
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
	pts := make([]chart.Point, 0, len(d.Sessions))
	for _, c := range d.Sessions {
		if c.DistanceMiles > 0 && c.DurationSeconds > 0 {
			pts = append(pts, chart.Point{Date: c.Date, Value: float64(c.DurationSeconds) / c.DistanceMiles})
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
				pts = append(pts, chart.Point{Date: c.Date, Value: c.DistanceMiles})
			}
		}
	} else {
		pts = pts[:0]
		d.ChartLabel = "Duration per session (min)"
		for _, c := range d.Sessions {
			if c.DurationSeconds > 0 {
				pts = append(pts, chart.Point{Date: c.Date, Value: float64(c.DurationSeconds) / 60})
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
			d.Chart = chart.BuildFmt(pts, func(v float64) string { return fmtClock(int(v)) })
			d.ChartFirst, d.ChartLast = fmtClock(int(d.Chart.First)), fmtClock(int(d.Chart.Last))
		} else {
			d.Chart = chart.Build(pts)
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
	app.render(w, "cardio_detail.html", app.cardioDetailData(name))
}
