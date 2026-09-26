package store

import "github.com/jwald3/attherack/internal/dates"

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

func (s *Store) LogSupplement(date, name string, amount float64, unit string) (SupplementLog, error) {
	date = orToday(date)
	res, err := s.db.Exec(`INSERT INTO supplement_logs (date, name, amount, unit) VALUES (?, ?, ?, ?)`, date, name, amount, unit)
	if err != nil {
		return SupplementLog{}, err
	}
	id, _ := res.LastInsertId()
	return SupplementLog{ID: id, Date: date, Name: name, Amount: amount, Unit: unit}, nil
}

// ListSupplementLogs returns recent doses, most recent first.
func (s *Store) ListSupplementLogs(limit int) ([]SupplementLog, error) {
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

func (s *Store) DeleteSupplementLog(id int64) error {
	_, err := s.db.Exec(`DELETE FROM supplement_logs WHERE id = ?`, id)
	return err
}

// SupplementSummaries returns every supplement ever logged (names grouped
// case-insensitively), with its latest dose and the number of distinct days
// it was taken on/after since. Most recently taken first.
func (s *Store) SupplementSummaries(since string) ([]SupplementSummary, error) {
	rows, err := s.db.Query(`
SELECT l.name, l.amount, l.unit, l.date,
       (SELECT COUNT(DISTINCT date) FROM supplement_logs d WHERE d.name = l.name COLLATE NOCASE AND d.date >= ?),
       EXISTS(SELECT 1 FROM supplement_logs t WHERE t.name = l.name COLLATE NOCASE AND t.date = ?)
FROM supplement_logs l
WHERE l.id = (SELECT id FROM supplement_logs x WHERE x.name = l.name COLLATE NOCASE ORDER BY date DESC, id DESC LIMIT 1)
ORDER BY l.date DESC, l.id DESC`, since, dates.Today())
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
