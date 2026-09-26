package store

// Program is a named, reusable workout template: an ordered list of exercises
// with target sets/reps/weight that can be logged to a day in one action.
type Program struct {
	ID        int64             `json:"id"`
	Name      string            `json:"name"`
	Notes     string            `json:"notes"`
	CreatedAt string            `json:"created_at"`
	Exercises []ProgramExercise `json:"exercises,omitempty"`
}

// ProgramExercise is one line of a program: an exercise plus how many sets to
// log and their target reps/weight/rpe.
type ProgramExercise struct {
	ID       int64    `json:"id"`
	Position int      `json:"position"`
	Exercise string   `json:"exercise"`
	Sets     int      `json:"sets"`
	Reps     int      `json:"reps"`
	Weight   float64  `json:"weight"`
	RPE      *float64 `json:"rpe,omitempty"`
}

// CreateProgram inserts a program and its exercises atomically, returning the
// new program id. Exercise order is taken from the slice order.
func (s *Store) CreateProgram(name, notes string, exs []ProgramExercise) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO programs (name, notes) VALUES (?, ?)`, name, notes)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for i, e := range exs {
		if _, err := tx.Exec(
			`INSERT INTO program_exercises (program_id, position, exercise, sets, reps, weight, rpe) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			id, i, e.Exercise, max(e.Sets, 1), e.Reps, e.Weight, e.RPE); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

// ListPrograms returns all programs (newest first) with their exercises attached.
func (s *Store) ListPrograms() ([]Program, error) {
	rows, err := s.db.Query(`SELECT id, name, notes, created_at FROM programs ORDER BY created_at DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var programs []Program
	byID := map[int64]int{}
	for rows.Next() {
		var p Program
		if err := rows.Scan(&p.ID, &p.Name, &p.Notes, &p.CreatedAt); err != nil {
			return nil, err
		}
		byID[p.ID] = len(programs)
		programs = append(programs, p)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(programs) == 0 {
		return programs, nil
	}

	// Attach exercises in a second pass.
	exRows, err := s.db.Query(`
SELECT program_id, id, position, exercise, sets, reps, weight, rpe
FROM program_exercises ORDER BY program_id, position, id`)
	if err != nil {
		return nil, err
	}
	defer exRows.Close()
	for exRows.Next() {
		var pid int64
		var e ProgramExercise
		if err := exRows.Scan(&pid, &e.ID, &e.Position, &e.Exercise, &e.Sets, &e.Reps, &e.Weight, &e.RPE); err != nil {
			return nil, err
		}
		if idx, ok := byID[pid]; ok {
			programs[idx].Exercises = append(programs[idx].Exercises, e)
		}
	}
	return programs, exRows.Err()
}

// GetProgram returns one program with its exercises, or ok=false if not found.
func (s *Store) GetProgram(id int64) (Program, bool) {
	var p Program
	err := s.db.QueryRow(`SELECT id, name, notes, created_at FROM programs WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Notes, &p.CreatedAt)
	if err != nil {
		return Program{}, false
	}
	rows, err := s.db.Query(`
SELECT id, position, exercise, sets, reps, weight, rpe
FROM program_exercises WHERE program_id = ? ORDER BY position, id`, id)
	if err != nil {
		return Program{}, false
	}
	defer rows.Close()
	for rows.Next() {
		var e ProgramExercise
		if err := rows.Scan(&e.ID, &e.Position, &e.Exercise, &e.Sets, &e.Reps, &e.Weight, &e.RPE); err != nil {
			return Program{}, false
		}
		p.Exercises = append(p.Exercises, e)
	}
	return p, rows.Err() == nil
}

// DeleteProgram removes a program and its exercises. Children are deleted
// explicitly (the FK cascade is declared, but this matches DeleteThread and is
// robust regardless of PRAGMA foreign_keys state).
func (s *Store) DeleteProgram(id int64) error {
	if _, err := s.db.Exec(`DELETE FROM program_exercises WHERE program_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM programs WHERE id = ?`, id)
	return err
}

// StartProgram logs every set of a program to the given date (default today),
// reusing LogSet so the sets fold into that day's workout. It returns how many
// sets were logged, or ok=false if the program doesn't exist.
func (s *Store) StartProgram(id int64, date string) (logged int, ok bool, err error) {
	date = orToday(date)
	p, found := s.GetProgram(id)
	if !found {
		return 0, false, nil
	}
	for _, e := range p.Exercises {
		for i := 0; i < max(e.Sets, 1); i++ {
			if _, err := s.LogSet(date, e.Exercise, e.Weight, e.Reps, e.RPE); err != nil {
				return logged, true, err
			}
			logged++
		}
	}
	return logged, true, nil
}
