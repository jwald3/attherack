package store

import (
	"bytes"
	"testing"

	"github.com/jwald3/attherack/internal/testutil"
)

func TestMeasurementsStore(t *testing.T) {
	st := newTestStore(t)

	// UPSERT: logging the same (date, site) twice keeps one row, latest value.
	if err := st.LogMeasurement("2026-09-20", "waist", 34); err != nil {
		t.Fatal(err)
	}
	if err := st.LogMeasurement("2026-09-20", "waist", 33.5); err != nil {
		t.Fatal(err)
	}
	if err := st.LogMeasurement("2026-09-25", "waist", 33); err != nil {
		t.Fatal(err)
	}
	if err := st.LogMeasurement("2026-09-25", "chest", 42); err != nil {
		t.Fatal(err)
	}

	// History is oldest-first and reflects the overwrite (33.5, not 34).
	pts, err := st.MeasurementHistory("waist", 60)
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

	// LatestMeasurements returns the newest per site.
	latest, err := st.LatestMeasurements()
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
	if err := st.DeleteMeasurement("2026-09-25", "waist"); err != nil {
		t.Fatal(err)
	}
	pts, _ = st.MeasurementHistory("waist", 60)
	if len(pts) != 1 || pts[0].Value != 33.5 {
		t.Fatalf("after delete expected 1 waist point (33.5), got %+v", pts)
	}
}

func TestProgressPhotosStore(t *testing.T) {
	st := newTestStore(t)

	id, err := st.AddProgressPhoto("2026-09-25", "front", "image/png", testutil.TinyPNG)
	if err != nil {
		t.Fatal(err)
	}

	// Listing returns metadata only, no bytes.
	list, err := st.ListProgressPhotos()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != id || list[0].Pose != "front" || list[0].Data != nil {
		t.Fatalf("ListProgressPhotos wrong: %+v", list)
	}

	// GetProgressPhoto loads the bytes back intact.
	p, ok := st.GetProgressPhoto(id)
	if !ok || p.MediaType != "image/png" || !bytes.Equal(p.Data, testutil.TinyPNG) {
		t.Fatalf("GetProgressPhoto returned %+v ok=%v", p, ok)
	}

	if err := st.DeleteProgressPhoto(id); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.GetProgressPhoto(id); ok {
		t.Fatal("photo still present after delete")
	}
}
