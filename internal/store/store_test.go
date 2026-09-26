package store

import "testing"

func newTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(t.TempDir() + "/t.db")
	if err != nil {
		t.Fatal(err)
	}
	// Close before TempDir cleanup so Windows can delete the file.
	t.Cleanup(func() { st.Close() })
	return st
}
