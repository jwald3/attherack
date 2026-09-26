package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A 1x1 PNG, the smallest thing http.DetectContentType calls image/png.
var tinyPNG = []byte{
	0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52,
	0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x06, 0x00, 0x00, 0x00, 0x1f, 0x15, 0xc4,
	0x89, 0x00, 0x00, 0x00, 0x0a, 0x49, 0x44, 0x41, 0x54, 0x78, 0x9c, 0x63, 0x00, 0x01, 0x00, 0x00,
	0x05, 0x00, 0x01, 0x0d, 0x0a, 0x2d, 0xb4, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4e, 0x44, 0xae,
	0x42, 0x60, 0x82,
}

func TestChatImagesStoreRoundTrip(t *testing.T) {
	app := newTestApp(t)
	tid, err := app.store.createThread("t")
	if err != nil {
		t.Fatal(err)
	}
	mid, err := app.store.addChatMessageWithImages(tid, "user", "what machine is this?", []ChatImage{{MediaType: "image/png", Data: tinyPNG}})
	if err != nil {
		t.Fatal(err)
	}

	msgs, err := app.store.listChatMessages(tid, 10)
	if err != nil || len(msgs) != 1 {
		t.Fatalf("list: %v, %d msgs", err, len(msgs))
	}
	if len(msgs[0].Images) != 1 || msgs[0].Images[0].MediaType != "image/png" || msgs[0].Images[0].Data != nil {
		t.Fatalf("listing should carry image refs without bytes, got %+v", msgs[0].Images)
	}
	img, ok := app.store.getChatImage(msgs[0].Images[0].ID)
	if !ok || !bytes.Equal(img.Data, tinyPNG) || img.MessageID != mid {
		t.Fatalf("getChatImage returned %+v ok=%v", img, ok)
	}

	// Serving the image.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/chat/img/0", nil)
	req.SetPathValue("id", itoa(img.ID))
	app.handleChatImage(rr, req)
	if rr.Code != 200 || rr.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rr.Body.Bytes(), tinyPNG) {
		t.Fatalf("serve image: code=%d type=%q", rr.Code, rr.Header().Get("Content-Type"))
	}

	// The rendered user bubble shows a thumbnail.
	rr = httptest.NewRecorder()
	app.writeUserBubble(rr, msgs[0])
	if !strings.Contains(rr.Body.String(), `<img src="/chat/img/`+itoa(img.ID)+`"`) {
		t.Fatalf("user bubble should include thumbnail, got %s", rr.Body.String())
	}

	// Deleting the thread takes its images with it.
	if err := app.store.deleteThread(tid); err != nil {
		t.Fatal(err)
	}
	if _, ok := app.store.getChatImage(img.ID); ok {
		t.Fatal("image survived thread deletion")
	}
}

func multipartChat(t *testing.T, text string, files map[string][]byte) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("message", text)
	for name, data := range files {
		fw, err := mw.CreateFormFile("images", name)
		if err != nil {
			t.Fatal(err)
		}
		fw.Write(data)
	}
	mw.Close()
	req := httptest.NewRequest("POST", "/chat", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return req
}

func TestReadChatImages(t *testing.T) {
	req := multipartChat(t, "hi", map[string][]byte{"a.png": tinyPNG})
	if err := req.ParseMultipartForm(maxChatFormBytes); err != nil {
		t.Fatal(err)
	}
	imgs, err := readChatImages(req)
	if err != nil || len(imgs) != 1 || imgs[0].MediaType != "image/png" {
		t.Fatalf("got %v, %+v", err, imgs)
	}

	// A non-image is rejected by sniffing, regardless of its name.
	req = multipartChat(t, "hi", map[string][]byte{"fake.jpg": []byte("hello, not an image at all")})
	if err := req.ParseMultipartForm(maxChatFormBytes); err != nil {
		t.Fatal(err)
	}
	if _, err := readChatImages(req); err == nil {
		t.Fatal("expected a rejection for a non-image upload")
	}

	// Plain form posts (no multipart) have no images and no error.
	plain := httptest.NewRequest("POST", "/chat", strings.NewReader("message=hi"))
	plain.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_ = plain.ParseForm()
	if imgs, err := readChatImages(plain); err != nil || len(imgs) != 0 {
		t.Fatalf("plain post: %v %+v", err, imgs)
	}
}

func TestBuildMessagesWithImages(t *testing.T) {
	app := newTestApp(t)
	agent := &Agent{store: app.store}
	tid, _ := app.store.createThread("t")
	_, err := app.store.addChatMessageWithImages(tid, "user", "", []ChatImage{{MediaType: "image/png", Data: tinyPNG}})
	if err != nil {
		t.Fatal(err)
	}
	_ = app.store.addChatMessage(tid, "assistant", "That's a pec deck.")
	history, _ := app.store.listChatMessages(tid, 10)

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
	app := newTestApp(t)
	ct := &captureTransport{}
	agent := &Agent{apiKey: "sk-ant-test", store: app.store, lib: &ExerciseLibrary{}, http: &http.Client{Transport: ct}}

	reply, _, err := agent.Chat(nil, "", []ChatImage{{MediaType: "image/png", Data: tinyPNG}})
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
	if got, _ := base64.StdEncoding.DecodeString(img.Source.Data); !bytes.Equal(got, tinyPNG) {
		t.Fatal("image bytes were not base64-encoded verbatim")
	}
	if txt.Type != "text" || txt.Text == "" {
		t.Fatalf("photo-only message needs a stand-in text block, got %+v", txt)
	}
}
