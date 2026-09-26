// Package importer loads ChimpFitness CSV exports, run once via the -import-*
// and -migrate-cardio flags. It's intentionally tolerant: unknown columns are
// ignored and malformed rows are skipped with a warning.
package importer

import (
	"encoding/csv"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/jwald3/attherack/internal/store"
)

// muscleMap normalizes the exporter's Title-Case muscle names to the lowercase
// vocabulary used by free-exercise-db (and the app's search facets).
var muscleMap = map[string]string{
	"front delts": "shoulders",
	"side delts":  "shoulders",
	"rear delts":  "shoulders",
	"delts":       "shoulders",
	"shoulders":   "shoulders",
	"triceps":     "triceps",
	"biceps":      "biceps",
	"forearms":    "forearms",
	"chest":       "chest",
	"upper chest": "chest",
	"lats":        "lats",
	"back":        "middle back",
	"rhomboids":   "middle back",
	"middle back": "middle back",
	"upper back":  "middle back",
	"traps":       "traps",
	"lower back":  "lower back",
	"core":        "abdominals",
	"abs":         "abdominals",
	"abdominals":  "abdominals",
	"obliques":    "abdominals",
	"quads":       "quadriceps",
	"quadriceps":  "quadriceps",
	"hamstrings":  "hamstrings",
	"glutes":      "glutes",
	"calves":      "calves",
	"adductors":   "adductors",
	"abductors":   "abductors",
	"neck":        "neck",
}

// equipmentMap normalizes the exporter's equipment values to the dataset's.
var equipmentMap = map[string]string{
	"cables":        "cable",
	"cable":         "cable",
	"bodyweight":    "body only",
	"body only":     "body only",
	"smith_machine": "machine",
	"machine":       "machine",
	"dumbbell":      "dumbbell",
	"barbell":       "barbell",
	"kettlebell":    "kettlebells",
	"kettlebells":   "kettlebells",
	"bands":         "bands",
	"band":          "bands",
}

func normMuscle(s string) string {
	key := strings.ToLower(strings.TrimSpace(s))
	if m, ok := muscleMap[key]; ok {
		return m
	}
	return key // pass through unknowns rather than dropping them
}

func normEquipment(s string) string {
	key := strings.ToLower(strings.TrimSpace(s))
	if e, ok := equipmentMap[key]; ok {
		return e
	}
	return key
}

