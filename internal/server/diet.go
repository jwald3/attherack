package server

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

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
	Foods     []store.FoodLog
	HasMacros bool
	Calories  float64
	Protein   float64
	Carbs     float64
	Fat       float64
}

func (app *App) dietData() dietData {
	d := dietData{PageTitle: "Diet", Active: "diet", Today: dates.Today(), Meal: mealForNow(), Names: app.store.RecentFoodNames(200)}
	foods, _ := app.store.ListFoodSince(dates.DaysAgo(29))
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
	app.render(w, "diet_page.html", app.dietData())
}

func (app *App) dietContent(w http.ResponseWriter) {
	app.render(w, "diet_content.html", app.dietData())
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
	f := store.FoodLog{
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
	if _, err := app.store.LogFood(f); err != nil {
		serverError(w, err)
		return
	}
	app.dietContent(w)
}

func (app *App) handleDeleteFood(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		badID(w)
		return
	}
	if err := app.store.DeleteFood(id); err != nil {
		serverError(w, err)
		return
	}
	app.dietContent(w)
}
