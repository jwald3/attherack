package store

import "testing"

func TestProgramCreateGetStartDelete(t *testing.T) {
	st := newTestStore(t)

	rpe := 8.0
	id, err := st.CreateProgram("Push Day", "chest & shoulders", []ProgramExercise{
		{Exercise: "Barbell Bench Press", Sets: 3, Reps: 5, Weight: 185},
		{Exercise: "Overhead Press", Sets: 3, Reps: 8, Weight: 95, RPE: &rpe},
	})
	if err != nil {
		t.Fatal(err)
	}

	// GetProgram returns the exercises in order with correct fields.
	p, ok := st.GetProgram(id)
	if !ok {
		t.Fatal("GetProgram: not found")
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

	// StartProgram logs the right number of sets (3 + 3 = 6) with correct values.
	n, found, err := st.StartProgram(id, "2026-09-25")
	if err != nil || !found {
		t.Fatalf("StartProgram: n=%d found=%v err=%v", n, found, err)
	}
	if n != 6 {
		t.Fatalf("expected 6 sets logged, got %d", n)
	}
	bench, err := st.ExerciseHistory("Barbell Bench Press", 100)
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
	ohp, _ := st.ExerciseHistory("Overhead Press", 100)
	if len(ohp) != 3 {
		t.Fatalf("expected 3 ohp sets, got %d", len(ohp))
	}

	// ListPrograms shows the program.
	all, err := st.ListPrograms()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != id || len(all[0].Exercises) != 2 {
		t.Fatalf("ListPrograms wrong: %+v", all)
	}

	// DeleteProgram removes it and cascades its exercises.
	if err := st.DeleteProgram(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.GetProgram(id); ok {
		t.Fatal("program still present after delete")
	}
	var leftover int
	if err := st.db.QueryRow(`SELECT COUNT(1) FROM program_exercises WHERE program_id = ?`, id).Scan(&leftover); err != nil {
		t.Fatal(err)
	}
	if leftover != 0 {
		t.Fatalf("expected exercises to cascade-delete, %d remain", leftover)
	}

	// StartProgram on a missing id reports not-found, no error.
	if n, found, err := st.StartProgram(id, "2026-09-25"); err != nil || found || n != 0 {
		t.Fatalf("start of deleted program: n=%d found=%v err=%v", n, found, err)
	}
}