func normMuscleList(raw, sep string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(raw, sep) {
		m := normMuscle(part)
		if m != "" && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	return out
}

// Exercises loads the ChimpFitness exercises CSV as custom exercises.
func Exercises(st *store.Store, path string) error {
	rows, col, err := readCSV(path)
	if err != nil {
		return err
	}

	added, skipped := 0, 0
	for _, row := range rows[1:] {
		name := strings.TrimSpace(get(row, col, "name"))
		if name == "" {
			continue
		}
		primary := normMuscleList(get(row, col, "muscles"), "|")
		equip := normEquipment(get(row, col, "equipment"))
		kind := strings.ToLower(strings.TrimSpace(get(row, col, "kind")))
		category := "strength"
		if kind == "cardio" {
			category = "cardio"
		}
		mt := strings.ToLower(get(row, col, "movement_type"))
		level := "intermediate"
		if mt == "isolation" {
			level = "beginner"
		}
		_, err := st.AddCustomExercise(name, equip, level, category, primary, nil)
		switch {
		case errors.Is(err, store.ErrExerciseExists):
			skipped++
		case err != nil:
			fmt.Fprintf(os.Stderr, "  ! %s: %v\n", name, err)
			skipped++
		default:
			added++
		}
	}
	fmt.Printf("Exercises: %d added, %d skipped (already present)\n", added, skipped)
	return nil
}

// Bodyweight loads only the body_weight column from a workouts CSV,
// recording one entry per date (idempotent — safe to re-run).
func Bodyweight(st *store.Store, path string) error {
	rows, col, err := readCSV(path)
	if err != nil {
		return err
	}
	seen := map[string]bool{}
	count := 0
	for _, row := range rows[1:] {
		date := dateOf(get(row, col, "workout_started_at"))
		if date == "" || seen[date] {
			continue
		}
		if bw := parseFloat(get(row, col, "body_weight")); bw > 0 {
			if err := st.LogBodyweight(date, bw); err == nil {
				seen[date] = true
				count++
			}
		}
	}
	fmt.Printf("Bodyweight: %d entries imported\n", count)
	return nil
}

// MigrateCardio moves cardio from a workouts CSV into cardio sessions, then
// removes the 0x0 marker sets and "Cardio:" notes an earlier import left
// behind. Idempotent.
func MigrateCardio(st *store.Store, path string) error {
	names, err := cardioNames(path)
	if err != nil {
		return fmt.Errorf("read cardio names: %w", err)
	}
	if err := importCardio(st, path); err != nil {
		return fmt.Errorf("import cardio: %w", err)
	}
	sets, notes, err := st.RemoveCardioMarkers(names)
	if err != nil {
		return fmt.Errorf("cleanup cardio markers: %w", err)
	}
	fmt.Printf("Cleanup: removed %d cardio marker sets, cleaned %d workout notes\n", sets, notes)
	return nil
}

// cardioNames returns the lowercased set of exercise names whose kind is
// "cardio" in a workouts CSV.
func cardioNames(path string) (map[string]bool, error) {
	rows, col, err := readCSV(path)
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, row := range rows[1:] {
		if strings.ToLower(strings.TrimSpace(get(row, col, "exercise_kind"))) == "cardio" {
			if n := strings.TrimSpace(get(row, col, "exercise_name")); n != "" {
				names[strings.ToLower(n)] = true
			}
		}
	}
	return names, nil
}

// importCardio loads only cardio rows from a workouts CSV into cardio sessions.
// It clears them first so it's safe to re-run.
func importCardio(st *store.Store, path string) error {
	rows, col, err := readCSV(path)
	if err != nil {
		return err
	}
	if err := st.ClearCardio(); err != nil {
		return err
	}
	count := 0
	for _, row := range rows[1:] {
		if strings.ToLower(strings.TrimSpace(get(row, col, "exercise_kind"))) != "cardio" {
			continue
		}
		date := dateOf(get(row, col, "workout_started_at"))
		name := strings.TrimSpace(get(row, col, "exercise_name"))
		if date == "" || name == "" {
			continue
		}
		dur := parseInt(get(row, col, "duration_seconds"))
		dist := parseFloat(get(row, col, "distance_miles"))
		if _, err := st.LogCardio(date, name, dur, dist); err == nil {
			count++
		}
	}
	fmt.Printf("Cardio: %d sessions imported\n", count)
	return nil
}

// Workouts loads the ChimpFitness workouts CSV. Rows are grouped into per-day
// workouts; strength rows become sets, cardio rows become cardio sessions, and
// each day's bodyweight and session notes are attached.
func Workouts(st *store.Store, path string) error {
	rows, col, err := readCSV(path)
	if err != nil {
		return err
	}

	// Accumulate note lines and the session note per date, applied after sets.
	type dayAcc struct {
		sessionNote string
		targetTags  string
		bodyweight  float64
	}
	days := map[string]*dayAcc{}
	var order []string

	sets, cardio := 0, 0
	for _, row := range rows[1:] {
		date := dateOf(get(row, col, "workout_started_at"))
		if date == "" {
			continue
		}
		acc, ok := days[date]
		if !ok {
			acc = &dayAcc{}
			days[date] = acc
			order = append(order, date)
		}
		if n := strings.TrimSpace(get(row, col, "workout_notes")); n != "" && acc.sessionNote == "" {
			acc.sessionNote = n
		}
		if t := strings.TrimSpace(get(row, col, "target_tags")); t != "" && acc.targetTags == "" {
			acc.targetTags = t
		}
		if acc.bodyweight == 0 {
			if bw := parseFloat(get(row, col, "body_weight")); bw > 0 {
				acc.bodyweight = bw
			}
		}

		name := strings.TrimSpace(get(row, col, "exercise_name"))
		if name == "" {
			continue
		}
		kind := strings.ToLower(strings.TrimSpace(get(row, col, "exercise_kind")))
		weight := parseFloat(get(row, col, "weight"))
		reps := parseInt(get(row, col, "reps"))

		if kind == "cardio" {
			// Store cardio in its own table (type + duration + distance).
			dur := parseInt(get(row, col, "duration_seconds"))
			dist := parseFloat(get(row, col, "distance_miles"))
			if _, err := st.LogCardio(date, name, dur, dist); err == nil {
				cardio++
			}
			continue
		}

		if _, err := st.LogSet(date, name, weight, reps, nil); err != nil {
			fmt.Fprintf(os.Stderr, "  ! %s %s: %v\n", date, name, err)
			continue
		}
		sets++
	}

	// Attach notes + bodyweight per day.
	sort.Strings(order)
	bwCount := 0
	for _, date := range order {
		acc := days[date]
		if acc.bodyweight > 0 {
			if err := st.LogBodyweight(date, acc.bodyweight); err == nil {
				bwCount++
			}
		}
		var b strings.Builder
		if acc.targetTags != "" {
			b.WriteString(acc.targetTags)
		}
		if acc.sessionNote != "" {
			if b.Len() > 0 {
				b.WriteString(" — ")
			}
			b.WriteString(acc.sessionNote)
		}
		if b.Len() > 0 {
			if _, err := st.SetWorkoutNotes(date, b.String()); err != nil {
				fmt.Fprintf(os.Stderr, "  ! notes %s: %v\n", date, err)
			}
		}
	}

	fmt.Printf("Workouts: %d strength sets, %d cardio entries, %d bodyweight entries across %d days\n", sets, cardio, bwCount, len(order))
	return nil
}

// --- small CSV helpers ---

// readCSV reads a whole CSV export and indexes its header row by lowercased
// column name.
func readCSV(path string) (rows [][]string, col map[string]int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer f.Close()
	rows, err = csv.NewReader(f).ReadAll()
	if err != nil {
		return nil, nil, fmt.Errorf("parse csv: %w", err)
	}
	if len(rows) < 2 {
		return nil, nil, fmt.Errorf("no data rows")
	}
	return rows, headerIndex(rows[0]), nil
}

func headerIndex(header []string) map[string]int {
	m := make(map[string]int, len(header))
	for i, h := range header {
		m[strings.TrimSpace(strings.ToLower(h))] = i
	}
	return m
}

func get(row []string, col map[string]int, name string) string {
	if i, ok := col[name]; ok && i < len(row) {
		return row[i]
	}
	return ""
}

// dateOf extracts YYYY-MM-DD from an ISO timestamp like 2026-05-04T22:22:30Z.
func dateOf(ts string) string {
	ts = strings.TrimSpace(ts)
	if len(ts) >= 10 {
		return ts[:10]
	}
	return ""
}

func parseFloat(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

func parseInt(s string) int {
	v, _ := strconv.Atoi(strings.TrimSpace(s))
	return v
}
