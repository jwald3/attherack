package coach

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// Tools for lifting: sets, workouts, and the exercise library.

var logSetTool = tool{
	def: toolDef{
		Name:        "log_set",
		Description: "Log a single working set. Groups into the workout for the given date (defaults to today). weight is a plain number in the user's own units.",
		InputSchema: object(map[string]any{
			"exercise": prop("string", "Exercise name, e.g. 'Barbell Squat'"),
			"weight":   prop("number", "Weight lifted (user's units)"),
			"reps":     prop("integer", "Repetitions performed"),
			"rpe":      prop("number", "Optional rate of perceived exertion, 1-10"),
			"date":     dateProp(),
		}, "exercise", "weight", "reps"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
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
			in.Date = dates.Today()
		}
		set, err := a.store.LogSet(in.Date, in.Exercise, in.Weight, in.Reps, in.RPE)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Logged set #%d: %s %gx%d on %s", set.ID, in.Exercise, in.Weight, in.Reps, in.Date), true, nil
	},
}

var listWorkoutsTool = tool{
	def: toolDef{
		Name:        "list_workouts",
		Description: "List recent workouts (most recent first) with all their logged sets. Use to review history or overall trends.",
		InputSchema: object(map[string]any{
			"limit": prop("integer", "How many recent workouts to return (default 15)"),
		}),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Limit int `json:"limit"`
		}
		_ = json.Unmarshal(input, &in)
		if in.Limit == 0 {
			in.Limit = 15
		}
		workouts, err := a.store.ListWorkouts(in.Limit)
		if err != nil {
			return "", false, err
		}
		b, _ := json.Marshal(workouts)
		return string(b), false, nil
	},
}

var getExerciseHistoryTool = tool{
	def: toolDef{
		Name:        "get_exercise_history",
		Description: "Get the recent set history for a single exercise, most recent first. Use to analyze progression on a specific lift.",
		InputSchema: object(map[string]any{
			"exercise": prop("string", "Exercise name to look up"),
			"limit":    prop("integer", "Max sets to return (default 30)"),
		}, "exercise"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Exercise string `json:"exercise"`
			Limit    int    `json:"limit"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		hist, err := a.store.ExerciseHistory(in.Exercise, in.Limit)
		if err != nil {
			return "", false, err
		}
		if len(hist) == 0 {
			return fmt.Sprintf("No logged sets for %q yet.", in.Exercise), false, nil
		}
		var sb strings.Builder
		for _, h := range hist {
			// Include the set id so it can be referenced by delete_set / update_set.
			fmt.Fprintf(&sb, "set #%d — %s: %s %gx%d%s\n", h.Set.ID, h.Date, h.Set.Exercise, h.Set.Weight, h.Set.Reps, fmtRPE(h.Set.RPE))
		}
		return sb.String(), false, nil
	},
}

var searchExercisesTool = tool{
	def: toolDef{
		Name:        "search_exercises",
		Description: "Search the exercise database. Filter by free-text query, target muscle, and/or equipment. Returns name, target muscles, equipment, and category.",
		InputSchema: object(map[string]any{
			"query":     prop("string", "Free text, e.g. 'row' or 'squat'"),
			"muscle":    prop("string", "Target muscle, e.g. 'quadriceps', 'chest', 'lats'"),
			"equipment": prop("string", "e.g. 'barbell', 'dumbbell', 'body only', 'cable'"),
			"limit":     prop("integer", "Max results (default 15)"),
		}),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
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
			out[i] = slim{e.Name, e.PrimaryMuscles, e.EquipmentName(), e.Category}
		}
		b, _ := json.Marshal(out)
		return string(b), false, nil
	},
}

var createExerciseTool = tool{
	def: toolDef{
		Name:        "create_exercise",
		Description: "Add a new custom exercise to the user's library when they mention a movement that isn't already there (e.g. 'parallel grip lat pulldown'). Check search_exercises first to avoid duplicates. After creating it you can log sets against it.",
		InputSchema: object(map[string]any{
			"name":              prop("string", "Exercise name"),
			"primary_muscles":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "e.g. ['lats']"},
			"secondary_muscles": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			"equipment":         prop("string", "e.g. 'cable', 'machine', 'barbell'"),
			"category":          enumProp(categories, ""),
		}, "name"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
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
		ex, err := a.store.AddCustomExercise(in.Name, in.Equipment, "", in.Category, in.PrimaryMuscles, in.SecondaryMuscles)
		if err != nil {
			if errors.Is(err, store.ErrExerciseExists) {
				return fmt.Sprintf("%q already exists in the library — no need to add it.", in.Name), false, nil
			}
			return "", false, err
		}
		a.lib.Add(ex)
		return fmt.Sprintf("Added custom exercise %q. It's now searchable and you can log sets against it.", ex.Name), true, nil
	},
}

var setWorkoutNotesTool = tool{
	def: toolDef{
		Name:        "set_workout_notes",
		Description: "Attach or update freeform notes on a workout for a given date (defaults to today). Use for how a session felt, injuries, focus, etc.",
		InputSchema: object(map[string]any{
			"notes": map[string]any{"type": "string"},
			"date":  dateProp(),
		}, "notes"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Notes string `json:"notes"`
			Date  string `json:"date"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if in.Date == "" {
			in.Date = dates.Today()
		}
		if _, err := a.store.SetWorkoutNotes(in.Date, in.Notes); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Saved notes for %s.", in.Date), true, nil
	},
}

var updateSetTool = tool{
	def: toolDef{
		Name:        "update_set",
		Description: "Fix an already-logged set by its id: overwrite its weight, reps and/or rpe. Use this to correct a mistake (e.g. a typo in reps) instead of logging a duplicate. Get the set id from get_exercise_history or list_workouts first.",
		InputSchema: object(map[string]any{
			"id":     prop("integer", "The set id to update (from get_exercise_history or list_workouts)"),
			"weight": prop("number", "Corrected weight (user's units). Omit to keep the current value."),
			"reps":   prop("integer", "Corrected reps. Omit to keep the current value."),
			"rpe":    prop("number", "Corrected RPE 1-10. Omit to keep the current value."),
		}, "id"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
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
		cur, err := a.store.GetSet(in.ID)
		if errors.Is(err, store.ErrNotFound) {
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
		updated, err := a.store.UpdateSet(in.ID, weight, reps, rpe)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Updated set #%d: %s %gx%d on %s.", updated.Set.ID, updated.Set.Exercise, updated.Set.Weight, updated.Set.Reps, updated.Date), true, nil
	},
}

var deleteSetTool = tool{
	def: toolDef{
		Name:        "delete_set",
		Description: "Delete a mistakenly-logged set by its id. Use for a duplicate or an entry that should not exist. Get the set id from get_exercise_history or list_workouts first, and confirm with the user which set before deleting if there's any ambiguity.",
		InputSchema: object(map[string]any{
			"id": prop("integer", "The set id to delete (from get_exercise_history or list_workouts)"),
		}, "id"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			ID int64 `json:"id"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		if in.ID == 0 {
			return "", false, fmt.Errorf("id is required")
		}
		cur, err := a.store.GetSet(in.ID)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Sprintf("No set with id #%d.", in.ID), false, nil
		}
		if err != nil {
			return "", false, err
		}
		if err := a.store.DeleteSet(in.ID); err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Deleted set #%d: %s %gx%d on %s.", cur.Set.ID, cur.Set.Exercise, cur.Set.Weight, cur.Set.Reps, cur.Date), true, nil
	},
}
