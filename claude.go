package main

import (
	"bytes"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	claudeURL   = "https://api.anthropic.com/v1/messages"
	claudeBase  = "https://api.anthropic.com"
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

	// image (user-attached photo, sent inline as base64)
	Source *imageSource `json:"source,omitempty"`
}

// imageSource is the base64 payload of an image content block.
type imageSource struct {
	Type      string `json:"type"` // always "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// imagePart builds an image content block from stored bytes.
func imagePart(img ChatImage) contentPart {
	return contentPart{Type: "image", Source: &imageSource{
		Type:      "base64",
		MediaType: img.MediaType,
		Data:      base64.StdEncoding.EncodeToString(img.Data),
	}}
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
	case "image":
		m["source"] = c.Source
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
	url    string // messages endpoint; "" means the real API
}

func newAgent(apiKey string, store *Store, lib *ExerciseLibrary) *Agent {
	return &Agent{
		apiKey: apiKey,
		store:  store,
		lib:    lib,
		http:   &http.Client{Timeout: 5 * time.Minute},
		url:    messagesURL(),
	}
}

// messagesURL returns the Messages endpoint, honouring ANTHROPIC_BASE_URL so
// the end-to-end tests (and proxies) can point the app at a stand-in server.
func messagesURL() string {
	base := strings.TrimRight(os.Getenv("ANTHROPIC_BASE_URL"), "/")
	if base == "" {
		base = claudeBase
	}
	return base + "/v1/messages"
}

// endpoint is the URL call sites use; Agents built without newAgent (tests)
// fall back to the real API.
func (a *Agent) endpoint() string {
	if a.url == "" {
		return claudeURL
	}
	return a.url
}

const systemPrompt = `You are a knowledgeable, encouraging strength & conditioning coach embedded in a lightweight weightlifting tracker. You help the user plan training, log workouts, and surface insights from their history.

You have tools to read and write the user's workout data (sets grouped into dated workouts) and to search an exercise database of ~870 movements. Prefer calling tools over guessing:
- When the user reports doing an exercise, log it with log_set.
- To fix a mistake in an already-logged set (wrong reps/weight/rpe), don't log a duplicate: call get_exercise_history to find the set's id, then update_set to correct it or delete_set to remove a stray entry. Confirm which set you're changing if there's any ambiguity.
- The user can save reusable workout templates called programs. Create one with create_program, see what exists with list_programs, and log a whole program's sets to a day with start_program (by name or id). Use these when the user describes a routine they want to reuse (e.g. "make a push day of bench, ohp, dips" then later "start my push day").
- When asked how they're trending or about a specific lift, call get_exercise_history or list_workouts first, then answer with real numbers.
- When suggesting exercises, use search_exercises so you recommend real movements with correct muscle/equipment data.
- If the user mentions a movement not in the library, search first; if it's genuinely missing, add it with create_exercise (then you can log sets against it).
- When the user says they took a supplement (creatine, protein, vitamins…), log it with log_supplement. For questions about supplement consistency, call get_supplement_history.
- When the user describes something they ate or drank, log it with log_food: a plain food name plus any useful detail in notes (portion, how it was prepared, brand). Only fill macros if the user gives them or explicitly asks you to estimate. For diet questions (protein intake, eating patterns, what they ate), call get_food_history first; if macros are missing, reason from the food names and notes and say that your numbers are estimates.
- For anything about weight loss/gain, body composition, or bodyweight trends, call get_bodyweight_history first. The snapshot below shows only the single most recent weigh-in — it is NOT the full history, so never conclude "only one entry" without calling the tool.
- When the user gives a body measurement (waist, chest, an arm, etc.), log it with log_measurement. For questions about a measurement trend, call get_measurement_history first. The snapshot shows only the latest value per site, not the full history.

Be concise and practical. Use the user's own units (they give weight as a number; don't assume kg vs lb). Today's date is provided below — use it as the default date for logging unless the user specifies otherwise.

If the user asks for insights, ground every claim in data you retrieved. Call out progressions ("+10 from last week"), stalls, and imbalances when you see them.

The user can attach photos to a message: a physique check-in, a gym machine or piece of equipment they don't recognize, a screenshot of a workout plan, a meal, or a form check still. When a photo is attached, look at it carefully and answer about what you actually see. For an unfamiliar machine, name it, say what it trains and how to set it up; if it's a known movement, search_exercises so you can log it under a real name. For physique photos be honest, specific and kind: describe what stands out and tie it to training and nutrition suggestions rather than giving a medical or body-fat verdict. For a plan or log screenshot, offer to log the sets it shows. Do not guess at details the photo doesn't show.`

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
			Name:        "log_measurement",
			Description: "Record a body measurement (e.g. waist, chest, an arm) for a date (defaults to today). Overwrites any existing value for that site and date. Values are plain numbers in the user's own units.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"site":  map[string]any{"type": "string", "enum": measurementSiteSlugs(), "description": "Which body site: waist, chest, hips, neck, arm_l/arm_r, thigh_l/thigh_r, calf_l/calf_r"},
					"value": map[string]any{"type": "number", "description": "Measurement in the user's units"},
					"date":  map[string]any{"type": "string", "description": "YYYY-MM-DD; omit for today"},
				},
				"required": []string{"site", "value"},
			},
		},
		{
			Name:        "get_measurement_history",
			Description: "Get body-measurement history. Give a site for its dated values over time (to analyze a trend); omit site to get the latest value of every site.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"site":  map[string]any{"type": "string", "enum": measurementSiteSlugs(), "description": "Which site to look up; omit for the latest of all sites"},
					"limit": map[string]any{"type": "integer", "description": "Max entries for a single site (default 60)"},
				},
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
		{
			Name:        "create_program",
			Description: "Save a reusable workout template ('program'): a named, ordered list of exercises with target sets/reps/weight. Use when the user describes a routine they want to reuse (e.g. a push day). The user can later start it to log all its sets to a day in one action.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":  map[string]any{"type": "string", "description": "Program name, e.g. 'Push Day'"},
					"notes": map[string]any{"type": "string", "description": "Optional notes about the program"},
					"exercises": map[string]any{
						"type":        "array",
						"description": "Ordered exercises in the program",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"exercise": map[string]any{"type": "string", "description": "Exercise name, e.g. 'Barbell Bench Press'"},
								"sets":     map[string]any{"type": "integer", "description": "How many sets to log when the program is started (default 1)"},
								"reps":     map[string]any{"type": "integer", "description": "Target reps per set"},
								"weight":   map[string]any{"type": "number", "description": "Target weight in the user's units"},
								"rpe":      map[string]any{"type": "number", "description": "Optional target RPE, 1-10"},
							},
							"required": []string{"exercise"},
						},
					},
				},
				"required": []string{"name", "exercises"},
			},
		},
		{
			Name:        "list_programs",
			Description: "List the user's saved programs (workout templates) with their exercises. Use to see what programs exist before starting one or to answer questions about them.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "start_program",
			Description: "Log every set of a saved program to a day (defaults to today), so the user doesn't re-enter the exercises. Identify the program by id or by name. If a name matches more than one program, list the matches and ask which.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":   map[string]any{"type": "integer", "description": "Program id (from list_programs). Use this or name."},
					"name": map[string]any{"type": "string", "description": "Program name to look up (case-insensitive). Use this or id."},
					"date": map[string]any{"type": "string", "description": "YYYY-MM-DD; omit for today"},
				},
			},
		},
	}
}

