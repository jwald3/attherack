package main

import (
	"bytes"
	"mime/multipart"
	"strings"
	"testing"
)

func TestMeasurementsStore(t *testing.T) {
	st := newTestStore(t)

	// UPSERT: logging the same (date, site) twice keeps one row, latest value.
	if err := st.logMeasurement("2026-09-20", "waist", 34); err != nil {
		t.Fatal(err)
	}
	if err := st.logMeasurement("2026-09-20", "waist", 33.5); err != nil {
		t.Fatal(err)
	}
	if err := st.logMeasurement("2026-09-25", "waist", 33); err != nil {
		t.Fatal(err)
	}
	if err := st.logMeasurement("2026-09-25", "chest", 42); err != nil {
		t.Fatal(err)
	}

	// History is oldest-first and reflects the overwrite (33.5, not 34).
	pts, err := st.measurementHistory("waist", 60)
	if err != nil {
		t.Fatal(err)
	}
	if len(pts) != 2 {
		t.Fatalf("expected 2 waist points, got %d: %+v", len(pts), pts)
	}
	if pts[0].Date != "2026-09-20" || pts[0].Value != 33.5 {
		t.Fatalf("first point wrong (upsert/order): %+v", pts[0])
	}
	if pts[1].Date != "2026-09-25" || pts[1].Value != 33 {
		t.Fatalf("second point wrong: %+v", pts[1])
	}

	// latestMeasurements returns the newest per site.
	latest, err := st.latestMeasurements()
	if err != nil {
		t.Fatal(err)
	}
	if latest["waist"].Value != 33 || latest["waist"].Date != "2026-09-25" {
		t.Fatalf("latest waist wrong: %+v", latest["waist"])
	}
	if latest["chest"].Value != 42 {
		t.Fatalf("latest chest wrong: %+v", latest["chest"])
	}

	// Delete removes just that site+date.
	if err := st.deleteMeasurement("2026-09-25", "waist"); err != nil {
		t.Fatal(err)
	}
	pts, _ = st.measurementHistory("waist", 60)
	if len(pts) != 1 || pts[0].Value != 33.5 {
		t.Fatalf("after delete expected 1 waist point (33.5), got %+v", pts)
	}
}

func TestProgressPhotosStore(t *testing.T) {
	st := newTestStore(t)

	id, err := st.addProgressPhoto("2026-09-25", "front", "image/png", tinyPNG)
	if err != nil {
		t.Fatal(err)
	}

	// Listing returns metadata only, no bytes.
	list, err := st.listProgressPhotos()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].Pose != "front" || list[0].Data != nil {
		t.Fatalf("listProgressPhotos wrong: %+v", list)
	}

	// getProgressPhoto loads the bytes back intact.
	p, ok := st.getProgressPhoto(id)
	if !ok || p.MediaType != "image/png" || !bytes.Equal(p.Data, tinyPNG) {
		t.Fatalf("getProgressPhoto returned %+v ok=%v", p, ok)
	}

	if err := st.deleteProgressPhoto(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.getProgressPhoto(id); ok {
		t.Fatal("photo still present after delete")
	}
}

func TestReadUploadedImage(t *testing.T) {
	// A valid PNG passes and is detected as image/png.
	mt, data, err := readUploadedImage(fileHeader(t, "shot.png", tinyPNG))
	if err != nil || mt != "image/png" || !bytes.Equal(data, tinyPNG) {
		t.Fatalf("valid PNG: mt=%q err=%v", mt, err)
	}

	// A non-image is rejected.
	if _, _, err := readUploadedImage(fileHeader(t, "notes.txt", []byte("just some text, not an image"))); err == nil {
		t.Fatal("expected non-image to be rejected")
	}

	// An oversize file is rejected.
	big := make([]byte, maxChatImageBytes+10)
	copy(big, tinyPNG)
	if _, _, err := readUploadedImage(fileHeader(t, "big.png", big)); err == nil {
		t.Fatal("expected oversize file to be rejected")
	}
}

func TestCoachHasMeasurementTools(t *testing.T) {
	a := &Agent{}
	var names []string
	for _, td := range a.tools() {
		names = append(names, td.Name)
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"log_measurement", "get_measurement_history"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing tool %q; have: %s", want, joined)
		}
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
