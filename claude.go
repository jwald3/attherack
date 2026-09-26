package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	claudeURL   = "https://api.anthropic.com/v1/messages"
	claudeModel = "claude-opus-4-8"
	haikuModel  = "claude-haiku-4-5" // cheap model for quick field inference
	apiVersion  = "2023-06-01"
	maxTurns    = 8 // safety cap on the tool-use loop
)

// --- Anthropic wire types (only the fields we use) ---

type apiMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

// contentPart is a union over text / thinking / tool_use / tool_result blocks.
type contentPart struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// thinking (adaptive thinking) — must be echoed back verbatim, including
	// the signature, or the API rejects the next turn.
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"` // redacted_thinking payload

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
}

// MarshalJSON emits only the fields valid for each block type. This matters for
// thinking blocks: the API requires the `thinking` field to be present (even
// when empty under display:"omitted"), while text blocks must not carry it.
func (c contentPart) MarshalJSON() ([]byte, error) {
	m := map[string]any{"type": c.Type}
	switch c.Type {
	case "thinking":
		m["thinking"] = c.Thinking // always present, even if ""
		if c.Signature != "" {
			m["signature"] = c.Signature
		}
	case "redacted_thinking":
		m["data"] = c.Data
	case "tool_use":
		m["id"] = c.ID
		m["name"] = c.Name
		if c.Input != nil {
			m["input"] = c.Input
		} else {
			m["input"] = map[string]any{}
		}
	case "tool_result":
		m["tool_use_id"] = c.ToolUseID
		m["content"] = c.Content
		if c.IsError {
			m["is_error"] = true
		}
	default: // "text" and anything else
		m["text"] = c.Text
	}
	return json.Marshal(m)
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type apiRequest struct {
	Model     string         `json:"model"`
	MaxTokens int            `json:"max_tokens"`
	System    string         `json:"system,omitempty"`
	Messages  []apiMessage   `json:"messages"`
	Tools     []toolDef      `json:"tools,omitempty"`
	Thinking  map[string]any `json:"thinking,omitempty"`
}

type apiResponse struct {
	Content    []contentPart `json:"content"`
	StopReason string        `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// Agent runs the Claude tool-use loop against the user's training data.
type Agent struct {
	apiKey string
	store  *Store
	lib    *ExerciseLibrary
	http   *http.Client
}

func newAgent(apiKey string, store *Store, lib *ExerciseLibrary) *Agent {
	return &Agent{
		apiKey: apiKey,
		store:  store,
		lib:    lib,
		http:   &http.Client{Timeout: 5 * time.Minute},
	}
}

const systemPrompt = `You are a knowledgeable, encouraging strength & conditioning coach embedded in a lightweight weightlifting tracker. You help the user plan training, log workouts, and surface insights from their history.

You have tools to read and write the user's workout data (sets grouped into dated workouts) and to search an exercise database of ~870 movements. Prefer calling tools over guessing:
- When the user reports doing an exercise, log it with log_set.
- To fix a mistake in an already-logged set (wrong reps/weight/rpe), don't log a duplicate: call get_exercise_history to find the set's id, then update_set to correct it or delete_set to remove a stray entry. Confirm which set you're changing if there's any ambiguity.
- When asked how they're trending or about a specific lift, call get_exercise_history or list_workouts first, then answer with real numbers.
- When suggesting exercises, use search_exercises so you recommend real movements with correct muscle/equipment data.
- If the user mentions a movement not in the library, search first; if it's genuinely missing, add it with create_exercise (then you can log sets against it).
- When the user says they took a supplement (creatine, protein, vitamins…), log it with log_supplement. For questions about supplement consistency, call get_supplement_history.
- When the user describes something they ate or drank, log it with log_food: a plain food name plus any useful detail in notes (portion, how it was prepared, brand). Only fill macros if the user gives them or explicitly asks you to estimate. For diet questions (protein intake, eating patterns, what they ate), call get_food_history first; if macros are missing, reason from the food names and notes and say that your numbers are estimates.
- For anything about weight loss/gain, body composition, or bodyweight trends, call get_bodyweight_history first. The snapshot below shows only the single most recent weigh-in — it is NOT the full history, so never conclude "only one entry" without calling the tool.

Be concise and practical. Use the user's own units (they give weight as a number; don't assume kg vs lb). Today's date is provided below — use it as the default date for logging unless the user specifies otherwise.

If the user asks for insights, ground every claim in data you retrieved. Call out progressions ("+10 from last week"), stalls, and imbalances when you see them.`

func (a *Agent) tools() []toolDef {
	return []toolDef{
		{
			Name:        "log_set",
			Description: "Log a single working set. Groups into the workout for the given date (defaults to today). weight is a plain number in the user's own units.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"exercise": map[string]any{"type": "string", "description": "Exercise name, e.g. 'Barbell Squat'"},
					"weight":   map[string]any{"type": "number", "description": "Weight lifted (user's units)"},
					"reps":     map[string]any{"type": "integer", "description": "Repetitions performed"},
					"rpe":      map[string]any{"type": "number", "description": "Optional rate of perceived exertion, 1-10"},
					"date":     map[string]any{"type": "string", "description": "YYYY-MM-DD; omit for today"},
				},
				"required": []string{"exercise", "weight", "reps"},
			},
		},
		{
			Name:        "list_workouts",
			Description: "List recent workouts (most recent first) with all their logged sets. Use to review history or overall trends.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "description": "How many recent workouts to return (default 15)"},
				},
			},
		},
		{
			Name:        "get_exercise_history",
			Description: "Get the recent set history for a single exercise, most recent first. Use to analyze progression on a specific lift.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"exercise": map[string]any{"type": "string", "description": "Exercise name to look up"},
					"limit":    map[string]any{"type": "integer", "description": "Max sets to return (default 30)"},
				},
				"required": []string{"exercise"},
			},
		},
		{
			Name:        "search_exercises",
			Description: "Search the exercise database. Filter by free-text query, target muscle, and/or equipment. Returns name, target muscles, equipment, and category.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query":     map[string]any{"type": "string", "description": "Free text, e.g. 'row' or 'squat'"},
					"muscle":    map[string]any{"type": "string", "description": "Target muscle, e.g. 'quadriceps', 'chest', 'lats'"},
					"equipment": map[string]any{"type": "string", "description": "e.g. 'barbell', 'dumbbell', 'body only', 'cable'"},
					"limit":     map[string]any{"type": "integer", "description": "Max results (default 15)"},
				},
			},
		},
		{
			Name:        "log_cardio",
			Description: "Log a cardio session (e.g. walking, elliptical, running). Provide any of duration and distance.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"type":             map[string]any{"type": "string", "description": "Cardio type, e.g. 'Elliptical', 'Walking'"},
					"duration_minutes": map[string]any{"type": "number", "description": "Duration in minutes"},
					"distance_miles":   map[string]any{"type": "number", "description": "Distance in miles"},
					"date":             map[string]any{"type": "string", "description": "YYYY-MM-DD; omit for today"},
				},
				"required": []string{"type"},
			},
		},
		{
			Name:        "get_cardio_history",
			Description: "Get recent cardio sessions (type, duration, distance), most recent first. Use for cardio volume, endurance trends, or total mileage.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "description": "Max sessions (default 100)"},
				},
			},
		},
		{
			Name:        "log_supplement",
			Description: "Log a supplement dose the user took (e.g. creatine, protein powder, vitamin D, fish oil).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":   map[string]any{"type": "string", "description": "Supplement name, e.g. 'Creatine'. Reuse the user's existing names from history when they match."},
					"amount": map[string]any{"type": "number", "description": "Dose amount, e.g. 5"},
					"unit":   map[string]any{"type": "string", "description": "Dose unit, e.g. 'g', 'mg', 'IU', 'capsules', 'scoops'"},
					"date":   map[string]any{"type": "string", "description": "YYYY-MM-DD; omit for today"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "get_supplement_history",
			Description: "Get recent supplement doses (date, name, amount, unit), most recent first. Use for questions about what they take, consistency, or missed days.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "description": "Max entries (default 200)"},
				},
			},
		},
		{
			Name:        "log_food",
			Description: "Log something the user ate or drank. Name and notes are enough; macros are optional.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":     map[string]any{"type": "string", "description": "Food name, e.g. 'Chicken burrito bowl'"},
					"meal":     map[string]any{"type": "string", "enum": []string{"breakfast", "lunch", "dinner", "snack"}, "description": "Which meal, if known"},
					"notes":    map[string]any{"type": "string", "description": "Portion size, ingredients, brand, how it was prepared, etc."},
					"calories": map[string]any{"type": "number"},
					"protein":  map[string]any{"type": "number", "description": "grams"},
					"carbs":    map[string]any{"type": "number", "description": "grams"},
					"fat":      map[string]any{"type": "number", "description": "grams"},
					"date":     map[string]any{"type": "string", "description": "YYYY-MM-DD; omit for today"},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "get_food_history",
			Description: "Get the user's food log for the last N days (meal, name, notes, and any macros), newest day first. Use for any question about diet, protein intake, calories, or eating habits.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"days": map[string]any{"type": "integer", "description": "How many days back, including today (default 7)"},
				},
			},
		},
		{
			Name:        "get_bodyweight_history",
			Description: "Get the user's bodyweight history (dated entries, most recent first). Use this whenever the question involves weight loss/gain, body composition, or trends over time — the system prompt only shows the single latest weight.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "description": "Max entries to return (default 200 — usually the full history)"},
				},
			},
		},
		{
			Name:        "log_bodyweight",
			Description: "Record the user's bodyweight for a date (defaults to today). Overwrites any existing entry for that date.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"weight": map[string]any{"type": "number", "description": "Bodyweight in the user's units"},
					"date":   map[string]any{"type": "string", "description": "YYYY-MM-DD; omit for today"},
				},
				"required": []string{"weight"},
			},
		},
		{
			Name:        "create_exercise",
			Description: "Add a new custom exercise to the user's library when they mention a movement that isn't already there (e.g. 'parallel grip lat pulldown'). Check search_exercises first to avoid duplicates. After creating it you can log sets against it.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":              map[string]any{"type": "string", "description": "Exercise name"},
					"primary_muscles":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "e.g. ['lats']"},
					"secondary_muscles": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"equipment":         map[string]any{"type": "string", "description": "e.g. 'cable', 'machine', 'barbell'"},
					"category":          map[string]any{"type": "string", "enum": []string{"strength", "stretching", "plyometrics", "cardio", "strongman", "powerlifting"}},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "set_workout_notes",
			Description: "Attach or update freeform notes on a workout for a given date (defaults to today). Use for how a session felt, injuries, focus, etc.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"notes": map[string]any{"type": "string"},
					"date":  map[string]any{"type": "string", "description": "YYYY-MM-DD; omit for today"},
				},
				"required": []string{"notes"},
			},
		},
		{
			Name:        "update_set",
			Description: "Fix an already-logged set by its id: overwrite its weight, reps and/or rpe. Use this to correct a mistake (e.g. a typo in reps) instead of logging a duplicate. Get the set id from get_exercise_history or list_workouts first.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "integer", "description": "The set id to update (from get_exercise_history or list_workouts)"},
					"weight": map[string]any{"type": "number", "description": "Corrected weight (user's units). Omit to keep the current value."},
					"reps":   map[string]any{"type": "integer", "description": "Corrected reps. Omit to keep the current value."},
					"rpe":    map[string]any{"type": "number", "description": "Corrected RPE 1-10. Omit to keep the current value."},
				},
				"required": []string{"id"},
			},
		},
		{
			Name:        "delete_set",
			Description: "Delete a mistakenly-logged set by its id. Use for a duplicate or an entry that should not exist. Get the set id from get_exercise_history or list_workouts first, and confirm with the user which set before deleting if there's any ambiguity.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "integer", "description": "The set id to delete (from get_exercise_history or list_workouts)"},
				},
				"required": []string{"id"},
			},
		},
	}
}

// Chat runs a full turn: sends the conversation to Claude, executes any tool
// calls, and loops until Claude produces a final text answer. It returns the
// assistant's final text and whether the store was mutated (so the UI can
// know to refresh the log panel).
func (a *Agent) Chat(history []ChatMessage, userMsg string) (reply string, mutated bool, err error) {
	msgs := make([]apiMessage, 0, len(history)+1)
	for _, m := range history {
		msgs = append(msgs, apiMessage{
			Role:    m.Role,
			Content: []contentPart{{Type: "text", Text: m.Content}},
		})
	}
	msgs = append(msgs, apiMessage{
		Role:    "user",
		Content: []contentPart{{Type: "text", Text: userMsg}},
	})

	sys := systemPrompt + "\n\nToday's date: " + today() +
		"\n\nRecent training snapshot:\n" + a.store.summaryContext()

	for turn := 0; turn < maxTurns; turn++ {
		resp, err := a.call(apiRequest{
			Model:     claudeModel,
			MaxTokens: 4096,
			System:    sys,
			Messages:  msgs,
			Tools:     a.tools(),
			Thinking:  map[string]any{"type": "adaptive"},
		})
		if err != nil {
			return "", mutated, err
		}

		// Append the assistant turn verbatim so tool_use blocks are preserved.
		msgs = append(msgs, apiMessage{Role: "assistant", Content: resp.Content})

		if resp.StopReason != "tool_use" {
			return collectText(resp.Content), mutated, nil
		}

		// Execute every tool_use block and gather results into one user turn.
		var results []contentPart
		for _, part := range resp.Content {
			if part.Type != "tool_use" {
				continue
			}
			out, didMutate, terr := a.runTool(part.Name, part.Input)
			if didMutate {
				mutated = true
			}
			res := contentPart{Type: "tool_result", ToolUseID: part.ID, Content: out}
			if terr != nil {
				res.IsError = true
				res.Content = "error: " + terr.Error()
			}
			results = append(results, res)
		}
		msgs = append(msgs, apiMessage{Role: "user", Content: results})
	}
	return "I got stuck working through that — try rephrasing?", mutated, nil
}

func (a *Agent) call(req apiRequest) (*apiResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest("POST", claudeURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// Surface the API's own error message when we can parse it.
		var apiErr apiResponse
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Error != nil {
			return nil, fmt.Errorf("claude api (%d): %s", resp.StatusCode, apiErr.Error.Message)
		}
		return nil, fmt.Errorf("claude api returned %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decoding claude response: %w", err)
	}
	return &out, nil
}

// runTool dispatches a tool call to the store/library and returns a text result.
func (a *Agent) runTool(name string, input json.RawMessage) (result string, mutated bool, err error) {
	switch name {
	case "log_set":
		var in struct {
			Exercise string   `json:"exercise"`
			Weight   float64  `json:"weight"`
			Reps     int      `json:"reps"`
			RPE      *float64 `json:"rpe"`
			Date     string   `json:"date"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if in.Date == "" {
			in.Date = today()
		}
		set, err := a.store.logSet(in.Date, in.Exercise, in.Weight, in.Reps, in.RPE)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged set #%d: %s %gx%d on %s", set.ID, in.Exercise, in.Weight, in.Reps, in.Date), true, nil

	case "list_workouts":
		var in struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Limit == 0 {
			in.Limit = 15
		}
		workouts, err := a.store.listWorkouts(in.Limit)
		if err != nil {
			return "", false, err
		}
		b, _ := json.Marshal(workouts)
		return string(b), false, nil

	case "get_exercise_history":
		var in struct {
			Exercise string `json:"exercise"`
			Limit    int    `json:"limit"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		hist, err := a.store.exerciseHistory(in.Exercise, in.Limit)
		if err != nil {
			return "", false, err
		}
		if len(hist) == 0 {
			return fmt.Sprintf("No logged sets for %q yet.", in.Exercise), false, nil
		}
		var sb strings.Builder
		for _, h := range hist {
			rpe := ""
			if h.Set.RPE != nil {
				rpe = fmt.Sprintf(" @RPE%.1f", *h.Set.RPE)
			}
			// Include the set id so it can be referenced by delete_set / update_set.
			fmt.Fprintf(&sb, "set #%d — %s: %s %gx%d%s\n", h.Set.ID, h.Date, h.Set.Exercise, h.Set.Weight, h.Set.Reps, rpe)
		}
		return sb.String(), false, nil

	case "search_exercises":
		var in struct {
			Query     string `json:"query"`
			Muscle    string `json:"muscle"`
			Equipment string `json:"equipment"`
			Limit     int    `json:"limit"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Limit == 0 {
			in.Limit = 15
		}
		hits := a.lib.Search(in.Query, in.Muscle, in.Equipment, in.Limit)
		if len(hits) == 0 {
			return "No matching exercises.", false, nil
		}
		// Return a compact projection, not full instructions/images.
		type slim struct {
			Name      string   `json:"name"`
			Primary   []string `json:"primary_muscles"`
			Equipment string   `json:"equipment"`
			Category  string   `json:"category"`
		}
		out := make([]slim, len(hits))
		for i, e := range hits {
			out[i] = slim{e.Name, e.PrimaryMuscles, strDeref(e.Equipment), e.Category}
		}
		b, _ := json.Marshal(out)
		return string(b), false, nil

	case "log_cardio":
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
			in.Date = today()
		}
		c, err := a.store.logCardio(in.Date, in.Type, int(in.DurationMinutes*60), in.DistanceMiles)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged cardio: %s on %s (%.0f min, %.2f mi).", c.Type, c.Date, in.DurationMinutes, in.DistanceMiles), true, nil

	case "get_cardio_history":
		var in struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Limit == 0 {
			in.Limit = 100
		}
		sessions, err := a.store.listCardio(in.Limit)
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

	case "log_supplement":
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
		l, err := a.store.logSupplement(in.Date, strings.TrimSpace(in.Name), in.Amount, strings.TrimSpace(in.Unit))
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged supplement: %s %g%s on %s.", l.Name, l.Amount, l.Unit, l.Date), true, nil

	case "get_supplement_history":
		var in struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Limit == 0 {
			in.Limit = 200
		}
		logs, err := a.store.listSupplementLogs(in.Limit)
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

	case "log_food":
		var in FoodLog
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			return "", false, fmt.Errorf("name is required")
		}
		in.ID = 0
		f, err := a.store.logFood(in)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged food on %s: %s.", f.Date, describeFood(f)), true, nil

	case "get_food_history":
		var in struct {
			Days int `json:"days"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Days <= 0 {
			in.Days = 7
		}
		foods, err := a.store.listFoodSince(daysAgo(in.Days - 1))
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

	case "get_bodyweight_history":
		var in struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Limit == 0 {
			in.Limit = 200
		}
		entries, err := a.store.listBodyweight(in.Limit)
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

	case "log_bodyweight":
		var in struct {
			Weight float64 `json:"weight"`
			Date   string  `json:"date"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if in.Date == "" {
			in.Date = today()
		}
		if err := a.store.logBodyweight(in.Date, in.Weight); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Recorded bodyweight %g on %s.", in.Weight, in.Date), true, nil

	case "create_exercise":
		var in struct {
			Name             string   `json:"name"`
			PrimaryMuscles   []string `json:"primary_muscles"`
			SecondaryMuscles []string `json:"secondary_muscles"`
			Equipment        string   `json:"equipment"`
			Category         string   `json:"category"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		ex, err := a.store.addCustomExercise(in.Name, in.Equipment, "", in.Category, in.PrimaryMuscles, in.SecondaryMuscles)
		if err != nil {
			if err == errExerciseExists {
				return fmt.Sprintf("%q already exists in the library — no need to add it.", in.Name), false, nil
			}
			return "", false, err
		}
		a.lib.Add(ex)
		return fmt.Sprintf("Added custom exercise %q. It's now searchable and you can log sets against it.", ex.Name), true, nil

	case "set_workout_notes":
		var in struct {
			Notes string `json:"notes"`
			Date  string `json:"date"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if in.Date == "" {
			in.Date = today()
		}
		if _, err := a.store.setWorkoutNotes(in.Date, in.Notes); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Saved notes for %s.", in.Date), true, nil

	case "update_set":
		var in struct {
			ID     int64    `json:"id"`
			Weight *float64 `json:"weight"`
			Reps   *int     `json:"reps"`
			RPE    *float64 `json:"rpe"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if in.ID == 0 {
			return "", false, fmt.Errorf("id is required")
		}
		// Start from the current values so omitted fields are preserved.
		cur, err := a.store.getSet(in.ID)
		if err == sql.ErrNoRows {
			return fmt.Sprintf("No set with id #%d.", in.ID), false, nil
		}
		if err != nil {
			return "", false, err
		}
		weight, reps, rpe := cur.Set.Weight, cur.Set.Reps, cur.Set.RPE
		if in.Weight != nil {
			weight = *in.Weight
		}
		if in.Reps != nil {
			reps = *in.Reps
		}
		if in.RPE != nil {
			rpe = in.RPE
		}
		updated, err := a.store.updateSet(in.ID, weight, reps, rpe)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Updated set #%d: %s %gx%d on %s.", updated.Set.ID, updated.Set.Exercise, updated.Set.Weight, updated.Set.Reps, updated.Date), true, nil

	case "delete_set":
		var in struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if in.ID == 0 {
			return "", false, fmt.Errorf("id is required")
		}
		cur, err := a.store.getSet(in.ID)
		if err == sql.ErrNoRows {
			return fmt.Sprintf("No set with id #%d.", in.ID), false, nil
		}
		if err != nil {
			return "", false, err
		}
		if err := a.store.deleteSet(in.ID); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Deleted set #%d: %s %gx%d on %s.", cur.Set.ID, cur.Set.Exercise, cur.Set.Weight, cur.Set.Reps, cur.Date), true, nil

	default:
		return "", false, fmt.Errorf("unknown tool %q", name)
	}
}

func collectText(parts []contentPart) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			sb.WriteString(p.Text)
		}
	}
	s := strings.TrimSpace(sb.String())
	if s == "" {
		return "(no response)"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// --- AI field suggestion (cheap model) ---

// ExerciseSuggestion is the structured guess for a custom exercise's metadata.
type ExerciseSuggestion struct {
	PrimaryMuscles   []string `json:"primary_muscles"`
	SecondaryMuscles []string `json:"secondary_muscles"`
	Equipment        string   `json:"equipment"`
	Category         string   `json:"category"`
}

// suggestRequest is a minimal Messages request with structured output.
type suggestRequest struct {
	Model        string         `json:"model"`
	MaxTokens    int            `json:"max_tokens"`
	System       string         `json:"system,omitempty"`
	Messages     []apiMessage   `json:"messages"`
	OutputConfig map[string]any `json:"output_config,omitempty"`
}

// SuggestExercise uses a cheap model to infer a custom exercise's muscles,
// equipment, and category from just its name. Enum values are drawn from
// the free-exercise-db vocabulary so results merge cleanly with the dataset.
func (a *Agent) SuggestExercise(name string) (ExerciseSuggestion, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return ExerciseSuggestion{}, fmt.Errorf("name is required")
	}

	const muscleList = "abdominals, abductors, adductors, biceps, calves, chest, forearms, glutes, hamstrings, lats, lower back, middle back, neck, quadriceps, shoulders, traps, triceps"
	sys := "You are a strength-training taxonomy assistant. Given an exercise name, infer its attributes. " +
		"Use ONLY these muscle values (lowercase, exact): " + muscleList + ". " +
		"Pick 1-2 primary muscles and 0-3 secondary muscles. Be accurate and conservative."

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"primary_muscles":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"secondary_muscles": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"equipment":         map[string]any{"type": "string", "enum": []string{"barbell", "dumbbell", "cable", "machine", "kettlebells", "bands", "body only", "medicine ball", "exercise ball", "foam roll", "e-z curl bar", "other"}},
			"category":          map[string]any{"type": "string", "enum": []string{"strength", "stretching", "plyometrics", "cardio", "strongman", "powerlifting"}},
		},
		"required":             []string{"primary_muscles", "secondary_muscles", "equipment", "category"},
		"additionalProperties": false,
	}

	req := suggestRequest{
		Model:     haikuModel,
		MaxTokens: 512,
		System:    sys,
		Messages: []apiMessage{{
			Role:    "user",
			Content: []contentPart{{Type: "text", Text: "Exercise name: " + name}},
		}},
		OutputConfig: map[string]any{
			"format": map[string]any{"type": "json_schema", "schema": schema},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return ExerciseSuggestion{}, err
	}
	httpReq, err := http.NewRequest("POST", claudeURL, bytes.NewReader(body))
	if err != nil {
		return ExerciseSuggestion{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := a.http.Do(httpReq)
	if err != nil {
		return ExerciseSuggestion{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return ExerciseSuggestion{}, err
	}
	if resp.StatusCode != http.StatusOK {
		var apiErr apiResponse
		if json.Unmarshal(raw, &apiErr) == nil && apiErr.Error != nil {
			return ExerciseSuggestion{}, fmt.Errorf("%s", apiErr.Error.Message)
		}
		return ExerciseSuggestion{}, fmt.Errorf("suggest failed (%d)", resp.StatusCode)
	}

	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return ExerciseSuggestion{}, err
	}
	// With output_config.format the first text block holds valid JSON.
	jsonText := collectText(out.Content)
	var sug ExerciseSuggestion
	if err := json.Unmarshal([]byte(jsonText), &sug); err != nil {
		return ExerciseSuggestion{}, fmt.Errorf("parsing suggestion: %w", err)
	}
	return sug, nil
}

// TitleFor asks the cheap model for a short conversation title based on the
// opening exchange, like "Bench plateau fixes". Falls back to "" on error.
func (a *Agent) TitleFor(userMsg, reply string) string {
	resp, err := a.call(apiRequest{
		Model:     haikuModel,
		MaxTokens: 30,
		System:    "Write a 2-5 word title for this fitness coaching conversation. Reply with the title only: no quotes, no trailing punctuation.",
		Messages: []apiMessage{{
			Role:    "user",
			Content: []contentPart{{Type: "text", Text: "User: " + truncate(userMsg, 600) + "\n\nCoach: " + truncate(reply, 600)}},
		}},
	})
	if err != nil {
		return ""
	}
	title := strings.Trim(strings.TrimSpace(collectText(resp.Content)), `"'.`)
	if len(title) > 60 {
		return ""
	}
	return title
}

// describeFood renders a food entry compactly for tool results, e.g.
// "[lunch] Burrito bowl (double chicken) — 750 kcal, 55g protein".
func describeFood(f FoodLog) string {
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
