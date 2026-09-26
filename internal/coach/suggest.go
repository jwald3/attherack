package coach

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Quick single-shot requests to the cheap model: custom-exercise metadata and
// conversation titles.

// categories is the free-exercise-db category vocabulary.
var categories = []string{"strength", "stretching", "plyometrics", "cardio", "strongman", "powerlifting"}

// ExerciseSuggestion is the structured guess for a custom exercise's metadata.
type ExerciseSuggestion struct {
	PrimaryMuscles   []string `json:"primary_muscles"`
	SecondaryMuscles []string `json:"secondary_muscles"`
	Equipment        string   `json:"equipment"`
	Category         string   `json:"category"`
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

	stringList := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"primary_muscles":   stringList,
			"secondary_muscles": stringList,
			"equipment":         enumProp([]string{"barbell", "dumbbell", "cable", "machine", "kettlebells", "bands", "body only", "medicine ball", "exercise ball", "foam roll", "e-z curl bar", "other"}, ""),
			"category":          enumProp(categories, ""),
		},
		"required":             []string{"primary_muscles", "secondary_muscles", "equipment", "category"},
		"additionalProperties": false,
	}

	resp, err := a.call(apiRequest{
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
	})
	if err != nil {
		// Show the API's own message, which reads better in the form than ours.
		var apiErr *apiError
		if errors.As(err, &apiErr) {
			if apiErr.Message != "" {
				return ExerciseSuggestion{}, errors.New(apiErr.Message)
			}
			return ExerciseSuggestion{}, fmt.Errorf("suggest failed (%d)", apiErr.StatusCode)
		}
		return ExerciseSuggestion{}, err
	}
	// With output_config.format the first text block holds valid JSON.
	var sug ExerciseSuggestion
	if err := json.Unmarshal([]byte(collectText(resp.Content)), &sug); err != nil {
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
