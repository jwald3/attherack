package store

import "testing"

func TestDistinctLoggedExercises(t *testing.T) {
	st := newTestStore(t)

	// No sets yet → no names.
	names, err := st.DistinctLoggedExercises()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 0 {
		t.Fatalf("expected no logged exercises, got %v", names)
	}

	// Log the same exercise twice plus another; distinct names come back sorted.
	if _, err := st.LogSet("2026-09-01", "Close Grip Lat Pulldown", 120, 8, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LogSet("2026-09-02", "Close Grip Lat Pulldown", 125, 8, nil, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.LogSet("2026-09-02", "Barbell Squat", 225, 5, nil, ""); err != nil {
		t.Fatal(err)
	}

	names, err = st.DistinctLoggedExercises()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"Barbell Squat", "Close Grip Lat Pulldown"}
	if len(names) != len(want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("got %v, want %v", names, want)
		}
	}
}
