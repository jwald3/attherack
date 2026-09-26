// Package seed fills an empty database with demo data.
package seed

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/store"
)

// Demo fills an empty database with about six weeks of realistic fake
// training, cardio, bodyweight, supplement and food data, so a new checkout
// shows every tab populated. It refuses to touch a database that already has
// data. Run it with: go run . -seed-demo -db demo.db
func Demo(s *store.Store) error {
	if has, err := s.HasData(); err != nil {
		return err
	} else if has {
		return fmt.Errorf("database already has data; use -db to seed a new file instead")
	}

	rng := rand.New(rand.NewSource(42)) // fixed seed: same demo every time
	const weeks = 6
	start := time.Now().AddDate(0, 0, -weeks*7+1)
	day := func(i int) string { return start.AddDate(0, 0, i).Format(dates.Layout) }
	rpe := func(v float64) *float64 { return &v }

	type lift struct {
		name       string
		base, step float64 // starting weight and weekly increase
		sets, reps int
	}
	// Upper/lower split on Mon/Tue/Thu/Fri (by offset within each week).
	sessions := map[int][]lift{
		0: {{"Barbell Squat", 185, 5, 3, 5}, {"Romanian Deadlift", 155, 5, 3, 8}, {"Leg Press", 270, 10, 3, 10}},
		1: {{"Barbell Bench Press - Medium Grip", 155, 5, 3, 5}, {"Bent Over Barbell Row", 135, 5, 3, 8}, {"Lying Triceps Press", 60, 2.5, 3, 10}},
		3: {{"Barbell Deadlift", 245, 10, 1, 5}, {"Barbell Squat", 155, 5, 2, 8}, {"Wide-Grip Lat Pulldown", 120, 5, 3, 10}},
		4: {{"Standing Military Press", 95, 2.5, 3, 5}, {"Seated Cable Rows", 110, 5, 3, 10}, {"Incline Dumbbell Press", 50, 2.5, 3, 10}, {"Dumbbell Bicep Curl", 25, 0, 3, 12}},
	}
	notes := []string{"Felt strong today.", "Slept badly, kept it moving.", "Grip was the limiter on rows.", "", "", ""}

	bw := 196.0
	for i := 0; i < weeks*7; i++ {
		date, week, dow := day(i), i/7, i%7

		for _, l := range sessions[dow] {
			for set := 0; set < l.sets; set++ {
				r := l.reps
				if set == l.sets-1 && rng.Intn(3) == 0 {
					r-- // the occasional missed rep on the last set
				}
				if _, err := s.LogSet(date, l.name, l.base+l.step*float64(week), r, rpe(7+float64(set)*0.5)); err != nil {
					return err
				}
			}
		}
		if len(sessions[dow]) > 0 {
			if note := notes[rng.Intn(len(notes))]; note != "" {
				if _, err := s.SetWorkoutNotes(date, note); err != nil {
					return err
				}
			}
		}

		// Cardio: walks most days, an easy run on Wednesdays and weekends.
		if rng.Intn(4) > 0 {
			mins := 20 + rng.Intn(3)*10
			if _, err := s.LogCardio(date, "Walking", mins*60, float64(mins)/20*(0.95+rng.Float64()*0.1)); err != nil {
				return err
			}
		}
		if dow == 2 || dow == 5 {
			miles := 3.1
			secs := int(miles * float64(600-week*6+rng.Intn(20))) // pace improves ~6s/mi per week
			if _, err := s.LogCardio(date, "Running", secs, miles); err != nil {
				return err
			}
		}

		// Bodyweight: a slow cut with daily noise, weighed most mornings.
		bw -= 0.12
		if rng.Intn(5) > 0 {
			if err := s.LogBodyweight(date, float64(int((bw+rng.Float64()*1.6-0.8)*10))/10); err != nil {
				return err
			}
		}

		// Supplements: creatine nearly every day, vitamin D most days.
		if rng.Intn(10) > 0 {
			if _, err := s.LogSupplement(date, "Creatine", 5, "g"); err != nil {
				return err
			}
		}
		if rng.Intn(3) > 0 {
			if _, err := s.LogSupplement(date, "Vitamin D", 2000, "IU"); err != nil {
				return err
			}
		}
	}

	// Food for the last few days: mostly name + notes, some with macros.
	num := func(v float64) *float64 { return &v }
	foods := []store.FoodLog{
		{Meal: "breakfast", Name: "Oatmeal with berries", Notes: "1 cup oats, frozen blueberries, honey"},
		{Meal: "lunch", Name: "Chicken burrito bowl", Notes: "double chicken, no rice", Calories: num(750), Protein: num(62), Fat: num(28)},
		{Meal: "snack", Name: "Greek yogurt", Notes: "plain, 2%", Protein: num(20)},
		{Meal: "dinner", Name: "Salmon, potatoes and green beans", Notes: "about 6 oz salmon"},
		{Meal: "breakfast", Name: "Scrambled eggs and toast", Notes: "3 eggs, 2 slices sourdough"},
		{Meal: "lunch", Name: "Turkey sandwich", Notes: "deli turkey, swiss, on wheat"},
		{Meal: "dinner", Name: "Spaghetti with meat sauce", Notes: "big bowl"},
		{Meal: "snack", Name: "Protein shake", Notes: "1 scoop whey in milk", Calories: num(280), Protein: num(35), Carbs: num(15), Fat: num(8)},
	}
	for i, f := range foods {
		f.Date = day(weeks*7 - 1 - i/4) // four entries per day: today and yesterday
		if _, err := s.LogFood(f); err != nil {
			return err
		}
	}

	// One example coach conversation so the Coach tab isn't empty.
	id, err := s.CreateThread("Welcome to the demo")
	if err != nil {
		return err
	}
	if err := s.AddChatMessage(id, "user", "What can you help me with?"); err != nil {
		return err
	}
	return s.AddChatMessage(id, "assistant", "This is **demo data**, so explore freely. With an Anthropic API key added, I can:\n\n"+
		"- **Log things** as you describe them: \"3x5 squats at 215\", \"walked 2 miles\", \"had eggs and toast\"\n"+
		"- **Answer questions** from your real history: \"how's my bench trending?\"\n"+
		"- **Plan training**: \"suggest a pull day\"\n\n"+
		"| Tab | What it tracks |\n|---|---|\n| Training | Sets, workouts, exercise library |\n| Cardio | Distance, time, pace |\n"+
		"| Bodyweight | Weigh-ins and trend |\n| Diet | Food, with optional macros |\n| Supplements | Daily doses and consistency |")
}
