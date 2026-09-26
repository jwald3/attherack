package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/jwald3/attherack/internal/chart"
	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// --- Progress tab (measurements + photos) ---

// MeasurementView is one site's data for the Progress page: its label/slug, the
// latest value (if any), a trend chart, and its dated history (newest first).
type MeasurementView struct {
	Slug     string
	Label    string
	HasValue bool
	Latest   float64
	LatestOn string
	Chart    chart.Data
	History  []chart.Point // newest first, for the small per-site list
}

// progressData drives the Progress tab.
type progressData struct {
	PageTitle    string
	Active       string
	Today        string
	Measurements []MeasurementView
	AnyValues    bool
	Photos       []store.ProgressPhoto
}

func (app *App) progressData() progressData {
	latest, _ := app.store.LatestMeasurements()
	views := make([]MeasurementView, 0, len(store.MeasurementSites))
	anyValues := false
	for _, site := range store.MeasurementSites {
		v := MeasurementView{Slug: site.Slug, Label: site.Label}
		hist, _ := app.store.MeasurementHistory(site.Slug, 60) // oldest first
		if m, ok := latest[site.Slug]; ok {
			v.HasValue = true
			v.Latest = m.Value
			v.LatestOn = m.Date
			anyValues = true
		}
		pts := make([]chart.Point, len(hist))
		v.History = make([]chart.Point, len(hist))
		for i, m := range hist {
			pts[i] = chart.Point{Date: m.Date, Value: m.Value}
			v.History[len(hist)-1-i] = pts[i] // history list wants newest first
		}
		v.Chart = chart.Build(pts)
		views = append(views, v)
	}
	photos, _ := app.store.ListProgressPhotos()
	return progressData{
		PageTitle:    "Progress",
		Active:       "progress",
		Today:        dates.Today(),
		Measurements: views,
		AnyValues:    anyValues,
		Photos:       photos,
	}
}

func (app *App) handleProgressPage(w http.ResponseWriter, r *http.Request) {
	app.render(w, "progress_page.html", app.progressData())
}

func (app *App) measurementsContent(w http.ResponseWriter) {
	app.render(w, "measurements_content.html", app.progressData())
}

func (app *App) photosContent(w http.ResponseWriter) {
	app.render(w, "photos_content.html", app.progressData())
}

// handleAddMeasurement logs whichever site fields were filled in for a date.
func (app *App) handleAddMeasurement(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	date := formDate(r)
	for _, site := range store.MeasurementSites {
		raw := strings.TrimSpace(r.FormValue(site.Slug))
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil || v <= 0 {
			continue
		}
		if err := app.store.LogMeasurement(date, site.Slug, v); err != nil {
			serverError(w, err)
			return
		}
	}
	app.measurementsContent(w)
}

func (app *App) handleDeleteMeasurement(w http.ResponseWriter, r *http.Request) {
	if err := app.store.DeleteMeasurement(r.PathValue("date"), r.PathValue("site")); err != nil {
		serverError(w, err)
		return
	}
	app.measurementsContent(w)
}

// handleAddPhoto stores an uploaded progress photo (multipart) with a date and
// optional pose, then re-renders the gallery.
func (app *App) handleAddPhoto(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(maxChatFormBytes); err != nil {
		http.Error(w, "Upload too large or malformed.", http.StatusBadRequest)
		return
	}
	files := r.MultipartForm.File["photo"]
	if len(files) == 0 {
		app.photosContent(w)
		return
	}
	date := formDate(r)
	pose := strings.TrimSpace(r.FormValue("pose"))
	for _, fh := range files {
		mt, data, err := readUploadedImage(fh)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if data == nil {
			continue
		}
		if _, err := app.store.AddProgressPhoto(date, pose, mt, data); err != nil {
			serverError(w, err)
			return
		}
	}
	w.Header().Set("HX-Trigger", "photo-added")
	app.photosContent(w)
}

func (app *App) handleDeletePhoto(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		badID(w)
		return
	}
	if err := app.store.DeleteProgressPhoto(id); err != nil {
		serverError(w, err)
		return
	}
	app.photosContent(w)
}

// handleProgressPhoto serves a stored progress photo's bytes.
func (app *App) handleProgressPhoto(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	p, ok := app.store.GetProgressPhoto(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeImage(w, p.MediaType, p.Data)
}
