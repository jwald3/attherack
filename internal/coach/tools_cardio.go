package coach

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// Tools for cardio sessions.

var logCardioTool = tool{
	def: toolDef{
		Name:        "log_cardio",
		Description: "Log a cardio session (e.g. walking, elliptical, running). Provide any of duration and distance. Durations keep seconds — pass a run time like '27:08' in duration, not a rounded minute count.",
		InputSchema: object(map[string]any{
			"type":             prop("string", "Cardio type, e.g. 'Elliptical', 'Walking'"),
			"duration":         prop("string", "Duration as mm:ss or h:mm:ss (e.g. '27:08'), preserving seconds. Preferred over duration_minutes."),
			"duration_minutes": prop("number", "Duration in minutes (use only when you have no seconds; 'duration' is preferred)."),
			"distance_miles":   prop("number", "Distance in miles"),
			"date":             dateProp(),
		}, "type"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Type            string  `json:"type"`
			Duration        string  `json:"duration"`
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
		// Prefer the seconds-precise duration string; fall back to minutes.
		seconds := store.ParseDuration(in.Duration)
		if seconds == 0 && in.DurationMinutes > 0 {
			seconds = int(in.DurationMinutes*60 + 0.5)
		}
		c, err := a.store.LogCardio(in.Date, in.Type, seconds, in.DistanceMiles)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged cardio: %s on %s (%s, %.2f mi).", c.Type, c.Date, fmtClockSec(c.DurationSeconds), in.DistanceMiles), true, nil
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
				// Full mm:ss, so the coach sees real times (e.g. 27:08, not 27min).
				fmt.Fprintf(&sb, " %s", fmtClockSec(c.DurationSeconds))
				if c.DistanceMiles > 0 {
					fmt.Fprintf(&sb, " (%s/mi)", fmtClockSec(int(float64(c.DurationSeconds)/c.DistanceMiles+0.5)))
				}
			}
			sb.WriteString("\n")
		}
		return sb.String(), false, nil
	},
}
