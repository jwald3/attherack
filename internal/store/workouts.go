package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Workout is a dated training session that groups a set of logged sets.
type Workout struct {
	ID    int64  `json:"id"`
	Date  string `json:"date"` // YYYY-MM-DD
	Notes string `json:"notes"`
	Sets  []Set  `json:"sets,omitempty"`
}

// Set is a single logged working set within a workout.
type Set struct {
	ID        int64    `json:"id"`
	WorkoutID int64    `json:"workout_id"`
	Exercise  string   `json:"exercise"`
	Weight    float64  `json:"weight"`
	Reps      int      `json:"reps"`
	RPE       *float64 `json:"rpe,omitempty"`
	Note      string   `json:"note,omitempty"`
}

// ExerciseGroup is all of a workout's sets for one exercise, in the order they
// were logged. It's how the Training log is displayed: one heading per exercise
// with its sets beneath, instead of a flat list of loose sets.
type ExerciseGroup struct {
	Exercise string
	Sets     []Set
}

// ExerciseGroups groups a workout's sets by exercise, merging every set of the
// same exercise under one group regardless of the order logged. Groups appear
// in the order each exercise was first performed; sets within a group keep their
// logged order.
func (w Workout) ExerciseGroups() []ExerciseGroup {
	var groups []ExerciseGroup
	idx := map[string]int{}
	for _, st := range w.Sets {
		key := strings.ToLower(strings.TrimSpace(st.Exercise))
		if i, ok := idx[key]; ok {
			groups[i].Sets = append(groups[i].Sets, st)
			continue
		}
		idx[key] = len(groups)
		groups = append(groups, ExerciseGroup{Exercise: st.Exercise, Sets: []Set{st}})
	}
	return groups
}

// DatedSet is a logged set with its workout date attached.
type DatedSet struct {
	Date string
	Set  Set
}

// --- Workouts ---

// getOrCreateWorkout returns the workout for a given date, creating it if absent.
func (s *Store) getOrCreateWorkout(date string) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM workouts WHERE date = ?`, date).Scan(&id)
	if err == sql.ErrNoRows {
		res, err := s.db.Exec(`INSERT INTO workouts (date) VALUES (?)`, date)
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	return id, err
}

// SetWorkoutNotes attaches notes to the workout on date, creating it if needed.
func (s *Store) SetWorkoutNotes(date, notes string) (int64, error) {
	id, err := s.getOrCreateWorkout(date)
	if err != nil {
		return 0, err
	}
	_, err = s.db.Exec(`UPDATE workouts SET notes = ? WHERE id = ?`, notes, id)
	return id, err
}

// ListWorkouts returns workouts (most recent first) with their sets attached.
func (s *Store) ListWorkouts(limit int) ([]Workout, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.db.Query(`SELECT id, date, notes FROM workouts ORDER BY date DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workouts []Workout
	byID := map[int64]int{}
	for rows.Next() {
		var w Workout
		if err := rows.Scan(&w.ID, &w.Date, &w.Notes); err != nil {
			return nil, err
		}
		byID[w.ID] = len(workouts)
		workouts = append(workouts, w)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(workouts) == 0 {
		return workouts, nil
	}

	// Attach sets in a second pass.
	setRows, err := s.db.Query(`
SELECT s.id, s.workout_id, s.exercise, s.weight, s.reps, s.rpe, s.note
FROM sets s JOIN workouts w ON w.id = s.workout_id
WHERE w.date IN (SELECT date FROM workouts ORDER BY date DESC, id DESC LIMIT ?)
ORDER BY s.id ASC`, limit)
	if err != nil {
		return nil, err
	}
	defer setRows.Close()
	for setRows.Next() {
		var st Set
		if err := setRows.Scan(&st.ID, &st.WorkoutID, &st.Exercise, &st.Weight, &st.Reps, &st.RPE, &st.Note); err != nil {
			return nil, err
		}
		if idx, ok := byID[st.WorkoutID]; ok {
			workouts[idx].Sets = append(workouts[idx].Sets, st)
		}
	}
	return workouts, setRows.Err()
}

// --- Sets ---

// datedSetCols selects a set joined to its workout's date, in the order
// scanDatedSet reads them.
const datedSetCols = `SELECT w.date, s.id, s.workout_id, s.exercise, s.weight, s.reps, s.rpe, s.note
FROM sets s JOIN workouts w ON w.id = s.workout_id`

type scanner interface{ Scan(dest ...any) error }

func scanDatedSet(r scanner) (DatedSet, error) {
	var d DatedSet
	err := r.Scan(&d.Date, &d.Set.ID, &d.Set.WorkoutID, &d.Set.Exercise, &d.Set.Weight, &d.Set.Reps, &d.Set.RPE, &d.Set.Note)
	return d, err
}

// LogSet records a set, folding it into the workout for date. note may be empty.
func (s *Store) LogSet(date, exercise string, weight float64, reps int, rpe *float64, note string) (Set, error) {
	wID, err := s.getOrCreateWorkout(date)
	if err != nil {
		return Set{}, err
	}
	res, err := s.db.Exec(
		`INSERT INTO sets (workout_id, exercise, weight, reps, rpe, note) VALUES (?, ?, ?, ?, ?, ?)`,
		wID, exercise, weight, reps, rpe, note)
	if err != nil {
		return Set{}, err
	}
	id, _ := res.LastInsertId()
	return Set{ID: id, WorkoutID: wID, Exercise: exercise, Weight: weight, Reps: reps, RPE: rpe, Note: note}, nil
}

func (s *Store) DeleteSet(id int64) error {
	_, err := s.db.Exec(`DELETE FROM sets WHERE id = ?`, id)
	return err
}

// GetSet returns a single set with its workout date, or ErrNotFound.
func (s *Store) GetSet(id int64) (DatedSet, error) {
	d, err := scanDatedSet(s.db.QueryRow(datedSetCols+` WHERE s.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return d, ErrNotFound
	}
	return d, err
}

// UpdateSet overwrites the weight, reps, rpe and note of an existing set. It
// returns the updated set (with its workout date attached) or ErrNotFound if no
// set has that id.
func (s *Store) UpdateSet(id int64, weight float64, reps int, rpe *float64, note string) (DatedSet, error) {
	res, err := s.db.Exec(
		`UPDATE sets SET weight = ?, reps = ?, rpe = ?, note = ? WHERE id = ?`,
		weight, reps, rpe, note, id)
	if err != nil {
		return DatedSet{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return DatedSet{}, ErrNotFound
	}
	return s.GetSet(id)
}

// ExerciseHistory returns recent sets for one exercise (most recent first).
func (s *Store) ExerciseHistory(exercise string, limit int) ([]DatedSet, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(datedSetCols+`
WHERE s.exercise = ? COLLATE NOCASE
ORDER BY w.date DESC, s.id DESC LIMIT ?`, exercise, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DatedSet
	for rows.Next() {
		d, err := scanDatedSet(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// ExerciseDailyTop returns, per date, the heaviest set for an exercise (oldest
// first) — the series used for a progression chart. Only Date and Set.Weight
// are populated.
func (s *Store) ExerciseDailyTop(exercise string) ([]DatedSet, error) {
	rows, err := s.db.Query(`
SELECT w.date, MAX(s.weight) AS top
FROM sets s JOIN workouts w ON w.id = s.workout_id
WHERE s.exercise = ? COLLATE NOCASE AND s.weight > 0
GROUP BY w.date
ORDER BY w.date ASC`, exercise)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DatedSet
	for rows.Next() {
		var d DatedSet
		if err := rows.Scan(&d.Date, &d.Set.Weight); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
