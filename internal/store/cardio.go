package store

import (
	"strconv"
	"strings"
)

// ParseDuration turns a human duration into whole seconds. It accepts clock
// forms ("27:08" = 27m08s, "1:05:30" = 1h05m30s) and a plain or decimal number
// of minutes ("27" = 27m, "27.5" = 27m30s). Returns 0 for empty/unparseable
// input, so a blank field logs no duration. Minutes and seconds beyond a clock's
// natural range still add up (e.g. "0:90" = 90 seconds).
func ParseDuration(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	if strings.Contains(s, ":") {
		parts := strings.Split(s, ":")
		total := 0.0
		for _, p := range parts {
			v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
			if err != nil {
				return 0
			}
			total = total*60 + v
		}
		if total < 0 {
			return 0
		}
		return int(total + 0.5)
	}
	// A bare number is minutes (decimals allowed, so "27.5" is 27m30s).
	mins, err := strconv.ParseFloat(s, 64)
	if err != nil || mins < 0 {
		return 0
	}
	return int(mins*60 + 0.5)
}

// CardioSession is a single logged cardio activity.
type CardioSession struct {
	ID              int64   `json:"id"`
	Date            string  `json:"date"`
	Type            string  `json:"type"`
	DurationSeconds int     `json:"duration_seconds"`
	DistanceMiles   float64 `json:"distance_miles"`
}

func (s *Store) LogCardio(date, ctype string, durationSeconds int, distanceMiles float64) (CardioSession, error) {
	date = orToday(date)
	res, err := s.db.Exec(
		`INSERT INTO cardio_sessions (date, type, duration_seconds, distance_miles) VALUES (?, ?, ?, ?)`,
		date, ctype, durationSeconds, distanceMiles)
	if err != nil {
		return CardioSession{}, err
	}
	id, _ := res.LastInsertId()
	return CardioSession{ID: id, Date: date, Type: ctype, DurationSeconds: durationSeconds, DistanceMiles: distanceMiles}, nil
}

// ListCardio returns recent sessions, most recent first.
func (s *Store) ListCardio(limit int) ([]CardioSession, error) {
	if limit <= 0 {
		limit = 100
	}
	return s.queryCardio(`
SELECT id, date, type, duration_seconds, distance_miles
FROM cardio_sessions ORDER BY date DESC, id DESC LIMIT ?`, limit)
}

// CardioHistoryByType returns every session of one cardio type (case-insensitive),
// most recent first.
func (s *Store) CardioHistoryByType(ctype string) ([]CardioSession, error) {
	return s.queryCardio(`
SELECT id, date, type, duration_seconds, distance_miles
FROM cardio_sessions WHERE type = ? COLLATE NOCASE
ORDER BY date DESC, id DESC`, ctype)
}

func (s *Store) queryCardio(query string, args ...any) ([]CardioSession, error) {
	rows, err := s.db.Query(query, args...)
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

func (s *Store) DeleteCardio(id int64) error {
	_, err := s.db.Exec(`DELETE FROM cardio_sessions WHERE id = ?`, id)
	return err
}

// CardioTotalsSince returns total distance (miles) and duration (seconds) for
// cardio on/after the given date (YYYY-MM-DD).
func (s *Store) CardioTotalsSince(date string) (miles float64, seconds int, count int) {
	row := s.db.QueryRow(`
SELECT COALESCE(SUM(distance_miles),0), COALESCE(SUM(duration_seconds),0), COUNT(1)
FROM cardio_sessions WHERE date >= ?`, date)
	_ = row.Scan(&miles, &seconds, &count)
	return
}

// ClearCardio deletes every cardio session. Used by the CSV importer so a
// re-import doesn't duplicate sessions.
func (s *Store) ClearCardio() error {
	_, err := s.db.Exec(`DELETE FROM cardio_sessions`)
	return err
}

// RemoveCardioMarkers undoes how an earlier importer recorded cardio: it
// deletes the 0x0 marker sets whose exercise is in cardioNames (lowercased)
// and strips "Cardio: ..." segments from workout notes. Idempotent. It returns
// how many sets were removed and how many notes were cleaned.
func (s *Store) RemoveCardioMarkers(cardioNames map[string]bool) (sets, notes int, err error) {
	// Delete sets whose exercise is a known cardio type and which have no load.
	rows, err := s.db.Query(`SELECT id, exercise FROM sets WHERE weight = 0 AND reps = 0`)
	if err != nil {
		return 0, 0, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		var ex string
		if err := rows.Scan(&id, &ex); err != nil {
			rows.Close()
			return 0, 0, err
		}
		if cardioNames[strings.ToLower(ex)] {
			ids = append(ids, id)
		}
	}
	rows.Close()
	for _, id := range ids {
		_, _ = s.db.Exec(`DELETE FROM sets WHERE id = ?`, id)
	}

	// Strip "Cardio: ..." segments (everything from a "Cardio:" line onward,
	// preceded by a blank line as the importer wrote it) from notes.
	nrows, err := s.db.Query(`SELECT id, notes FROM workouts WHERE notes LIKE '%Cardio:%'`)
	if err != nil {
		return 0, 0, err
	}
	type upd struct {
		id    int64
		notes string
	}
	var updates []upd
	for nrows.Next() {
		var id int64
		var n string
		if err := nrows.Scan(&id, &n); err != nil {
			nrows.Close()
			return 0, 0, err
		}
		if idx := strings.Index(n, "Cardio:"); idx >= 0 {
			cleaned := strings.TrimRight(n[:idx], "\n ")
			updates = append(updates, upd{id, cleaned})
		}
	}
	nrows.Close()
	for _, u := range updates {
		_, _ = s.db.Exec(`UPDATE workouts SET notes = ? WHERE id = ?`, u.notes, u.id)
	}
	return len(ids), len(updates), nil
}
