package coach

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// Tools for programs: reusable workout templates.

var createProgramTool = tool{
	def: toolDef{
		Name:        "create_program",
		Description: "Save a reusable workout template ('program'): a named, ordered list of exercises with target sets/reps/weight. Use when the user describes a routine they want to reuse (e.g. a push day). The user can later start it to log all its sets to a day in one action.",
		InputSchema: object(map[string]any{
			"name":  prop("string", "Program name, e.g. 'Push Day'"),
			"notes": prop("string", "Optional notes about the program"),
			"exercises": map[string]any{
				"type":        "array",
				"description": "Ordered exercises in the program",
				"items": object(map[string]any{
					"exercise": prop("string", "Exercise name, e.g. 'Barbell Bench Press'"),
					"sets":     prop("integer", "How many sets to log when the program is started (default 1)"),
					"reps":     prop("integer", "Target reps per set"),
					"weight":   prop("number", "Target weight in the user's units"),
					"rpe":      prop("number", "Optional target RPE, 1-10"),
				}, "exercise"),
			},
		}, "name", "exercises"),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
		var in struct {
			Name      string                  `json:"name"`
			Notes     string                  `json:"notes"`
			Exercises []store.ProgramExercise `json:"exercises"`
		}
		if err := json.Unmarshal(input, &in); err != nil {
			return "", false, err
		}
		in.Name = strings.TrimSpace(in.Name)
		if in.Name == "" {
			return "", false, fmt.Errorf("name is required")
		}
		// Keep only exercises that actually name a movement.
		var exs []store.ProgramExercise
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
		id, err := a.store.CreateProgram(in.Name, strings.TrimSpace(in.Notes), exs)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Created program %q with %d exercises (#%d).", in.Name, len(exs), id), true, nil
	},
}

var listProgramsTool = tool{
	def: toolDef{
		Name:        "list_programs",
		Description: "List the user's saved programs (workout templates) with their exercises. Use to see what programs exist before starting one or to answer questions about them.",
		InputSchema: object(map[string]any{}),
	},
	run: func(a *Agent, _ json.RawMessage) (string, bool, error) {
		programs, err := a.store.ListPrograms()
		if err != nil {
			return "", false, err
		}
		if len(programs) == 0 {
			return "No programs saved yet.", false, nil
		}
		b, _ := json.Marshal(programs)
		return string(b), false, nil
	},
}

var startProgramTool = tool{
	def: toolDef{
		Name:        "start_program",
		Description: "Log every set of a saved program to a day (defaults to today), so the user doesn't re-enter the exercises. Identify the program by id or by name. If a name matches more than one program, list the matches and ask which.",
		InputSchema: object(map[string]any{
			"id":   prop("integer", "Program id (from list_programs). Use this or name."),
			"name": prop("string", "Program name to look up (case-insensitive). Use this or id."),
			"date": dateProp(),
		}),
	},
	run: func(a *Agent, input json.RawMessage) (string, bool, error) {
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
			programs, err := a.store.ListPrograms()
			if err != nil {
				return "", false, err
			}
			var matches []store.Program
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
			in.Date = dates.Today()
		}
		p, found := a.store.GetProgram(in.ID)
		if !found {
			return fmt.Sprintf("No program with id #%d.", in.ID), false, nil
		}
		n, _, err := a.store.StartProgram(in.ID, in.Date)
		if err != nil {
			return "", false, err
		}
		return fmt.Sprintf("Started %q: logged %d sets to %s.", p.Name, n, in.Date), true, nil
	},
}