// Chat runs a full turn: sends the conversation to Claude, executes any tool
// calls, and loops until Claude produces a final text answer. It returns the
// assistant's final text and whether the store was mutated (so the UI can
// know to refresh the log panel).
func (a *Agent) Chat(history []ChatMessage, userMsg string, images []ChatImage) (reply string, mutated bool, err error) {
	msgs := a.buildMessages(history, userMsg, images)

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

// buildMessages converts stored chat history plus the new user turn into API
// messages. User turns carry their attached photos as image blocks ahead of
// the text; earlier photos are reloaded from the store so follow-up questions
// ("what about the machine next to it?") still have the picture in context.
func (a *Agent) buildMessages(history []ChatMessage, userMsg string, images []ChatImage) []apiMessage {
	msgs := make([]apiMessage, 0, len(history)+1)
	for _, m := range history {
		// Skip placeholder replies still being generated (or failed): they have
		// no usable text and the API rejects empty assistant turns.
		if m.Role == "assistant" && (m.Status == "pending" || strings.TrimSpace(m.Content) == "") {
			continue
		}
		var imgs []ChatImage
		for _, ref := range m.Images {
			if ref.Data != nil {
				imgs = append(imgs, ref)
			} else if a.store != nil {
				if full, ok := a.store.getChatImage(ref.ID); ok {
					imgs = append(imgs, full)
				}
			}
		}
		parts := userParts(m.Content, imgs)
		if len(parts) == 0 {
			continue
		}
		msgs = append(msgs, apiMessage{Role: m.Role, Content: parts})
	}
	msgs = append(msgs, apiMessage{Role: "user", Content: userParts(userMsg, images)})
	return msgs
}

// userParts lays out a turn as [images..., text]. The API rejects empty text
// blocks, so a photo-only message carries a short stand-in caption.
func userParts(text string, images []ChatImage) []contentPart {
	parts := make([]contentPart, 0, len(images)+1)
	for _, img := range images {
		parts = append(parts, imagePart(img))
	}
	text = strings.TrimSpace(text)
	if text == "" && len(images) > 0 {
		text = "(photo attached, no caption)"
	}
	if text != "" {
		parts = append(parts, contentPart{Type: "text", Text: text})
	}
	return parts
}

func (a *Agent) call(req apiRequest) (*apiResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest("POST", a.endpoint(), bytes.NewReader(body))
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

	case "log_measurement":
		var in struct {
			Site  string  `json:"site"`
			Value float64 `json:"value"`
			Date  string  `json:"date"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if !isMeasurementSite(in.Site) {
			return fmt.Sprintf("Unknown measurement site %q. Valid sites: %s.", in.Site, strings.Join(measurementSiteSlugs(), ", ")), false, nil
		}
		if in.Date == "" {
			in.Date = today()
		}
		if err := a.store.logMeasurement(in.Date, in.Site, in.Value); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged %s %g on %s.", strings.ToLower(measurementLabel(in.Site)), in.Value, in.Date), true, nil

	case "get_measurement_history":
		var in struct {
			Site  string `json:"site"`
			Limit int    `json:"limit"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Site == "" {
			latest, err := a.store.latestMeasurements()
			if err != nil {
				return "", false, err
			}
			if len(latest) == 0 {
				return "No body measurements logged yet.", false, nil
			}
			var sb strings.Builder
			sb.WriteString("Latest measurement per site:\n")
			for _, site := range measurementSites {
				if m, ok := latest[site.Slug]; ok {
					fmt.Fprintf(&sb, "%s: %g (on %s)\n", site.Label, m.Value, m.Date)
				}
			}
			return sb.String(), false, nil
		}
		if !isMeasurementSite(in.Site) {
			return fmt.Sprintf("Unknown measurement site %q. Valid sites: %s.", in.Site, strings.Join(measurementSiteSlugs(), ", ")), false, nil
		}
		pts, err := a.store.measurementHistory(in.Site, in.Limit)
		if err != nil {
			return "", false, err
		}
		if len(pts) == 0 {
			return fmt.Sprintf("No %s measurements logged yet.", strings.ToLower(measurementLabel(in.Site))), false, nil
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%s history (oldest first):\n", measurementLabel(in.Site))
		for _, p := range pts {
			fmt.Fprintf(&sb, "%s: %g\n", p.Date, p.Value)
		}
		return sb.String(), false, nil

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

	case "create_program":
		var in struct {
			Name      string            `json:"name"`
			Notes     string            `json:"notes"`
			Exercises []ProgramExercise `json:"exercises"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			return "", false, fmt.Errorf("name is required")
		}
		// Keep only exercises that actually name a movement.
		var exs []ProgramExercise
		for _, e := range in.Exercises {
			e.Exercise = strings.TrimSpace(e.Exercise)
			if e.Exercise == "" {
				continue
			}
			exs = append(exs, e)
		}
		if len(exs) == 0 {
			return "", false, fmt.Errorf("a program needs at least one exercise")
		}
		id, err := a.store.createProgram(in.Name, strings.TrimSpace(in.Notes), exs)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Created program %q with %d exercises (#%d).", in.Name, len(exs), id), true, nil

	case "list_programs":
		programs, err := a.store.listPrograms()
		if err != nil {
			return "", false, err
		}
		if len(programs) == 0 {
			return "No programs saved yet.", false, nil
		}
		b, _ := json.Marshal(programs)
		return string(b), false, nil

	case "start_program":
		var in struct {
			ID   int64  `json:"id"`
			Name string `json:"name"`
			Date string `json:"date"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		// Resolve by name if no id was given.
		if in.ID == 0 {
			name := strings.TrimSpace(in.Name)
			if name == "" {
				return "", false, fmt.Errorf("provide a program id or name")
			}
			programs, err := a.store.listPrograms()
			if err != nil {
				return "", false, err
			}
			var matches []Program
			for _, p := range programs {
				if strings.EqualFold(strings.TrimSpace(p.Name), name) {
					matches = append(matches, p)
				}
			}
			switch len(matches) {
			case 0:
				return fmt.Sprintf("No program named %q. Use list_programs to see what exists.", name), false, nil
			case 1:
				in.ID = matches[0].ID
			default:
				var sb strings.Builder
				fmt.Fprintf(&sb, "Multiple programs named %q — ask which id:\n", name)
				for _, p := range matches {
					fmt.Fprintf(&sb, "  #%d (%d exercises)\n", p.ID, len(p.Exercises))
				}
				return sb.String(), false, nil
			}
		}
		if in.Date == "" {
			in.Date = today()
		}
		p, found := a.store.getProgram(in.ID)
		if !found {
			return fmt.Sprintf("No program with id #%d.", in.ID), false, nil
		}
		n, _, err := a.store.startProgram(in.ID, in.Date)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Started %q: logged %d sets to %s.", p.Name, n, in.Date), true, nil

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
	httpReq, err := http.NewRequest("POST", a.endpoint(), bytes.NewReader(body))
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
