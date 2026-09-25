package main

import (
	"path/filepath"
	"testing"
)

func TestSeedDemo(t *testing.T) {
	s, err := openStore(filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()

	if err := seedDemo(s); err != nil {
		t.Fatalf("seed: %v", err)
	}
	for table, min := range map[string]int{
		"workouts": 20, "sets": 200, "cardio_sessions": 20, "bodyweight": 20,
		"supplement_logs": 30, "food_logs": 8, "chat_threads": 1, "chat_messages": 2,
	} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM ` + table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n < min {
			t.Errorf("%s: %d rows, want at least %d", table, n, min)
		}
	}

	// Seeding again must refuse rather than duplicate or mix into real data.
	if err := seedDemo(s); err == nil {
		t.Error("second seed should fail on a non-empty database")
	}
}
