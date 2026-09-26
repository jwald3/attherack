// Package store is the SQLite persistence layer: the schema, its migrations,
// and every query the app runs, grouped by domain (one file each).
package store

import (
	"database/sql"
	"errors"

	"github.com/jwald3/attherack/internal/dates"
	_ "modernc.org/sqlite"
)

// ErrNotFound is returned when a lookup by id matches no row.
var ErrNotFound = errors.New("not found")

// Store wraps the SQLite database and all queries the app needs.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the database at path and brings its schema
// up to date.
func Open(path string) (*Store, error) {
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

// Close closes the underlying database.
func (s *Store) Close() error { return s.db.Close() }

// HasData reports whether any user data (training, cardio, bodyweight,
// supplements, food or chats) has been recorded.
func (s *Store) HasData() (bool, error) {
	var n int
	err := s.db.QueryRow(`
SELECT (SELECT COUNT(1) FROM workouts) + (SELECT COUNT(1) FROM cardio_sessions) +
       (SELECT COUNT(1) FROM bodyweight) + (SELECT COUNT(1) FROM supplement_logs) +
       (SELECT COUNT(1) FROM food_logs) + (SELECT COUNT(1) FROM chat_threads)`).Scan(&n)
	return n > 0, err
}

// orToday returns date, or today's date when it's blank.
func orToday(date string) string {
	if date == "" {
		return dates.Today()
	}
	return date
}
