package server

import (
	"fmt"
	"io"
	"mime/multipart"
	"net/http"

	"github.com/jwald3/attherack/internal/store"
)

// Limits on uploaded photos (chat attachments and progress photos). The
// browser downscales before upload, so these are backstops; the API itself
// caps images at 5 MB each.
const (
	maxChatImages     = 4
	maxChatImageBytes = 5 << 20
	maxChatFormBytes  = 24 << 20
)

// readChatImages pulls the "images" file parts off a multipart chat post,
// sniffing each one's real type (the browser's claimed type is ignored).
func readChatImages(r *http.Request) ([]store.ChatImage, error) {
	if r.MultipartForm == nil {
		return nil, nil
	}
	files := r.MultipartForm.File["images"]
	if len(files) > maxChatImages {
		return nil, fmt.Errorf("You can attach up to %d photos per message.", maxChatImages)
	}
	var out []store.ChatImage
	for _, fh := range files {
		mt, data, err := readUploadedImage(fh)
		if err != nil {
			return nil, err
		}
		if data == nil {
			continue // empty file part
		}
		out = append(out, store.ChatImage{MediaType: mt, Data: data})
	}
	return out, nil
}

// readUploadedImage reads one uploaded file, enforces the 5 MB cap, and verifies
// it's a supported image by content sniff. It returns (mediaType, bytes) or a
// user-facing error. bytes is nil (with no error) for an empty file part.
func readUploadedImage(fh *multipart.FileHeader) (string, []byte, error) {
	if fh.Size > maxChatImageBytes {
		return "", nil, fmt.Errorf("%s is too large (max 5 MB per photo).", fh.Filename)
	}
	f, err := fh.Open()
	if err != nil {
		return "", nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, maxChatImageBytes+1))
	f.Close()
	if err != nil {
		return "", nil, err
	}
	if len(data) == 0 {
		return "", nil, nil
	}
	if len(data) > maxChatImageBytes {
		return "", nil, fmt.Errorf("%s is too large (max 5 MB per photo).", fh.Filename)
	}
	mt := http.DetectContentType(data)
	switch mt {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
	default:
		return "", nil, fmt.Errorf("%s isn't a supported image (use JPEG, PNG, GIF or WebP).", fh.Filename)
	}
	return mt, data, nil
}

// writeImage serves stored image bytes. Stored images never change, so they
// can be cached forever.
func writeImage(w http.ResponseWriter, mediaType string, data []byte) {
	w.Header().Set("Content-Type", mediaType)
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Write(data)
}
