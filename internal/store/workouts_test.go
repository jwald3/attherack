package store

import "testing"

func TestExerciseGroups(t *testing.T) {
	// A day where the same exercise is logged non-consecutively: bench, squat,
	// then bench again. All bench sets should merge under one group, and groups
	// should appear in first-performed order.
	rpe := 8.0
	w := Workout{
		Date: "2026-09-26",
		Sets: []Set{
			{ID: 1, Exercise: "Bench Press", Weight: 135, Reps: 5},
			{ID: 2, Exercise: "Bench Press", Weight: 135, Reps: 5, RPE: &rpe},
			{ID: 3, Exercise: "Squat", Weight: 225, Reps: 5},
			{ID: 4, Exercise: "Bench Press", Weight: 155, Reps: 3},
			{ID: 5, Exercise: "Squat", Weight: 225, Reps: 5},
		},
	}

	groups := w.ExerciseGroups()
	if len(groups) != 2 {
		t.Fatalf("expected 2 groups (Bench, Squat), got %d: %+v", len(groups), groups)
	}
	if groups[0].Exercise != "Bench Press" || groups[1].Exercise != "Squat" {
		t.Fatalf("group order wrong: %q then %q", groups[0].Exercise, groups[1].Exercise)
	}
	// Bench: all three sets merged, in logged order (ids 1,2,4).
	if len(groups[0].Sets) != 3 {
		t.Fatalf("expected 3 bench sets, got %d", len(groups[0].Sets))
	}
	if groups[0].Sets[0].ID != 1 || groups[0].Sets[1].ID != 2 || groups[0].Sets[2].ID != 4 {
		t.Fatalf("bench sets out of order: %d, %d, %d",
			groups[0].Sets[0].ID, groups[0].Sets[1].ID, groups[0].Sets[2].ID)
	}
	if len(groups[1].Sets) != 2 {
		t.Fatalf("expected 2 squat sets, got %d", len(groups[1].Sets))
	}

	// Case-insensitive merge: "bench press" and "Bench Press" are one group.
	w2 := Workout{Sets: []Set{
		{ID: 1, Exercise: "Bench Press", Weight: 135, Reps: 5},
		{ID: 2, Exercise: "bench press", Weight: 140, Reps: 5},
	}}
	if g := w2.ExerciseGroups(); len(g) != 1 || len(g[0].Sets) != 2 {
		t.Fatalf("case-insensitive merge failed: %+v", g)
	}

	// Empty workout yields no groups.
	if g := (Workout{}).ExerciseGroups(); len(g) != 0 {
		t.Fatalf("empty workout should have no groups, got %+v", g)
	}
}
