package main

import (
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := openStore(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	// Close before TempDir cleanup so Windows can delete the file.
	t.Cleanup(func() { st.db.Close() })
	return st
}

func TestProgramCreateGetStartDelete(t *testing.T) {
	st := newTestStore(t)

	rpe := 8.0
	id, err := st.createProgram("Push Day", "chest & shoulders", []ProgramExercise{
		{Exercise: "Barbell Bench Press", Sets: 3, Reps: 5, Weight: 185},
		{Exercise: "Overhead Press", Sets: 3, Reps: 8, Weight: 95, RPE: &rpe},
	})
	if err != nil {
		t.Fatal(err)
	}

	// getProgram returns the exercises in order with correct fields.
	p, ok := st.getProgram(id)
	if !ok {
		t.Fatal("getProgram: not found")
	}
	if p.Name != "Push Day" || len(p.Exercises) != 2 {
		t.Fatalf("unexpected program: %+v", p)
	}
	if p.Exercises[0].Exercise != "Barbell Bench Press" || p.Exercises[0].Sets != 3 || p.Exercises[0].Reps != 5 || p.Exercises[0].Weight != 185 {
		t.Fatalf("exercise 0 wrong: %+v", p.Exercises[0])
	}
	if p.Exercises[1].Exercise != "Overhead Press" || p.Exercises[1].RPE == nil || *p.Exercises[1].RPE != 8.0 {
		t.Fatalf("exercise 1 wrong: %+v", p.Exercises[1])
	}

	// startProgram logs the right number of sets (3 + 3 = 6) with correct values.
	n, found, err := st.startProgram(id, "2026-09-25")
	if err != nil || !found {
		t.Fatalf("startProgram: n=%d found=%v err=%v", n, found, err)
	}
	if n != 6 {
		t.Fatalf("expected 6 sets logged, got %d", n)
	}
	bench, err := st.exerciseHistory("Barbell Bench Press", 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(bench) != 3 {
		t.Fatalf("expected 3 bench sets, got %d", len(bench))
	}
	for _, h := range bench {
		if h.Set.Weight != 185 || h.Set.Reps != 5 || h.Date != "2026-09-25" {
			t.Fatalf("bench set wrong: %+v on %s", h.Set, h.Date)
		}
	}
	ohp, _ := st.exerciseHistory("Overhead Press", 100)
	if len(ohp) != 3 {
		t.Fatalf("expected 3 ohp sets, got %d", len(ohp))
	}

	// listPrograms shows the program.
	all, err := st.listPrograms()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != id || len(all[0].Exercises) != 2 {
		t.Fatalf("listPrograms wrong: %+v", all)
	}

	// deleteProgram removes it and cascades its exercises.
	if err := st.deleteProgram(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.getProgram(id); ok {
		t.Fatal("program still present after delete")
	}
	var leftover int
	if err := st.db.QueryRow(`SELECT COUNT(1) FROM program_exercises WHERE program_id = ?`, id).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("expected exercises to cascade-delete, %d remain", leftover)
	}

	// startProgram on a missing id reports not-found, no error.
	if n, found, err := st.startProgram(id, "2026-09-25"); err != nil || found || n != 0 {
		t.Fatalf("start of deleted program: n=%d found=%v err=%v", n, found, err)
	}
}

func TestCoachHasProgramTools(t *testing.T) {
	a := &Agent{}
	var names []string
	for _, td := range a.tools() {
		names = append(names, td.Name)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"create_program", "list_programs", "start_program"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing tool %q; have: %s", want, joined)
		}
	}
}
