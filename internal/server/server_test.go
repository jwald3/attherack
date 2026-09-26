package server

import (
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/jwald3/attherack/internal/store"
)

// newTestApp builds an App with a real in-temp store and the real templates,
// but no agent (we don't hit the Anthropic API here).
func newTestApp(t *testing.T) *App {
	t.Helper()
	st, err := store.Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	// Close the DB before TempDir cleanup so Windows can delete the file.
	t.Cleanup(func() { st.Close() })
	app, err := New(st, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	return app
}

// get runs a GET through the real router.
func get(app *App, path string) *httptest.ResponseRecorder {
	rr := httptest.NewRecorder()
	app.Handler().ServeHTTP(rr, httptest.NewRequest("GET", path, nil))
	return rr
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
