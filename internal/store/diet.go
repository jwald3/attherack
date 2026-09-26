package store

import (
	"strconv"
	"strings"
)

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

func (s *Store) LogFood(f FoodLog) (FoodLog, error) {
	f.Date = orToday(f.Date)
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

// ListFoodSince returns entries on/after a date, newest day first; within a
// day, breakfast → lunch → dinner, then snacks/unlabeled, each in logged order.
func (s *Store) ListFoodSince(date string) ([]FoodLog, error) {
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

func (s *Store) DeleteFood(id int64) error {
	_, err := s.db.Exec(`DELETE FROM food_logs WHERE id = ?`, id)
	return err
}

// RecentFoodNames returns distinct food names, most recently logged first,
// for autocomplete.
func (s *Store) RecentFoodNames(limit int) []string {
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
