package store

// BodyweightEntry is a single dated bodyweight measurement.
type BodyweightEntry struct {
	Date   string  `json:"date"`
	Weight float64 `json:"weight"`
}

// LogBodyweight records (or replaces) the bodyweight for a date.
func (s *Store) LogBodyweight(date string, weight float64) error {
	_, err := s.db.Exec(`
INSERT INTO bodyweight (date, weight) VALUES (?, ?)
ON CONFLICT(date) DO UPDATE SET weight = excluded.weight`, orToday(date), weight)
	return err
}

// ListBodyweight returns recent entries, most recent first.
func (s *Store) ListBodyweight(limit int) ([]BodyweightEntry, error) {
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

// LatestBodyweight returns the most recent entry, or ok=false if none.
func (s *Store) LatestBodyweight() (BodyweightEntry, bool) {
	var e BodyweightEntry
	err := s.db.QueryRow(`SELECT date, weight FROM bodyweight ORDER BY date DESC LIMIT 1`).Scan(&e.Date, &e.Weight)
	if err != nil {
		return BodyweightEntry{}, false
	}
	return e, true
}

// DeleteBodyweight removes the entry for a date.
func (s *Store) DeleteBodyweight(date string) error {
	_, err := s.db.Exec(`DELETE FROM bodyweight WHERE date = ?`, date)
	return err
}
