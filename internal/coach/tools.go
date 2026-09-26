package coach

import (
	"encoding/json"
	"fmt"
)

// tool pairs a tool's schema (what Claude sees) with its implementation.
// Tools are defined next to the data they touch, in tools_*.go.
type tool struct {
	def toolDef
	// run executes a call and returns a text result for Claude, plus whether
	// it changed the user's data (so the UI knows to refresh).
	run func(a *Agent, input json.RawMessage) (result string, mutated bool, err error)
}

// allTools is every tool the coach can call, in the order the API sees them.
var allTools = []tool{
	logSetTool,
	listWorkoutsTool,
	getExerciseHistoryTool,
	searchExercisesTool,
	logCardioTool,
	getCardioHistoryTool,
	logSupplementTool,
	getSupplementHistoryTool,
	logFoodTool,
	getFoodHistoryTool,
	getBodyweightHistoryTool,
	logBodyweightTool,
	logMeasurementTool,
	getMeasurementHistoryTool,
	createExerciseTool,
	setWorkoutNotesTool,
	updateSetTool,
	deleteSetTool,
	createProgramTool,
	listProgramsTool,
	startProgramTool,
}

var toolsByName = func() map[string]tool {
	m := make(map[string]tool, len(allTools))
	for _, t := range allTools {
		m[t.def.Name] = t
	}
	return m
}()

// toolDefs returns the schemas sent with every coach request.
func toolDefs() []toolDef {
	defs := make([]toolDef, len(allTools))
	for i, t := range allTools {
		defs[i] = t.def
	}
	return defs
}

func errUnknownTool(name string) error { return fmt.Errorf("unknown tool %q", name) }

// Schema helpers: these keep the tool definitions readable.

func object(props map[string]any, required ...string) map[string]any {
	s := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		s["required"] = required
	}
	return s
}

func prop(typ, description string) map[string]any {
	p := map[string]any{"type": typ}
	if description != "" {
		p["description"] = description
	}
	return p
}

func enumProp(values []string, description string) map[string]any {
	p := prop("string", description)
	p["enum"] = values
	return p
}

func dateProp() map[string]any { return prop("string", "YYYY-MM-DD; omit for today") }
