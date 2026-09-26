package store

// schema creates every table and index. It's idempotent (IF NOT EXISTS), so it
// runs on every start; changes to existing tables go in a migrate* step below.
const schema = `
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
CREATE TABLE IF NOT EXISTS chat_images (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    message_id INTEGER NOT NULL REFERENCES chat_messages(id) ON DELETE CASCADE,
    media_type TEXT NOT NULL,
    data       BLOB NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_chat_images_message ON chat_images(message_id);
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
CREATE TABLE IF NOT EXISTS programs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL,
    notes      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE TABLE IF NOT EXISTS program_exercises (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    program_id INTEGER NOT NULL REFERENCES programs(id) ON DELETE CASCADE,
    position   INTEGER NOT NULL DEFAULT 0,
    exercise   TEXT NOT NULL,
    sets       INTEGER NOT NULL DEFAULT 1,
    reps       INTEGER NOT NULL DEFAULT 0,
    weight     REAL NOT NULL DEFAULT 0,
    rpe        REAL
);
CREATE INDEX IF NOT EXISTS idx_prog_ex_program ON program_exercises(program_id);
CREATE TABLE IF NOT EXISTS measurements (
    id    INTEGER PRIMARY KEY AUTOINCREMENT,
    date  TEXT NOT NULL,
    site  TEXT NOT NULL,
    value REAL NOT NULL,
    UNIQUE(date, site)
);
CREATE INDEX IF NOT EXISTS idx_measurements_site ON measurements(site, date);
CREATE TABLE IF NOT EXISTS progress_photos (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    date       TEXT NOT NULL,
    pose       TEXT NOT NULL DEFAULT '',
    media_type TEXT NOT NULL,
    data       BLOB NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_progress_photos_date ON progress_photos(date);
`

func (s *Store) migrate() error {
	if _, err := s.db.Exec(schema); err != nil {
		return err
	}
	if err := s.migrateChatThreads(); err != nil {
		return err
	}
	return s.migrateChatStatus()
}

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
	id, err := s.CreateThread("Earlier chat")
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE chat_messages SET thread_id = ? WHERE thread_id IS NULL`, id)
	if err == nil {
		_, err = s.db.Exec(`UPDATE chat_threads SET updated_at = COALESCE((SELECT MAX(created_at) FROM chat_messages WHERE thread_id = ?), updated_at) WHERE id = ?`, id, id)
	}
	return err
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
