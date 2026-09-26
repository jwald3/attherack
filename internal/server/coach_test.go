package server

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jwald3/attherack/internal/store"
	"github.com/jwald3/attherack/internal/testutil"
)

func TestBackgroundReplyLifecycle(t *testing.T) {
	app := newTestApp(t)
	tid, err := app.store.CreateThread("t")
	if err != nil {
		t.Fatal(err)
	}
	_ = app.store.AddChatMessage(tid, "user", "did 3x5 squats")

	// Simulate what handleChat does: insert a pending assistant reply.
	pid, err := app.store.AddPendingAssistant(tid)
	if err != nil {
		t.Fatal(err)
	}

	// Poll while pending -> should return a polling placeholder that targets
	// this message id and keeps polling.
	body := get(app, "/chat/msg/"+itoa(pid)).Body.String()
	if !strings.Contains(body, "pending") || !strings.Contains(body, "/chat/msg/"+itoa(pid)) {
		t.Fatalf("pending poll should return a self-polling placeholder, got:\n%s", body)
	}
	if strings.Contains(body, "squat power") {
		t.Fatal("reply leaked before it was finished")
	}

	// Background goroutine finishes the reply.
	if err := app.store.FinishChatMessage(pid, "Nice **squat power** today!", store.StatusDone, true); err != nil {
		t.Fatal(err)
	}

	// Poll again -> final rendered bubble, no more polling, log-refresh marker.
	body = get(app, "/chat/msg/"+itoa(pid)).Body.String()
	if strings.Contains(body, "hx-get=\"/chat/msg/") {
		t.Fatalf("finished reply must stop polling, got:\n%s", body)
	}
	if !strings.Contains(body, "<strong>squat power</strong>") {
		t.Fatalf("finished reply should render markdown, got:\n%s", body)
	}
	if !strings.Contains(body, "data-refresh-log") {
		t.Fatalf("mutated reply should carry the log-refresh marker, got:\n%s", body)
	}

	// Reload path: the coach page renders a pending reply as a poller so it
	// auto-resumes.
	pid2, _ := app.store.AddPendingAssistant(tid)
	body = get(app, "/c/"+itoa(tid)).Body.String()
	if !strings.Contains(body, `hx-get="/chat/msg/`+itoa(pid2)+`"`) {
		t.Fatalf("coach page should render the pending reply as a poller, got:\n%s", body)
	}
}

func TestChatImageServingAndThumbnails(t *testing.T) {
	app := newTestApp(t)
	tid, _ := app.store.CreateThread("t")
	mid, err := app.store.AddChatMessageWithImages(tid, "user", "what machine is this?", []store.ChatImage{{MediaType: "image/png", Data: testutil.TinyPNG}})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := app.store.GetChatMessage(mid)
	if len(m.Images) != 1 {
		t.Fatalf("want 1 image ref, got %+v", m.Images)
	}
	imgID := m.Images[0].ID

	// Serving the image.
	rr := get(app, "/chat/img/"+itoa(imgID))
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rr.Body.Bytes(), testutil.TinyPNG) {
		t.Fatalf("serve image: code=%d type=%q", rr.Code, rr.Header().Get("Content-Type"))
	}

	// The rendered user bubble shows a thumbnail.
	rr = httptest.NewRecorder()
	writeUserBubble(rr, m)
	if !strings.Contains(rr.Body.String(), `<img src="/chat/img/`+itoa(imgID)+`"`) {
		t.Fatalf("user bubble should include thumbnail, got %s", rr.Body.String())
	}
}
