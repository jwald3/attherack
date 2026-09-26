package main

import (
	"html/template"
	"net/http/httptest"
	"strings"
	"testing"
)

// newTestApp builds an App with a real in-temp store and the real templates,
// but no agent (we don't hit the Anthropic API here).
func newTestApp(t *testing.T) *App {
	t.Helper()
	st, err := openStore(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	// Close the DB before TempDir cleanup so Windows can delete the file.
	t.Cleanup(func() { st.db.Close() })
	tmpl, err := template.New("").Funcs(templateFuncs()).ParseFS(templateFS, "web/templates/*.html")
	if err != nil {
		t.Fatal(err)
	}
	return &App{store: st, tmpl: tmpl}
}

func TestBackgroundReplyLifecycle(t *testing.T) {
	app := newTestApp(t)
	tid, err := app.store.createThread("t")
	if err != nil {
		t.Fatal(err)
	}
	_ = app.store.addChatMessage(tid, "user", "did 3x5 squats")

	// Simulate what handleChat does: insert a pending assistant reply.
	pid, err := app.store.addPendingAssistant(tid)
	if err != nil {
		t.Fatal(err)
	}

	// Poll while pending -> should return a polling placeholder that targets
	// this message id and keeps polling.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/chat/msg/0", nil)
	req.SetPathValue("id", itoa(pid))
	app.handleChatMessage(rr, req)
	body := rr.Body.String()
	if !strings.Contains(body, "pending") || !strings.Contains(body, "/chat/msg/"+itoa(pid)) {
		t.Fatalf("pending poll should return a self-polling placeholder, got:\n%s", body)
	}
	if strings.Contains(body, "squat power") {
		t.Fatal("reply leaked before it was finished")
	}

	// Background goroutine finishes the reply.
	if err := app.store.finishChatMessage(pid, "Nice **squat power** today!", "done", true); err != nil {
		t.Fatal(err)
	}

	// Poll again -> final rendered bubble, no more polling, log-refresh marker.
	rr = httptest.NewRecorder()
	req = httptest.NewRequest("GET", "/chat/msg/0", nil)
	req.SetPathValue("id", itoa(pid))
	app.handleChatMessage(rr, req)
	body = rr.Body.String()
	if strings.Contains(body, "hx-get=\"/chat/msg/") {
		t.Fatalf("finished reply must stop polling, got:\n%s", body)
	}
	if !strings.Contains(body, "<strong>squat power</strong>") {
		t.Fatalf("finished reply should render markdown, got:\n%s", body)
	}
	if !strings.Contains(body, "data-refresh-log") {
		t.Fatalf("mutated reply should carry the log-refresh marker, got:\n%s", body)
	}

	// Reload path: the coach page must render a pending reply as a poller so it
	// auto-resumes. Insert a fresh pending row and render the message list.
	pid2, _ := app.store.addPendingAssistant(tid)
	msgs, _ := app.store.listChatMessages(tid, 100)
	var sawPending bool
	for _, m := range msgs {
		if m.ID == pid2 && m.Pending() {
			sawPending = true
		}
	}
	if !sawPending {
		t.Fatal("a pending reply should survive a fresh listChatMessages (reload)")
	}
}

// itoa avoids importing strconv just for the test file.
func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
