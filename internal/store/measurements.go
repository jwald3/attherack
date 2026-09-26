package store

// MeasurementSite is one tracked body-measurement location.
type MeasurementSite struct {
	Slug  string
	Label string
}

// MeasurementSites is the fixed set of body measurements the app tracks. The
// form, the coach tool's enum, and history rendering all read from this list,
// so adding a site here surfaces it everywhere.
var MeasurementSites = []MeasurementSite{
	{"waist", "Waist"},
	{"chest", "Chest"},
	{"hips", "Hips"},
	{"neck", "Neck"},
	{"arm_l", "Left arm"},
	{"arm_r", "Right arm"},
	{"thigh_l", "Left thigh"},
	{"thigh_r", "Right thigh"},
	{"calf_l", "Left calf"},
	{"calf_r", "Right calf"},
}

// MeasurementSiteSlugs returns just the slugs, for the coach tool's enum.
func MeasurementSiteSlugs() []string {
	out := make([]string, len(MeasurementSites))
	for i, s := range MeasurementSites {
		out[i] = s.Slug
	}
	return out
}

// MeasurementLabel returns the display label for a slug, or the slug itself if
// it isn't a known site.
func MeasurementLabel(slug string) string {
	for _, s := range MeasurementSites {
		if s.Slug == slug {
			return s.Label
		}
	}
	return slug
}

// IsMeasurementSite reports whether slug is a known measurement site.
func IsMeasurementSite(slug string) bool {
	for _, s := range MeasurementSites {
		if s.Slug == slug {
			return true
		}
	}
	return false
}

// Measurement is a single body measurement (a value for a site on a date).
type Measurement struct {
	Date  string  `json:"date"`
	Site  string  `json:"site"`
	Value float64 `json:"value"`
}

// LogMeasurement records (or replaces) the value for a site on a date.
func (s *Store) LogMeasurement(date, site string, value float64) error {
	_, err := s.db.Exec(`
INSERT INTO measurements (date, site, value) VALUES (?, ?, ?)
ON CONFLICT(date, site) DO UPDATE SET value = excluded.value`, orToday(date), site, value)
	return err
}

// MeasurementHistory returns dated values for one site, oldest first, limited
// to the most recent `limit`.
func (s *Store) MeasurementHistory(site string, limit int) ([]Measurement, error) {
	if limit <= 0 {
		limit = 60
	}
	rows, err := s.db.Query(`
SELECT date, site, value FROM measurements WHERE site = ?
ORDER BY date DESC LIMIT ?`, site, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Measurement
	for rows.Next() {
		var m Measurement
		if err := rows.Scan(&m.Date, &m.Site, &m.Value); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Return oldest-first so callers can render a history list or a chart.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// LatestMeasurements returns the most recent value per site, keyed by site slug.
func (s *Store) LatestMeasurements() (map[string]Measurement, error) {
	rows, err := s.db.Query(`
SELECT m.date, m.site, m.value FROM measurements m
JOIN (SELECT site, MAX(date) AS d FROM measurements GROUP BY site) latest
  ON m.site = latest.site AND m.date = latest.d`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]Measurement{}
	for rows.Next() {
		var m Measurement
		if err := rows.Scan(&m.Date, &m.Site, &m.Value); err != nil {
			return nil, err
		}
		out[m.Site] = m
	}
	return out, rows.Err()
}

// DeleteMeasurement removes one site's value on a date.
func (s *Store) DeleteMeasurement(date, site string) error {
	_, err := s.db.Exec(`DELETE FROM measurements WHERE date = ? AND site = ?`, date, site)
	return err
}
