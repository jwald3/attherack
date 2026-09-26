package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jwald3/attherack/internal/chart"
	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// --- Bodyweight tab ---

// bodyweightData drives the bodyweight tab (chart + list).
type bodyweightData struct {
	PageTitle string
	Active    string
	Today     string
	Entries   []store.BodyweightEntry // recent, newest-first (for the list)
	Latest    *store.BodyweightEntry
	ChangeDir string // "down" | "up" | "" — styling hint for the total change
	Chart     chart.Data
}

func (app *App) bodyweightData() bodyweightData {
	listEntries, _ := app.store.ListBodyweight(60)  // list shows recent
	allEntries, _ := app.store.ListBodyweight(3650) // chart uses full history
	pts := make([]chart.Point, len(allEntries))
	for i, e := range allEntries {
		pts[i] = chart.Point{Date: e.Date, Value: e.Weight}
	}
	d := bodyweightData{
		PageTitle: "Bodyweight",
		Active:    "bodyweight",
		Today:     dates.Today(),
		Entries:   listEntries,
		Chart:     chart.Build(pts),
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
	app.render(w, "bodyweight_page.html", app.bodyweightData())
}

// bodyweightContent renders just the chart+stats+list fragment (for HTMX swaps).
func (app *App) bodyweightContent(w http.ResponseWriter) {
	app.render(w, "bodyweight_content.html", app.bodyweightData())
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
	if err := app.store.LogBodyweight(formDate(r), weight); err != nil {
		serverError(w, err)
		return
	}
	app.bodyweightContent(w)
}

func (app *App) handleDeleteBodyweight(w http.ResponseWriter, r *http.Request) {
	if err := app.store.DeleteBodyweight(r.PathValue("date")); err != nil {
		serverError(w, err)
		return
	}
	app.bodyweightContent(w)
}
