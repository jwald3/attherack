package store

// ProgressPhoto is a dated body photo. Data is only populated by
// GetProgressPhoto; listing returns metadata only so the blobs aren't loaded
// into memory at once.
type ProgressPhoto struct {
	ID        int64  `json:"id"`
	Date      string `json:"date"`
	Pose      string `json:"pose"`
	MediaType string `json:"media_type"`
	Data      []byte `json:"-"`
}

// AddProgressPhoto stores a photo and returns its id.
func (s *Store) AddProgressPhoto(date, pose, mediaType string, data []byte) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO progress_photos (date, pose, media_type, data) VALUES (?, ?, ?, ?)`,
		orToday(date), pose, mediaType, data)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListProgressPhotos returns photo metadata (no bytes), newest first.
func (s *Store) ListProgressPhotos() ([]ProgressPhoto, error) {
	rows, err := s.db.Query(`
SELECT id, date, pose, media_type FROM progress_photos
ORDER BY date DESC, id DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ProgressPhoto
	for rows.Next() {
		var p ProgressPhoto
		if err := rows.Scan(&p.ID, &p.Date, &p.Pose, &p.MediaType); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProgressPhoto loads one photo, bytes included.
func (s *Store) GetProgressPhoto(id int64) (ProgressPhoto, bool) {
	var p ProgressPhoto
	err := s.db.QueryRow(`SELECT id, date, pose, media_type, data FROM progress_photos WHERE id = ?`, id).
		Scan(&p.ID, &p.Date, &p.Pose, &p.MediaType, &p.Data)
	return p, err == nil
}

// DeleteProgressPhoto removes one photo.
func (s *Store) DeleteProgressPhoto(id int64) error {
	_, err := s.db.Exec(`DELETE FROM progress_photos WHERE id = ?`, id)
	return err
}
