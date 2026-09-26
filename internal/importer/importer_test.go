package importer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jwald3/attherack/internal/store"
)

const workoutsCSV = `workout_started_at,exercise_name,exercise_kind,weight,reps,duration_seconds,distance_miles,body_weight,workout_notes,target_tags
2026-05-04T22:22:30Z,Barbell Squat,strength,225,5,,,195.5,Felt good,Legs
2026-05-04T22:30:00Z,Barbell Squat,strength,225,5,,,,,
2026-05-04T23:00:00Z,Treadmill Run,cardio,,,1800,3.1,,,
2026-05-06T18:00:00Z,Bench Press,strength,185,5,,,194,,
`

func TestWorkoutsAndMigrateCardio(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	path := filepath.Join(t.TempDir(), "workouts.csv")
	if err := os.WriteFile(path, []byte(workoutsCSV), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := Workouts(st, path); err != nil {
		t.Fatal(err)
	}
	workouts, _ := st.ListWorkouts(10)
	if len(workouts) != 2 {
		t.Fatalf("want 2 workouts, got %+v", workouts)
	}
	if day := workouts[1]; day.Date != "2026-05-04" || len(day.Sets) != 2 || day.Notes != "Legs — Felt good" {
		t.Fatalf("first day wrong: %+v", day)
	}
	if bw, _ := st.ListBodyweight(10); len(bw) != 2 {
		t.Fatalf("want 2 bodyweight entries, got %+v", bw)
	}

	// Simulate what an older importer left behind: a 0x0 marker set for the
	// cardio row and a "Cardio:" line in the notes.
	if _, err := st.LogSet("2026-05-04", "Treadmill Run", 0, 0, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetWorkoutNotes("2026-05-04", "Legs — Felt good\n\nCardio: Treadmill Run 30 min"); err != nil {
		t.Fatal(err)
	}

	// Migrating twice is idempotent: one session, marker and note line gone.
	for i := 0; i < 2; i++ {
		if err := MigrateCardio(st, path); err != nil {
			t.Fatal(err)
		}
	}
	cardio, _ := st.ListCardio(10)
	if len(cardio) != 1 || cardio[0].Type != "Treadmill Run" || cardio[0].DurationSeconds != 1800 {
		t.Fatalf("cardio wrong: %+v", cardio)
	}
	workouts, _ = st.ListWorkouts(10)
	if day := workouts[1]; len(day.Sets) != 2 || day.Notes != "Legs — Felt good" {
		t.Fatalf("markers not cleaned: %+v", day)
	}
}
