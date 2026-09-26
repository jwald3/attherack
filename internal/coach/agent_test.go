package coach

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/jwald3/attherack/internal/exercise"
	"github.com/jwald3/attherack/internal/store"
	"github.com/jwald3/attherack/internal/testutil"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	// Close before TempDir cleanup so Windows can delete the file.
	t.Cleanup(func() { st.Close() })
	return st
}

func TestBuildMessagesWithImages(t *testing.T) {
	st := newTestStore(t)
	agent := &Agent{store: st}
	tid, _ := st.CreateThread("t")
	_, err := st.AddChatMessageWithImages(tid, "user", "", []store.ChatImage{{MediaType: "image/png", Data: testutil.TinyPNG}})
	if err != nil {
		t.Fatal(err)
	}
	_ = st.AddChatMessage(tid, "assistant", "That's a pec deck.")
	history, _ := st.ListChatMessages(tid, 10)

	msgs := agent.buildMessages(history, "how do I set it up?", nil)
	if len(msgs) != 3 {
		t.Fatalf("want 3 messages, got %d", len(msgs))
	}
	// The earlier photo is reloaded from the store and sent ahead of its text;
	// a caption-less photo gets a stand-in text block so the turn isn't empty.
	first := msgs[0].Content
	if len(first) != 2 || first[0].Type != "image" || first[1].Type != "text" {
		t.Fatalf("first turn should be [image, text], got %+v", first)
	}
	if first[0].Source == nil || first[0].Source.MediaType != "image/png" || first[0].Source.Data == "" {
		t.Fatalf("image block missing base64 source: %+v", first[0])
	}
	raw, _ := json.Marshal(first[0])
	if !strings.Contains(string(raw), `"type":"image"`) || !strings.Contains(string(raw), `"type":"base64"`) {
		t.Fatalf("image block wire shape wrong: %s", raw)
	}
	if strings.Contains(string(raw), `"text"`) {
		t.Fatalf("image block must not carry a text field: %s", raw)
	}
	if last := msgs[2].Content; len(last) != 1 || last[0].Text != "how do I set it up?" {
		t.Fatalf("last turn wrong: %+v", last)
	}
}

// captureTransport records the outbound Claude request and answers with a
// canned end_turn reply, so Chat can be run without the network.
type captureTransport struct{ body []byte }

func (c *captureTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	c.body, _ = io.ReadAll(r.Body)
	reply := `{"content":[{"type":"text","text":"That's a pec deck."}],"stop_reason":"end_turn"}`
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(reply)),
		Request:    r,
	}, nil
}

func TestChatSendsImageBlocks(t *testing.T) {
	st := newTestStore(t)
	ct := &captureTransport{}
	agent := &Agent{apiKey: "sk-ant-test", store: st, lib: &exercise.Library{}, http: &http.Client{Transport: ct}}

	reply, _, err := agent.Chat(nil, "", []store.ChatImage{{MediaType: "image/png", Data: testutil.TinyPNG}})
	if err != nil || reply != "That's a pec deck." {
		t.Fatalf("Chat: %v, reply=%q", err, reply)
	}
	var req struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type   string `json:"type"`
				Text   string `json:"text"`
				Source *struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				} `json:"source"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(ct.body, &req); err != nil {
		t.Fatalf("request body not JSON: %v\n%s", err, ct.body)
	}
	if len(req.Messages) != 1 || len(req.Messages[0].Content) != 2 {
		t.Fatalf("want one user message with [image, text], got %s", ct.body)
	}
	img, txt := req.Messages[0].Content[0], req.Messages[0].Content[1]
	if img.Type != "image" || img.Source == nil || img.Source.Type != "base64" || img.Source.MediaType != "image/png" {
		t.Fatalf("bad image block: %+v", img)
	}
	if got, _ := base64.StdEncoding.DecodeString(img.Source.Data); !bytes.Equal(got, testutil.TinyPNG) {
		t.Fatal("image bytes were not base64-encoded verbatim")
	}
	if txt.Type != "text" || txt.Text == "" {
		t.Fatalf("photo-only message needs a stand-in text block, got %+v", txt)
	}
}
