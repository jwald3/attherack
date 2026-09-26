package coach

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// Tools for bodyweight and body measurements.

var getBodyweightHistoryTool = tool{
	def: toolDef{
		Name:        "get_bodyweight_history",
		Description: "Get the user's bodyweight history (dated entries, most recent first). Use this whenever the question involves weight loss/gain, body composition, or trends over time — the system prompt only shows the single latest weight.",
		InputSchema: object(map[string]any{
			"limit": prop("integer", "Max entries to return (default 200 — usually the full history)"),
		}),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Limit == 0 {
			in.Limit = 200
		}
		entries, err := a.store.ListBodyweight(in.Limit)
		if err != nil {
			return "", false, err
		}
		if len(entries) == 0 {
			return "No bodyweight entries logged yet.", false, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d bodyweight entries (most recent first):\n", len(entries))
		for _, e := range entries {
			fmt.Fprintf(&sb, "%s: %g\n", e.Date, e.Weight)
		}
		return sb.String(), false, nil
	},
}

var logBodyweightTool = tool{
	def: toolDef{
		Name:        "log_bodyweight",
		Description: "Record the user's bodyweight for a date (defaults to today). Overwrites any existing entry for that date.",
		InputSchema: object(map[string]any{
			"weight": prop("number", "Bodyweight in the user's units"),
			"date":   dateProp(),
		}, "weight"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Weight float64 `json:"weight"`
			Date   string  `json:"date"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if in.Date == "" {
			in.Date = dates.Today()
		}
		if err := a.store.LogBodyweight(in.Date, in.Weight); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Recorded bodyweight %g on %s.", in.Weight, in.Date), true, nil
	},
}

var logMeasurementTool = tool{
	def: toolDef{
		Name:        "log_measurement",
		Description: "Record a body measurement (e.g. waist, chest, an arm) for a date (defaults to today). Overwrites any existing value for that site and date. Values are plain numbers in the user's own units.",
		InputSchema: object(map[string]any{
			"site":  enumProp(store.MeasurementSiteSlugs(), "Which body site: waist, chest, hips, neck, arm_l/arm_r, thigh_l/thigh_r, calf_l/calf_r"),
			"value": prop("number", "Measurement in the user's units"),
			"date":  dateProp(),
		}, "site", "value"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Site  string  `json:"site"`
			Value float64 `json:"value"`
			Date  string  `json:"date"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if !store.IsMeasurementSite(in.Site) {
			return unknownSite(in.Site), false, nil
		}
		if in.Date == "" {
			in.Date = dates.Today()
		}
		if err := a.store.LogMeasurement(in.Date, in.Site, in.Value); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged %s %g on %s.", strings.ToLower(store.MeasurementLabel(in.Site)), in.Value, in.Date), true, nil
	},
}

var getMeasurementHistoryTool = tool{
	def: toolDef{
		Name:        "get_measurement_history",
		Description: "Get body-measurement history. Give a site for its dated values over time (to analyze a trend); omit site to get the latest value of every site.",
		InputSchema: object(map[string]any{
			"site":  enumProp(store.MeasurementSiteSlugs(), "Which site to look up; omit for the latest of all sites"),
			"limit": prop("integer", "Max entries for a single site (default 60)"),
		}),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Site  string `json:"site"`
			Limit int    `json:"limit"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Site == "" {
			latest, err := a.store.LatestMeasurements()
			if err != nil {
				return "", false, err
			}
			if len(latest) == 0 {
				return "No body measurements logged yet.", false, nil
			}
			var sb strings.Builder
			sb.WriteString("Latest measurement per site:\n")
			for _, site := range store.MeasurementSites {
				if m, ok := latest[site.Slug]; ok {
					fmt.Fprintf(&sb, "%s: %g (on %s)\n", site.Label, m.Value, m.Date)
				}
			}
			return sb.String(), false, nil
		}
		if !store.IsMeasurementSite(in.Site) {
			return unknownSite(in.Site), false, nil
		}
		hist, err := a.store.MeasurementHistory(in.Site, in.Limit)
		if err != nil {
			return "", false, err
		}
		label := store.MeasurementLabel(in.Site)
		if len(hist) == 0 {
			return fmt.Sprintf("No %s measurements logged yet.", strings.ToLower(label)), false, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%s history (oldest first):\n", label)
		for _, m := range hist {
			fmt.Fprintf(&sb, "%s: %g\n", m.Date, m.Value)
		}
		return sb.String(), false, nil
	},
}

func unknownSite(site string) string {
	return fmt.Sprintf("Unknown measurement site %q. Valid sites: %s.", site, strings.Join(store.MeasurementSiteSlugs(), ", "))
}
