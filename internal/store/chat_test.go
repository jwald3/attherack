package store

import (
	"bytes"
	"testing"

	"github.com/jwald3/attherack/internal/testutil"
)

func TestChatImagesRoundTrip(t *testing.T) {
	st := newTestStore(t)
	tid, err := st.CreateThread("t")
	if err != nil {
		t.Fatal(err)
	}
	mid, err := st.AddChatMessageWithImages(tid, "user", "what machine is this?", []ChatImage{{MediaType: "image/png", Data: testutil.TinyPNG}})
	if err != nil {
		t.Fatal(err)
	}

	msgs, err := st.ListChatMessages(tid, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("list: %v, %d msgs", err, len(msgs))
	}
	if len(msgs[0].Images) != 1 || msgs[0].Images[0].MediaType != "image/png" || msgs[0].Images[0].Data != nil {
		t.Fatalf("listing should carry image refs without bytes, got %+v", msgs[0].Images)
	}
	img, ok := st.GetChatImage(msgs[0].Images[0].ID)
	if !ok || !bytes.Equal(img.Data, testutil.TinyPNG) || img.MessageID != mid {
		t.Fatalf("GetChatImage returned %+v ok=%v", img, ok)
	}

	// Deleting the thread takes its images with it.
	if err := st.DeleteThread(tid); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.GetChatImage(img.ID); ok {
		t.Fatal("image survived thread deletion")
	}
}

func TestPendingReplyLifecycle(t *testing.T) {
	st := newTestStore(t)
	tid, _ := st.CreateThread("t")
	pid, err := st.AddPendingAssistant(tid)
	if err != nil {
		t.Fatal(err)
	}

	// A pending reply survives a fresh listing, so a reload resumes polling.
	msgs, _ := st.ListChatMessages(tid, 100)
	if len(msgs) != 1 || msgs[0].ID != pid || !msgs[0].Pending() {
		t.Fatalf("a pending reply should survive a fresh ListChatMessages, got %+v", msgs)
	}

	if err := st.FinishChatMessage(pid, "done!", StatusDone, true); err != nil {
		t.Fatal(err)
	}
	m, ok := st.GetChatMessage(pid)
	if !ok || m.Pending() || m.Content != "done!" || !m.Mutated {
		t.Fatalf("finished reply wrong: %+v ok=%v", m, ok)
	}
}

func TestSetNotFound(t *testing.T) {
	st := newTestStore(t)
	if _, err := st.GetSet(42); err != ErrNotFound {
		t.Fatalf("GetSet on a missing id: %v, want ErrNotFound", err)
	}
	if _, err := st.UpdateSet(42, 100, 5, nil); err != ErrNotFound {
		t.Fatalf("UpdateSet on a missing id: %v, want ErrNotFound", err)
	}
	set, err := st.LogSet("2026-09-25", "Squat", 225, 5, nil)
	if err != nil {
		t.Fatal(err)
	}
	d, err := st.UpdateSet(set.ID, 230, 4, nil)
	if err != nil || d.Date != "2026-09-25" || d.Set.Weight != 230 || d.Set.Reps != 4 {
		t.Fatalf("UpdateSet: %+v %v", d, err)
	}
}
