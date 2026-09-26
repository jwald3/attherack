package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jwald3/attherack/internal/exercise"
)

// ErrExerciseExists is returned when adding a custom exercise whose name is
// already taken.
var ErrExerciseExists = errors.New("an exercise with that name already exists")

// ListCustomExercises returns all user-added exercises as exercise.Exercise
// values so they merge cleanly with the built-in library.
func (s *Store) ListCustomExercises() ([]exercise.Exercise, error) {
	rows, err := s.db.Query(`
SELECT id, name, equipment, level, category, primary_muscles, secondary_muscles
FROM custom_exercises ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []exercise.Exercise
	for rows.Next() {
		var (
			e         exercise.Exercise
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
		e.PrimaryMuscles = exercise.SplitList(primary)
		e.SecondaryMuscles = exercise.SplitList(secondary)
		e.Custom = true
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddCustomExercise inserts a user-defined exercise and returns the stored
// value. Returns ErrExerciseExists if the generated id already exists.
func (s *Store) AddCustomExercise(name, equipment, level, category string, primary, secondary []string) (exercise.Exercise, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return exercise.Exercise{}, fmt.Errorf("exercise name is required")
	}
	if level == "" {
		level = "intermediate"
	}
	if category == "" {
		category = "strength"
	}
	id := exercise.CustomID(name)

	var exists int
	if err := s.db.QueryRow(`SELECT COUNT(1) FROM custom_exercises WHERE id = ?`, id).Scan(&exists); err != nil {
		return exercise.Exercise{}, err
	}
	if exists > 0 {
		return exercise.Exercise{}, ErrExerciseExists
	}

	var equipPtr any
	if strings.TrimSpace(equipment) != "" {
		equipPtr = strings.TrimSpace(equipment)
	}
	_, err := s.db.Exec(`
INSERT INTO custom_exercises (id, name, equipment, level, category, primary_muscles, secondary_muscles)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id, name, equipPtr, level, category, exercise.JoinList(primary), exercise.JoinList(secondary))
	if err != nil {
		return exercise.Exercise{}, err
	}
	e := exercise.Exercise{
		Name: name, Level: level, Category: category,
		PrimaryMuscles: exercise.CleanList(primary), SecondaryMuscles: exercise.CleanList(secondary),
		ID: id, Custom: true,
	}
	if s, ok := equipPtr.(string); ok {
		e.Equipment = &s
	}
	return e, nil
}
