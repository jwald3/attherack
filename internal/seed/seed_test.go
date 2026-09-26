package seed

import (
	"path/filepath"
	"testing"

	"github.com/jwald3/attherack/internal/store"
)

func TestDemo(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "demo.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := Demo(s); err != nil {
		t.Fatalf("seed: %v", err)
	}

	workouts, _ := s.ListWorkouts(1000)
	sets := 0
	for _, w := range workouts {
		sets += len(w.Sets)
	}
	cardio, _ := s.ListCardio(1000)
	bodyweight, _ := s.ListBodyweight(1000)
	supplements, _ := s.ListSupplementLogs(1000)
	food, _ := s.ListFoodSince("0000-00-00")
	threads, _ := s.ListThreads(100)
	var messages []store.ChatMessage
	if len(threads) > 0 {
		messages, _ = s.ListChatMessages(threads[0].ID, 100)
	}
	for name, c := range map[string]struct{ got, min int }{
		"workouts":        {len(workouts), 20},
		"sets":            {sets, 200},
		"cardio_sessions": {len(cardio), 20},
		"bodyweight":      {len(bodyweight), 20},
		"supplement_logs": {len(supplements), 30},
		"food_logs":       {len(food), 8},
		"chat_threads":    {len(threads), 1},
		"chat_messages":   {len(messages), 2},
	} {
		if c.got < c.min {
			t.Errorf("%s: %d rows, want at least %d", name, c.got, c.min)
		}
	}

	// Seeding again must refuse rather than duplicate or mix into real data.
	if err := Demo(s); err == nil {
		t.Error("second seed should fail on a non-empty database")
	}
}
