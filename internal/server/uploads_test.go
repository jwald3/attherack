package server

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jwald3/attherack/internal/testutil"
)

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
	req := multipartChat(t, "hi", map[string][]byte{"a.png": testutil.TinyPNG})
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

func TestReadUploadedImage(t *testing.T) {
	// A valid PNG passes and is detected as image/png.
	mt, data, err := readUploadedImage(fileHeader(t, "shot.png", testutil.TinyPNG))
	if err != nil || mt != "image/png" || !bytes.Equal(data, testutil.TinyPNG) {
		t.Fatalf("valid PNG: mt=%q err=%v", mt, err)
	}

	// A non-image is rejected.
	if _, _, err := readUploadedImage(fileHeader(t, "notes.txt", []byte("just some text, not an image"))); err == nil {
		t.Fatal("expected non-image to be rejected")
	}

	// An oversize file is rejected.
	big := make([]byte, maxChatImageBytes+10)
	copy(big, testutil.TinyPNG)
	if _, _, err := readUploadedImage(fileHeader(t, "big.png", big)); err == nil {
		t.Fatal("expected oversize file to be rejected")
	}
}

// fileHeader builds a *multipart.FileHeader carrying the given bytes, by writing
// a real multipart body and parsing it back — the only way to get a FileHeader.
func fileHeader(t *testing.T, name string, data []byte) *multipart.FileHeader {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("f", name)
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(data)
	mw.Close()

	mr := multipart.NewReader(&buf, mw.Boundary())
	form, err := mr.ReadForm(int64(len(data)) + 4096)
	if err != nil {
		t.Fatal(err)
	}
	fhs := form.File["f"]
	if len(fhs) != 1 {
		t.Fatalf("expected 1 file, got %d", len(fhs))
	}
	return fhs[0]
}
