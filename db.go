package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

var errExerciseExists = errors.New("an exercise with that name already exists")

// Store wraps the SQLite database and all queries the app needs.
type Store struct {
	db *sql.DB
}

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
}

func openStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// modernc SQLite is happiest with a single writer; keep the pool tight.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON;`); err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS workouts (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    date  TEXT NOT NULL,
    notes TEXT NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS sets (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    workout_id INTEGER NOT NULL REFERENCES workouts(id) ON DELETE CASCADE,
    exercise   TEXT NOT NULL,
    weight     REAL NOT NULL DEFAULT 0,
    reps       INTEGER NOT NULL DEFAULT 0,
    rpe        REAL
);
CREATE INDEX IF NOT EXISTS idx_sets_workout ON sets(workout_id);
CREATE INDEX IF NOT EXISTS idx_sets_exercise ON sets(exercise);
CREATE TABLE IF NOT EXISTS chat_messages (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    role       TEXT NOT NULL,
    content    TEXT NOT NULL,
    status     TEXT NOT NULL DEFAULT 'done', -- 'pending' | 'done' | 'error'
    mutated    INTEGER NOT NULL DEFAULT 0,    -- assistant reply touched the log
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS chat_threads (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    title      TEXT NOT NULL DEFAULT 'New chat',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS bodyweight (
    date   TEXT PRIMARY KEY,
    weight REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS cardio_sessions (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    date             TEXT NOT NULL,
    type             TEXT NOT NULL,
    duration_seconds INTEGER NOT NULL DEFAULT 0,
    distance_miles   REAL NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_cardio_date ON cardio_sessions(date);
CREATE TABLE IF NOT EXISTS supplement_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    date       TEXT NOT NULL,
    name       TEXT NOT NULL,
    amount     REAL NOT NULL DEFAULT 0,
    unit       TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_supp_date ON supplement_logs(date);
CREATE TABLE IF NOT EXISTS food_logs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    date       TEXT NOT NULL,
    meal       TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL,
    notes      TEXT NOT NULL DEFAULT '',
    calories   REAL,
    protein    REAL,
    carbs      REAL,
    fat        REAL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_food_date ON food_logs(date);
CREATE TABLE IF NOT EXISTS custom_exercises (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL,
    equipment         TEXT,
    level             TEXT NOT NULL DEFAULT 'intermediate',
    category          TEXT NOT NULL DEFAULT 'strength',
    primary_muscles   TEXT NOT NULL DEFAULT '',
    secondary_muscles TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
`)
	if err != nil {
		return err
	}
	if err := s.migrateChatThreads(); err != nil {
		return err
	}
	return s.migrateChatStatus()
}

// migrateChatStatus adds the status/mutated columns to chat_messages for
// databases created before background replies existed. Any pre-existing row
// is already a finished message, so it defaults to 'done'.
func (s *Store) migrateChatStatus() error {
	for col, def := range map[string]string{
		"status":  "TEXT NOT NULL DEFAULT 'done'",
		"mutated": "INTEGER NOT NULL DEFAULT 0",
	} {
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('chat_messages') WHERE name = ?`, col).Scan(&n); err != nil {
			return err
		}
		if n == 0 {
			if _, err := s.db.Exec(`ALTER TABLE chat_messages ADD COLUMN ` + col + ` ` + def); err != nil {
				return err
			}
		}
	}
	// A reply left 'pending' by a crash mid-generation can never complete;
	// mark such orphans as errored so the UI stops waiting on them.
	_, err := s.db.Exec(`UPDATE chat_messages SET status = 'error', content = 'This reply was interrupted. Please ask again.' WHERE status = 'pending'`)
	return err
}

// --- Settings (key/value) ---

func (s *Store) getSetting(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Store) setSetting(key, value string) error {
	_, err := s.db.Exec(`
INSERT INTO settings (key, value) VALUES (?, ?)
ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) deleteSetting(key string) error {
	_, err := s.db.Exec(`DELETE FROM settings WHERE key = ?`, key)
	return err
}

// --- Bodyweight ---

// BodyweightEntry is a single dated bodyweight measurement.
type BodyweightEntry struct {
	Date   string  `json:"date"`
	Weight float64 `json:"weight"`
}

// logBodyweight records (or replaces) the bodyweight for a date.
func (s *Store) logBodyweight(date string, weight float64) error {
	if date == "" {
		date = today()
	}
	_, err := s.db.Exec(`
INSERT INTO bodyweight (date, weight) VALUES (?, ?)
ON CONFLICT(date) DO UPDATE SET weight = excluded.weight`, date, weight)
	return err
}

// listBodyweight returns recent entries, most recent first.
func (s *Store) listBodyweight(limit int) ([]BodyweightEntry, error) {
	if limit <= 0 {
		limit = 30
	}
	rows, err := s.db.Query(`SELECT date, weight FROM bodyweight ORDER BY date DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BodyweightEntry
	for rows.Next() {
		var e BodyweightEntry
		if err := rows.Scan(&e.Date, &e.Weight); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// latestBodyweight returns the most recent entry, or ok=false if none.
func (s *Store) latestBodyweight() (BodyweightEntry, bool) {
	var e BodyweightEntry
	err := s.db.QueryRow(`SELECT date, weight FROM bodyweight ORDER BY date DESC LIMIT 1`).Scan(&e.Date, &e.Weight)
	if err != nil {
		return BodyweightEntry{}, false
	}
	return e, true
}

// deleteBodyweight removes the entry for a date.
func (s *Store) deleteBodyweight(date string) error {
	_, err := s.db.Exec(`DELETE FROM bodyweight WHERE date = ?`, date)
	return err
}

// --- Cardio ---

// CardioSession is a single logged cardio activity.
type CardioSession struct {
	ID              int64   `json:"id"`
	Date            string  `json:"date"`
	Type            string  `json:"type"`
	DurationSeconds int     `json:"duration_seconds"`
	DistanceMiles   float64 `json:"distance_miles"`
}

func (s *Store) logCardio(date, ctype string, durationSeconds int, distanceMiles float64) (CardioSession, error) {
	if date == "" {
		date = today()
	}
	res, err := s.db.Exec(
		`INSERT INTO cardio_sessions (date, type, duration_seconds, distance_miles) VALUES (?, ?, ?, ?)`,
		date, ctype, durationSeconds, distanceMiles)
	if err != nil {
		return CardioSession{}, err
	}
	id, _ := res.LastInsertId()
	return CardioSession{ID: id, Date: date, Type: ctype, DurationSeconds: durationSeconds, DistanceMiles: distanceMiles}, nil
}

func (s *Store) listCardio(limit int) ([]CardioSession, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
SELECT id, date, type, duration_seconds, distance_miles
FROM cardio_sessions ORDER BY date DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CardioSession
	for rows.Next() {
		var c CardioSession
		if err := rows.Scan(&c.ID, &c.Date, &c.Type, &c.DurationSeconds, &c.DistanceMiles); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// cardioHistoryByType returns every session of one cardio type (case-insensitive),
// most recent first.
func (s *Store) cardioHistoryByType(ctype string) ([]CardioSession, error) {
	rows, err := s.db.Query(`
SELECT id, date, type, duration_seconds, distance_miles
FROM cardio_sessions WHERE type = ? COLLATE NOCASE
ORDER BY date DESC, id DESC`, ctype)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CardioSession
	for rows.Next() {
		var c CardioSession
		if err := rows.Scan(&c.ID, &c.Date, &c.Type, &c.DurationSeconds, &c.DistanceMiles); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) deleteCardio(id int64) error {
	_, err := s.db.Exec(`DELETE FROM cardio_sessions WHERE id = ?`, id)
	return err
}

// cardioTotalsSince returns total distance (miles) and duration (seconds) for
// cardio on/after the given date (YYYY-MM-DD).
func (s *Store) cardioTotalsSince(date string) (miles float64, seconds int, count int) {
	row := s.db.QueryRow(`
SELECT COALESCE(SUM(distance_miles),0), COALESCE(SUM(duration_seconds),0), COUNT(1)
FROM cardio_sessions WHERE date >= ?`, date)
	_ = row.Scan(&miles, &seconds, &count)
	return
}

// --- Supplements ---

// SupplementLog is one dose of a supplement taken on a date.
type SupplementLog struct {
	ID     int64   `json:"id"`
	Date   string  `json:"date"`
	Name   string  `json:"name"`
	Amount float64 `json:"amount"`
	Unit   string  `json:"unit"`
}

// SupplementSummary describes one supplement the user takes: its most recent
// dose (used for one-click logging) and how consistently it's been taken.
type SupplementSummary struct {
	Name       string
	LastAmount float64
	LastUnit   string
	LastDate   string
	DaysTaken  int // distinct days taken within the summary window
	TakenToday bool
}

func (s *Store) logSupplement(date, name string, amount float64, unit string) (SupplementLog, error) {
	if date == "" {
		date = today()
	}
	res, err := s.db.Exec(`INSERT INTO supplement_logs (date, name, amount, unit) VALUES (?, ?, ?, ?)`, date, name, amount, unit)
	if err != nil {
		return SupplementLog{}, err
	}
	id, _ := res.LastInsertId()
	return SupplementLog{ID: id, Date: date, Name: name, Amount: amount, Unit: unit}, nil
}

// listSupplementLogs returns recent doses, most recent first.
func (s *Store) listSupplementLogs(limit int) ([]SupplementLog, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
SELECT id, date, name, amount, unit FROM supplement_logs
ORDER BY date DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SupplementLog
	for rows.Next() {
		var l SupplementLog
		if err := rows.Scan(&l.ID, &l.Date, &l.Name, &l.Amount, &l.Unit); err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) deleteSupplementLog(id int64) error {
	_, err := s.db.Exec(`DELETE FROM supplement_logs WHERE id = ?`, id)
	return err
}

// supplementSummaries returns every supplement ever logged (names grouped
// case-insensitively), with its latest dose and the number of distinct days
// it was taken on/after since. Most recently taken first.
func (s *Store) supplementSummaries(since string) ([]SupplementSummary, error) {
	rows, err := s.db.Query(`
SELECT l.name, l.amount, l.unit, l.date,
       (SELECT COUNT(DISTINCT date) FROM supplement_logs d WHERE d.name = l.name COLLATE NOCASE AND d.date >= ?),
       EXISTS(SELECT 1 FROM supplement_logs t WHERE t.name = l.name COLLATE NOCASE AND t.date = ?)
FROM supplement_logs l
WHERE l.id = (SELECT id FROM supplement_logs x WHERE x.name = l.name COLLATE NOCASE ORDER BY date DESC, id DESC LIMIT 1)
ORDER BY l.date DESC, l.id DESC`, since, today())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SupplementSummary
	for rows.Next() {
		var sm SupplementSummary
		if err := rows.Scan(&sm.Name, &sm.LastAmount, &sm.LastUnit, &sm.LastDate, &sm.DaysTaken, &sm.TakenToday); err != nil {
			return nil, err
		}
		out = append(out, sm)
	}
	return out, rows.Err()
}

// --- Diet ---

// FoodLog is one food/meal entry. Macros are optional (nil = not recorded).
type FoodLog struct {
	ID       int64    `json:"id"`
	Date     string   `json:"date"`
	Meal     string   `json:"meal,omitempty"` // breakfast | lunch | dinner | snack | ""
	Name     string   `json:"name"`
	Notes    string   `json:"notes,omitempty"`
	Calories *float64 `json:"calories,omitempty"`
	Protein  *float64 `json:"protein,omitempty"`
	Carbs    *float64 `json:"carbs,omitempty"`
	Fat      *float64 `json:"fat,omitempty"`
}

// HasMacros reports whether any macro was recorded.
func (f FoodLog) HasMacros() bool {
	return f.Calories != nil || f.Protein != nil || f.Carbs != nil || f.Fat != nil
}

// MacroSummary renders recorded macros like "750 kcal · 62p · 28f".
func (f FoodLog) MacroSummary() string {
	var parts []string
	for _, m := range []struct {
		v      *float64
		suffix string
	}{{f.Calories, " kcal"}, {f.Protein, "p"}, {f.Carbs, "c"}, {f.Fat, "f"}} {
		if m.v != nil {
			parts = append(parts, strconv.FormatFloat(*m.v, 'f', -1, 64)+m.suffix)
		}
	}
	return strings.Join(parts, " · ")
}

func (s *Store) logFood(f FoodLog) (FoodLog, error) {
	if f.Date == "" {
		f.Date = today()
	}
	res, err := s.db.Exec(`
INSERT INTO food_logs (date, meal, name, notes, calories, protein, carbs, fat)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		f.Date, f.Meal, f.Name, f.Notes, f.Calories, f.Protein, f.Carbs, f.Fat)
	if err != nil {
		return FoodLog{}, err
	}
	f.ID, _ = res.LastInsertId()
	return f, nil
}

// listFoodSince returns entries on/after a date, newest day first; within a
// day, breakfast → lunch → dinner, then snacks/unlabeled, each in logged order.
func (s *Store) listFoodSince(date string) ([]FoodLog, error) {
	rows, err := s.db.Query(`
SELECT id, date, meal, name, notes, calories, protein, carbs, fat
FROM food_logs WHERE date >= ?
ORDER BY date DESC,
         CASE meal WHEN 'breakfast' THEN 0 WHEN 'lunch' THEN 1 WHEN 'dinner' THEN 2 ELSE 3 END,
         id ASC`, date)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FoodLog
	for rows.Next() {
		var f FoodLog
		if err := rows.Scan(&f.ID, &f.Date, &f.Meal, &f.Name, &f.Notes, &f.Calories, &f.Protein, &f.Carbs, &f.Fat); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) deleteFood(id int64) error {
	_, err := s.db.Exec(`DELETE FROM food_logs WHERE id = ?`, id)
	return err
}

// recentFoodNames returns distinct food names, most recently logged first,
// for autocomplete.
func (s *Store) recentFoodNames(limit int) []string {
	rows, err := s.db.Query(`
SELECT name FROM food_logs GROUP BY name COLLATE NOCASE ORDER BY MAX(id) DESC LIMIT ?`, limit)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if rows.Scan(&n) == nil {
			out = append(out, n)
		}
	}
	return out
}

// --- Custom exercises ---

// listCustomExercises returns all user-added exercises as Exercise values so they
// merge cleanly with the built-in library.
func (s *Store) listCustomExercises() ([]Exercise, error) {
	rows, err := s.db.Query(`
SELECT id, name, equipment, level, category, primary_muscles, secondary_muscles
FROM custom_exercises ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Exercise
	for rows.Next() {
		var (
			e         Exercise
			equip     sql.NullString
			primary   string
			secondary string
		)
		if err := rows.Scan(&e.ID, &e.Name, &equip, &e.Level, &e.Category, &primary, &secondary); err != nil {
			return nil, err
		}
		if equip.Valid && equip.String != "" {
			eq := equip.String
			e.Equipment = &eq
		}
		e.PrimaryMuscles = splitList(primary)
		e.SecondaryMuscles = splitList(secondary)
		e.Custom = true
		out = append(out, e)
	}
	return out, rows.Err()
}

// addCustomExercise inserts a user-defined exercise and returns the stored value.
// Returns errExerciseExists if the generated id already exists.
func (s *Store) addCustomExercise(name, equipment, level, category string, primary, secondary []string) (Exercise, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Exercise{}, fmt.Errorf("exercise name is required")
	}
	if level == "" {
		level = "intermediate"
	}
	if category == "" {
		category = "strength"
	}
	id := "custom_" + slugify(name)

	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM custom_exercises WHERE id = ?`, id).Scan(&exists); err != nil {
		return Exercise{}, err
	}
	if exists > 0 {
		return Exercise{}, errExerciseExists
	}

	var equipPtr any
	if strings.TrimSpace(equipment) != "" {
		equipPtr = strings.TrimSpace(equipment)
	}
	_, err := s.db.Exec(`
INSERT INTO custom_exercises (id, name, equipment, level, category, primary_muscles, secondary_muscles)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, equipPtr, level, category, joinList(primary), joinList(secondary))
	if err != nil {
		return Exercise{}, err
	}
	e := Exercise{
		Name: name, Level: level, Category: category,
		PrimaryMuscles: cleanList(primary), SecondaryMuscles: cleanList(secondary),
		ID: id, Custom: true,
	}
	if s, ok := equipPtr.(string); ok {
		e.Equipment = &s
	}
	return e, nil
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

func (s *Store) setWorkoutNotes(date, notes string) (int64, error) {
	id, err := s.getOrCreateWorkout(date)
	if err != nil {
		return 0, err
	}
	_, err = s.db.Exec(`UPDATE workouts SET notes = ? WHERE id = ?`, notes, id)
	return id, err
}

// listWorkouts returns workouts (most recent first) with their sets attached.
func (s *Store) listWorkouts(limit int) ([]Workout, error) {
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
SELECT s.id, s.workout_id, s.exercise, s.weight, s.reps, s.rpe
FROM sets s JOIN workouts w ON w.id = s.workout_id
WHERE w.date IN (SELECT date FROM workouts ORDER BY date DESC, id DESC LIMIT ?)
ORDER BY s.id ASC`, limit)
	if err != nil {
		return nil, err
	}
	defer setRows.Close()
	for setRows.Next() {
		var st Set
		if err := setRows.Scan(&st.ID, &st.WorkoutID, &st.Exercise, &st.Weight, &st.Reps, &st.RPE); err != nil {
			return nil, err
		}
		if idx, ok := byID[st.WorkoutID]; ok {
			workouts[idx].Sets = append(workouts[idx].Sets, st)
		}
	}
	return workouts, setRows.Err()
}

// --- Sets ---

func (s *Store) logSet(date, exercise string, weight float64, reps int, rpe *float64) (Set, error) {
	wID, err := s.getOrCreateWorkout(date)
	if err != nil {
		return Set{}, err
	}
	res, err := s.db.Exec(
		`INSERT INTO sets (workout_id, exercise, weight, reps, rpe) VALUES (?, ?, ?, ?, ?)`,
		wID, exercise, weight, reps, rpe)
	if err != nil {
		return Set{}, err
	}
	id, _ := res.LastInsertId()
	return Set{ID: id, WorkoutID: wID, Exercise: exercise, Weight: weight, Reps: reps, RPE: rpe}, nil
}

func (s *Store) deleteSet(id int64) error {
	_, err := s.db.Exec(`DELETE FROM sets WHERE id = ?`, id)
	return err
}

// getSet returns a single set with its workout date, or sql.ErrNoRows.
func (s *Store) getSet(id int64) (DatedSet, error) {
	var d DatedSet
	err := s.db.QueryRow(`
SELECT w.date, s.id, s.workout_id, s.exercise, s.weight, s.reps, s.rpe
FROM sets s JOIN workouts w ON w.id = s.workout_id
WHERE s.id = ?`, id).
		Scan(&d.Date, &d.Set.ID, &d.Set.WorkoutID, &d.Set.Exercise, &d.Set.Weight, &d.Set.Reps, &d.Set.RPE)
	return d, err
}

// updateSet overwrites the weight, reps and rpe of an existing set. It returns
// the updated set (with its workout date attached) or sql.ErrNoRows if no set
// has that id.
func (s *Store) updateSet(id int64, weight float64, reps int, rpe *float64) (DatedSet, error) {
	res, err := s.db.Exec(
		`UPDATE sets SET weight = ?, reps = ?, rpe = ? WHERE id = ?`,
		weight, reps, rpe, id)
	if err != nil {
		return DatedSet{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return DatedSet{}, sql.ErrNoRows
	}
	var d DatedSet
	err = s.db.QueryRow(`
SELECT w.date, s.id, s.workout_id, s.exercise, s.weight, s.reps, s.rpe
FROM sets s JOIN workouts w ON w.id = s.workout_id
WHERE s.id = ?`, id).
		Scan(&d.Date, &d.Set.ID, &d.Set.WorkoutID, &d.Set.Exercise, &d.Set.Weight, &d.Set.Reps, &d.Set.RPE)
	return d, err
}

// DatedSet is a logged set with its workout date attached.
type DatedSet struct {
	Date string
	Set  Set
}

// exerciseHistory returns recent sets for one exercise (most recent first).
func (s *Store) exerciseHistory(exercise string, limit int) ([]DatedSet, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
SELECT w.date, s.id, s.workout_id, s.exercise, s.weight, s.reps, s.rpe
FROM sets s JOIN workouts w ON w.id = s.workout_id
WHERE s.exercise = ? COLLATE NOCASE
ORDER BY w.date DESC, s.id DESC LIMIT ?`, exercise, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DatedSet
	for rows.Next() {
		var d DatedSet
		if err := rows.Scan(&d.Date, &d.Set.ID, &d.Set.WorkoutID, &d.Set.Exercise, &d.Set.Weight, &d.Set.Reps, &d.Set.RPE); err != nil {
			return nil, err
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// exerciseDailyTop returns, per date, the heaviest set for an exercise (oldest
// first) — the series used for a progression chart. Also returns the estimated
// 1RM (Epley) of that top set for an optional strength view.
func (s *Store) exerciseDailyTop(exercise string) ([]DatedSet, error) {
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

// --- Chat threads & messages ---

// ChatThread is one conversation with the coach.
type ChatThread struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

type ChatMessage struct {
	ID        int64  `json:"id"`
	ThreadID  int64  `json:"thread_id"`
	Role      string `json:"role"` // "user" or "assistant"
	Content   string `json:"content"`
	Status    string `json:"status"` // "pending" | "done" | "error"
	Mutated   bool   `json:"mutated"`
	CreatedAt string `json:"created_at"`
}

// Pending reports whether an assistant reply is still being generated.
func (m ChatMessage) Pending() bool { return m.Status == "pending" }

// migrateChatThreads adds thread support to databases created before threads
// existed, moving any old single-stream chat into one "Earlier chat" thread.
func (s *Store) migrateChatThreads() error {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM pragma_table_info('chat_messages') WHERE name = 'thread_id'`).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		if _, err := s.db.Exec(`ALTER TABLE chat_messages ADD COLUMN thread_id INTEGER REFERENCES chat_threads(id) ON DELETE CASCADE`); err != nil {
			return err
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_chat_thread ON chat_messages(thread_id)`); err != nil {
		return err
	}
	var orphans int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM chat_messages WHERE thread_id IS NULL`).Scan(&orphans); err != nil || orphans == 0 {
		return err
	}
	id, err := s.createThread("Earlier chat")
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE chat_messages SET thread_id = ? WHERE thread_id IS NULL`, id)
	if err == nil {
		_, err = s.db.Exec(`UPDATE chat_threads SET updated_at = COALESCE((SELECT MAX(created_at) FROM chat_messages WHERE thread_id = ?), updated_at) WHERE id = ?`, id, id)
	}
	return err
}

func (s *Store) createThread(title string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO chat_threads (title) VALUES (?)`, title)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) getThread(id int64) (ChatThread, bool) {
	var t ChatThread
	err := s.db.QueryRow(`SELECT id, title, updated_at FROM chat_threads WHERE id = ?`, id).Scan(&t.ID, &t.Title, &t.UpdatedAt)
	return t, err == nil
}

// listThreads returns conversations, most recently active first.
func (s *Store) listThreads(limit int) ([]ChatThread, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, title, updated_at FROM chat_threads ORDER BY updated_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatThread
	for rows.Next() {
		var t ChatThread
		if err := rows.Scan(&t.ID, &t.Title, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) renameThread(id int64, title string) error {
	_, err := s.db.Exec(`UPDATE chat_threads SET title = ? WHERE id = ?`, title, id)
	return err
}

func (s *Store) deleteThread(id int64) error {
	// Delete messages explicitly: the FK cascade only applies to columns that
	// were declared with it at table creation, not ones added via ALTER.
	if _, err := s.db.Exec(`DELETE FROM chat_messages WHERE thread_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM chat_threads WHERE id = ?`, id)
	return err
}

func (s *Store) addChatMessage(threadID int64, role, content string) error {
	if _, err := s.db.Exec(`INSERT INTO chat_messages (thread_id, role, content) VALUES (?, ?, ?)`, threadID, role, content); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE chat_threads SET updated_at = datetime('now') WHERE id = ?`, threadID)
	return err
}

// addPendingAssistant inserts a placeholder assistant reply that a background
// goroutine will fill in later, and returns its id so the UI can poll for it.
func (s *Store) addPendingAssistant(threadID int64) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO chat_messages (thread_id, role, content, status) VALUES (?, 'assistant', '', 'pending')`,
		threadID)
	if err != nil {
		return 0, err
	}
	if _, err := s.db.Exec(`UPDATE chat_threads SET updated_at = datetime('now') WHERE id = ?`, threadID); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// finishChatMessage fills in a pending assistant reply with its final content
// and status ('done' or 'error').
func (s *Store) finishChatMessage(id int64, content, status string, mutated bool) error {
	m := 0
	if mutated {
		m = 1
	}
	_, err := s.db.Exec(
		`UPDATE chat_messages SET content = ?, status = ?, mutated = ? WHERE id = ?`,
		content, status, m, id)
	return err
}

// getChatMessage returns one message by id.
func (s *Store) getChatMessage(id int64) (ChatMessage, bool) {
	var m ChatMessage
	err := s.db.QueryRow(
		`SELECT id, thread_id, role, content, status, mutated, created_at FROM chat_messages WHERE id = ?`, id).
		Scan(&m.ID, &m.ThreadID, &m.Role, &m.Content, &m.Status, &m.Mutated, &m.CreatedAt)
	return m, err == nil
}

// listChatMessages returns the most recent messages in a thread, oldest first.
func (s *Store) listChatMessages(threadID int64, limit int) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
SELECT id, thread_id, role, content, status, mutated, created_at FROM chat_messages
WHERE thread_id = ? ORDER BY id DESC LIMIT ?`, threadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.Role, &m.Content, &m.Status, &m.Mutated, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse into chronological order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	return msgs, nil
}

func today() string { return time.Now().Format("2006-01-02") }

// daysAgo returns the date N days before today as YYYY-MM-DD.
func daysAgo(n int) string {
	return time.Now().AddDate(0, 0, -n).Format("2006-01-02")
}

// slugify turns an exercise name into a stable id fragment (letters/digits/_).
func slugify(name string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevUnderscore = false
		default:
			if !prevUnderscore && b.Len() > 0 {
				b.WriteByte('_')
				prevUnderscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

// splitList / joinList (de)serialize muscle lists stored as comma-separated text.
func splitList(s string) []string {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	return cleanList(parts)
}

func joinList(items []string) string {
	return strings.Join(cleanList(items), ",")
}

// cleanList trims each item and drops empties.
func cleanList(items []string) []string {
	var out []string
	for _, it := range items {
		it = strings.TrimSpace(it)
		if it != "" {
			out = append(out, it)
		}
	}
	return out
}

// summaryContext builds a compact text snapshot of recent training for Claude's
// system prompt, so it always has grounding even before calling any tool.
func (s *Store) summaryContext() string {
	out := ""
	if bw, ok := s.latestBodyweight(); ok {
		out += fmt.Sprintf("Most recent weigh-in: %g (on %s). Full history via get_bodyweight_history.\n\n", bw.Weight, bw.Date)
	}
	if mi, sec, n := s.cardioTotalsSince(daysAgo(7)); n > 0 {
		out += fmt.Sprintf("Cardio last 7 days: %d sessions, %.1f mi, %d min. Full history via get_cardio_history.\n\n", n, mi, sec/60)
	}
	if logs, err := s.listSupplementLogs(50); err == nil {
		var taken []string
		for _, l := range logs {
			if l.Date == today() {
				taken = append(taken, fmt.Sprintf("%s %g%s", l.Name, l.Amount, l.Unit))
			}
		}
		if len(taken) > 0 {
			out += "Supplements taken today: " + strings.Join(taken, ", ") + ". Full history via get_supplement_history.\n\n"
		}
	}
	if foods, err := s.listFoodSince(today()); err == nil && len(foods) > 0 {
		var eaten []string
		for _, f := range foods {
			eaten = append(eaten, f.Name)
		}
		out += "Food logged today: " + strings.Join(eaten, ", ") + ". Details and past days via get_food_history.\n\n"
	}
	workouts, err := s.listWorkouts(8)
	if err != nil || len(workouts) == 0 {
		if out != "" {
			return out + "No workouts logged yet."
		}
		return "No workouts logged yet."
	}
	for _, w := range workouts {
		out += fmt.Sprintf("%s", w.Date)
		if w.Notes != "" {
			out += fmt.Sprintf(" (%s)", w.Notes)
		}
		out += ":\n"
		for _, st := range w.Sets {
			rpe := ""
			if st.RPE != nil {
				rpe = fmt.Sprintf(" @RPE%.1f", *st.RPE)
			}
			out += fmt.Sprintf("  - %s: %gx%d%s\n", st.Exercise, st.Weight, st.Reps, rpe)
		}
	}
	return out
}
