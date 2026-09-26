package store

// Chat message statuses. Assistant replies are inserted as pending and filled
// in by a background goroutine.
const (
	StatusPending = "pending"
	StatusDone    = "done"
	StatusError   = "error"
)

// ChatThread is one conversation with the coach.
type ChatThread struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
}

type ChatMessage struct {
	ID        int64  `json:"id"`
	ThreadID  int64  `json:"thread_id"`
	Role      string `json:"role"` // "user" or "assistant"
	Content   string `json:"content"`
	Status    string `json:"status"` // StatusPending | StatusDone | StatusError
	Mutated   bool   `json:"mutated"`
	CreatedAt string `json:"created_at"`
	// Images attached to a user message. Only ID and MediaType are populated
	// when listing; the bytes are loaded on demand with GetChatImage.
	Images []ChatImage `json:"images,omitempty"`
}

// ChatImage is a photo the user attached to a chat message (stored in SQLite
// after the browser has downscaled it).
type ChatImage struct {
	ID        int64  `json:"id"`
	MessageID int64  `json:"message_id"`
	MediaType string `json:"media_type"` // image/jpeg, image/png, image/gif, image/webp
	Data      []byte `json:"-"`
}

// Pending reports whether an assistant reply is still being generated.
func (m ChatMessage) Pending() bool { return m.Status == StatusPending }

// --- Threads ---

func (s *Store) CreateThread(title string) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO chat_threads (title) VALUES (?)`, title)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) GetThread(id int64) (ChatThread, bool) {
	var t ChatThread
	err := s.db.QueryRow(`SELECT id, title, updated_at FROM chat_threads WHERE id = ?`, id).Scan(&t.ID, &t.Title, &t.UpdatedAt)
	return t, err == nil
}

// ListThreads returns conversations, most recently active first.
func (s *Store) ListThreads(limit int) ([]ChatThread, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`SELECT id, title, updated_at FROM chat_threads ORDER BY updated_at DESC, id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatThread
	for rows.Next() {
		var t ChatThread
		if err := rows.Scan(&t.ID, &t.Title, &t.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) RenameThread(id int64, title string) error {
	_, err := s.db.Exec(`UPDATE chat_threads SET title = ? WHERE id = ?`, title, id)
	return err
}

func (s *Store) DeleteThread(id int64) error {
	// Delete messages explicitly: the FK cascade only applies to columns that
	// were declared with it at table creation, not ones added via ALTER.
	if _, err := s.db.Exec(`DELETE FROM chat_images WHERE message_id IN (SELECT id FROM chat_messages WHERE thread_id = ?)`, id); err != nil {
		return err
	}
	if _, err := s.db.Exec(`DELETE FROM chat_messages WHERE thread_id = ?`, id); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM chat_threads WHERE id = ?`, id)
	return err
}

func (s *Store) touchThread(threadID int64) error {
	_, err := s.db.Exec(`UPDATE chat_threads SET updated_at = datetime('now') WHERE id = ?`, threadID)
	return err
}

// --- Messages ---

func (s *Store) AddChatMessage(threadID int64, role, content string) error {
	_, err := s.AddChatMessageWithImages(threadID, role, content, nil)
	return err
}

// AddChatMessageWithImages stores a message plus any attached images and
// returns the new message id.
func (s *Store) AddChatMessageWithImages(threadID int64, role, content string, images []ChatImage) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO chat_messages (thread_id, role, content) VALUES (?, ?, ?)`, threadID, role, content)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	for _, img := range images {
		if _, err := s.db.Exec(`INSERT INTO chat_images (message_id, media_type, data) VALUES (?, ?, ?)`, id, img.MediaType, img.Data); err != nil {
			return 0, err
		}
	}
	return id, s.touchThread(threadID)
}

// AddPendingAssistant inserts a placeholder assistant reply that a background
// goroutine will fill in later, and returns its id so the UI can poll for it.
func (s *Store) AddPendingAssistant(threadID int64) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO chat_messages (thread_id, role, content, status) VALUES (?, 'assistant', '', ?)`,
		threadID, StatusPending)
	if err != nil {
		return 0, err
	}
	if err := s.touchThread(threadID); err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// FinishChatMessage fills in a pending assistant reply with its final content
// and status (StatusDone or StatusError).
func (s *Store) FinishChatMessage(id int64, content, status string, mutated bool) error {
	m := 0
	if mutated {
		m = 1
	}
	_, err := s.db.Exec(
		`UPDATE chat_messages SET content = ?, status = ?, mutated = ? WHERE id = ?`,
		content, status, m, id)
	return err
}

// GetChatMessage returns one message by id.
func (s *Store) GetChatMessage(id int64) (ChatMessage, bool) {
	var m ChatMessage
	err := s.db.QueryRow(
		`SELECT id, thread_id, role, content, status, mutated, created_at FROM chat_messages WHERE id = ?`, id).
		Scan(&m.ID, &m.ThreadID, &m.Role, &m.Content, &m.Status, &m.Mutated, &m.CreatedAt)
	if err != nil {
		return m, false
	}
	m.Images, _ = s.listImageRefs(m.ID)
	return m, true
}

// ListChatMessages returns the most recent messages in a thread, oldest first.
func (s *Store) ListChatMessages(threadID int64, limit int) ([]ChatMessage, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.db.Query(`
SELECT id, thread_id, role, content, status, mutated, created_at FROM chat_messages
WHERE thread_id = ? ORDER BY id DESC LIMIT ?`, threadID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var msgs []ChatMessage
	for rows.Next() {
		var m ChatMessage
		if err := rows.Scan(&m.ID, &m.ThreadID, &m.Role, &m.Content, &m.Status, &m.Mutated, &m.CreatedAt); err != nil {
			return nil, err
		}
		msgs = append(msgs, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reverse into chronological order.
	for i, j := 0, len(msgs)-1; i < j; i, j = i+1, j-1 {
		msgs[i], msgs[j] = msgs[j], msgs[i]
	}
	for i := range msgs {
		if msgs[i].Role == "user" {
			msgs[i].Images, _ = s.listImageRefs(msgs[i].ID)
		}
	}
	return msgs, nil
}

// --- Images ---

// GetChatImage loads one attached image, bytes included.
func (s *Store) GetChatImage(id int64) (ChatImage, bool) {
	var img ChatImage
	err := s.db.QueryRow(`SELECT id, message_id, media_type, data FROM chat_images WHERE id = ?`, id).
		Scan(&img.ID, &img.MessageID, &img.MediaType, &img.Data)
	return img, err == nil
}

// listImageRefs returns the id/media type of every image on a message (no bytes).
func (s *Store) listImageRefs(messageID int64) ([]ChatImage, error) {
	rows, err := s.db.Query(`SELECT id, message_id, media_type FROM chat_images WHERE message_id = ? ORDER BY id`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatImage
	for rows.Next() {
		var img ChatImage
		if err := rows.Scan(&img.ID, &img.MessageID, &img.MediaType); err != nil {
			return nil, err
		}
		out = append(out, img)
	}
	return out, rows.Err()
}
