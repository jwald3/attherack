package coach

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// Tools for supplements and food.

var logSupplementTool = tool{
	def: toolDef{
		Name:        "log_supplement",
		Description: "Log a supplement dose the user took (e.g. creatine, protein powder, vitamin D, fish oil).",
		InputSchema: object(map[string]any{
			"name":   prop("string", "Supplement name, e.g. 'Creatine'. Reuse the user's existing names from history when they match."),
			"amount": prop("number", "Dose amount, e.g. 5"),
			"unit":   prop("string", "Dose unit, e.g. 'g', 'mg', 'IU', 'capsules', 'scoops'"),
			"date":   dateProp(),
		}, "name"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Name   string  `json:"name"`
			Amount float64 `json:"amount"`
			Unit   string  `json:"unit"`
			Date   string  `json:"date"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if strings.TrimSpace(in.Name) == "" {
			return "", false, fmt.Errorf("name is required")
		}
		l, err := a.store.LogSupplement(in.Date, strings.TrimSpace(in.Name), in.Amount, strings.TrimSpace(in.Unit))
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged supplement: %s %g%s on %s.", l.Name, l.Amount, l.Unit, l.Date), true, nil
	},
}

var getSupplementHistoryTool = tool{
	def: toolDef{
		Name:        "get_supplement_history",
		Description: "Get recent supplement doses (date, name, amount, unit), most recent first. Use for questions about what they take, consistency, or missed days.",
		InputSchema: object(map[string]any{
			"limit": prop("integer", "Max entries (default 200)"),
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
		logs, err := a.store.ListSupplementLogs(in.Limit)
		if err != nil {
			return "", false, err
		}
		if len(logs) == 0 {
			return "No supplements logged yet.", false, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d supplement doses (most recent first):\n", len(logs))
		for _, l := range logs {
			fmt.Fprintf(&sb, "%s: %s", l.Date, l.Name)
			if l.Amount > 0 {
				fmt.Fprintf(&sb, " %g%s", l.Amount, l.Unit)
			}
			sb.WriteString("\n")
		}
		return sb.String(), false, nil
	},
}

var logFoodTool = tool{
	def: toolDef{
		Name:        "log_food",
		Description: "Log something the user ate or drank. Name and notes are enough; macros are optional.",
		InputSchema: object(map[string]any{
			"name":     prop("string", "Food name, e.g. 'Chicken burrito bowl'"),
			"meal":     enumProp([]string{"breakfast", "lunch", "dinner", "snack"}, "Which meal, if known"),
			"notes":    prop("string", "Portion size, ingredients, brand, how it was prepared, etc."),
			"calories": prop("number", ""),
			"protein":  prop("number", "grams"),
			"carbs":    prop("number", "grams"),
			"fat":      prop("number", "grams"),
			"date":     dateProp(),
		}, "name"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in store.FoodLog
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			return "", false, fmt.Errorf("name is required")
		}
		in.ID = 0
		f, err := a.store.LogFood(in)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged food on %s: %s.", f.Date, describeFood(f)), true, nil
	},
}

var getFoodHistoryTool = tool{
	def: toolDef{
		Name:        "get_food_history",
		Description: "Get the user's food log for the last N days (meal, name, notes, and any macros), newest day first. Use for any question about diet, protein intake, calories, or eating habits.",
		InputSchema: object(map[string]any{
			"days": prop("integer", "How many days back, including today (default 7)"),
		}),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Days int `json:"days"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Days <= 0 {
			in.Days = 7
		}
		foods, err := a.store.ListFoodSince(dates.DaysAgo(in.Days - 1))
		if err != nil {
			return "", false, err
		}
		if len(foods) == 0 {
			return fmt.Sprintf("No food logged in the last %d days.", in.Days), false, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d food entries, last %d days (newest day first):\n", len(foods), in.Days)
		lastDate := ""
		for _, f := range foods {
			if f.Date != lastDate {
				fmt.Fprintf(&sb, "%s:\n", f.Date)
				lastDate = f.Date
			}
			fmt.Fprintf(&sb, "  - %s\n", describeFood(f))
		}
		return sb.String(), false, nil
	},
}

// describeFood renders a food entry compactly for tool results, e.g.
// "[lunch] Burrito bowl (double chicken) — 750 kcal, 55g protein".
func describeFood(f store.FoodLog) string {
	var sb strings.Builder
	if f.Meal != "" {
		fmt.Fprintf(&sb, "[%s] ", f.Meal)
	}
	sb.WriteString(f.Name)
	if f.Notes != "" {
		fmt.Fprintf(&sb, " (%s)", f.Notes)
	}
	var macros []string
	if f.Calories != nil {
		macros = append(macros, fmt.Sprintf("%g kcal", *f.Calories))
	}
	for _, m := range []struct {
		v      *float64
		suffix string
	}{{f.Protein, "g protein"}, {f.Carbs, "g carbs"}, {f.Fat, "g fat"}} {
		if m.v != nil {
			macros = append(macros, fmt.Sprintf("%g%s", *m.v, m.suffix))
		}
	}
	if len(macros) > 0 {
		sb.WriteString(" — " + strings.Join(macros, ", "))
	}
	return sb.String()
}
