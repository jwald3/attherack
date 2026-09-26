package coach

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCoachHasTools(t *testing.T) {
	var names []string
	for _, td := range toolDefs() {
		names = append(names, td.Name)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{
		"create_program", "list_programs", "start_program",
		"log_measurement", "get_measurement_history",
	} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing tool %q; have: %s", want, joined)
		}
	}
	if len(toolsByName) != len(allTools) {
		t.Fatalf("duplicate tool names: %s", joined)
	}
}

func TestRunToolDispatch(t *testing.T) {
	a := &Agent{store: newTestStore(t)}

	out, mutated, err := a.runTool("log_bodyweight", json.RawMessage(`{"weight":190.5,"date":"2026-09-25"}`))
	if err != nil || !mutated || out != "Recorded bodyweight 190.5 on 2026-09-25." {
		t.Fatalf("log_bodyweight: %q mutated=%v err=%v", out, mutated, err)
	}
	out, mutated, err = a.runTool("get_bodyweight_history", nil)
	if err != nil || mutated || !strings.Contains(out, "2026-09-25: 190.5") {
		t.Fatalf("get_bodyweight_history: %q mutated=%v err=%v", out, mutated, err)
	}
	if _, _, err := a.runTool("no_such_tool", nil); err == nil {
		t.Fatal("unknown tool should error")
	}
}
