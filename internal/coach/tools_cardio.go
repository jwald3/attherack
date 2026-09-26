package coach

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jwald3/attherack/internal/dates"
)

// Tools for cardio sessions.

var logCardioTool = tool{
	def: toolDef{
		Name:        "log_cardio",
		Description: "Log a cardio session (e.g. walking, elliptical, running). Provide any of duration and distance.",
		InputSchema: object(map[string]any{
			"type":             prop("string", "Cardio type, e.g. 'Elliptical', 'Walking'"),
			"duration_minutes": prop("number", "Duration in minutes"),
			"distance_miles":   prop("number", "Distance in miles"),
			"date":             dateProp(),
		}, "type"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Type            string  `json:"type"`
			DurationMinutes float64 `json:"duration_minutes"`
			DistanceMiles   float64 `json:"distance_miles"`
			Date            string  `json:"date"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if in.Date == "" {
			in.Date = dates.Today()
		}
		c, err := a.store.LogCardio(in.Date, in.Type, int(in.DurationMinutes*60), in.DistanceMiles)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged cardio: %s on %s (%.0f min, %.2f mi).", c.Type, c.Date, in.DurationMinutes, in.DistanceMiles), true, nil
	},
}

var getCardioHistoryTool = tool{
	def: toolDef{
		Name:        "get_cardio_history",
		Description: "Get recent cardio sessions (type, duration, distance), most recent first. Use for cardio volume, endurance trends, or total mileage.",
		InputSchema: object(map[string]any{
			"limit": prop("integer", "Max sessions (default 100)"),
		}),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Limit == 0 {
			in.Limit = 100
		}
		sessions, err := a.store.ListCardio(in.Limit)
		if err != nil {
			return "", false, err
		}
		if len(sessions) == 0 {
			return "No cardio sessions logged yet.", false, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d cardio sessions (most recent first):\n", len(sessions))
		for _, c := range sessions {
			fmt.Fprintf(&sb, "%s: %s", c.Date, c.Type)
			if c.DistanceMiles > 0 {
				fmt.Fprintf(&sb, " %.2fmi", c.DistanceMiles)
			}
			if c.DurationSeconds > 0 {
				fmt.Fprintf(&sb, " %dmin", c.DurationSeconds/60)
			}
			sb.WriteString("\n")
		}
		return sb.String(), false, nil
	},
}
