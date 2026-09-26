package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// --- Supplements tab ---

type supplementsData struct {
	PageTitle  string
	Active     string
	Today      string
	Regulars   []store.SupplementSummary // everything ever logged, for quick-log + consistency
	TodayCount int
	Days       []supplementDay // recent history grouped by date, newest first
}

// supplementDay is one date's doses.
type supplementDay struct {
	Date string
	Logs []store.SupplementLog
}

func (app *App) supplementsData() supplementsData {
	d := supplementsData{PageTitle: "Supplements", Active: "supplements", Today: dates.Today()}
	// Consistency window: the last 30 days including today.
	d.Regulars, _ = app.store.SupplementSummaries(dates.DaysAgo(29))
	for _, r := range d.Regulars {
		if r.TakenToday {
			d.TodayCount++
		}
	}
	logs, _ := app.store.ListSupplementLogs(300)
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
	app.render(w, "supplements_page.html", app.supplementsData())
}

func (app *App) supplementsContent(w http.ResponseWriter) {
	app.render(w, "supplements_content.html", app.supplementsData())
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
	if _, err := app.store.LogSupplement(formDate(r), name, amount, strings.TrimSpace(r.FormValue("unit"))); err != nil {
		serverError(w, err)
		return
	}
	app.supplementsContent(w)
}

func (app *App) handleDeleteSupplement(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		badID(w)
		return
	}
	if err := app.store.DeleteSupplementLog(id); err != nil {
		serverError(w, err)
		return
	}
	app.supplementsContent(w)
}
