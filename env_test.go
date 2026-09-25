package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDotEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	content := "# comment\r\n\r\nATR_PLAIN=one\r\nexport ATR_EXPORTED=two\nATR_DQ=\"three four\"\nATR_SQ='five'\nATR_EMPTY=\nATR_SET=from-file\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ATR_SET", "from-env") // the real environment must win

	loadDotEnv(path)

	for key, want := range map[string]string{
		"ATR_PLAIN":    "one",
		"ATR_EXPORTED": "two",
		"ATR_DQ":       "three four",
		"ATR_SQ":       "five",
		"ATR_SET":      "from-env",
	} {
		if got := os.Getenv(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
		if key != "ATR_SET" {
			os.Unsetenv(key)
		}
	}
	if _, set := os.LookupEnv("ATR_EMPTY"); set {
		t.Error("empty value should not be set")
	}
}

func TestLoadDotEnvMissingFile(t *testing.T) {
	loadDotEnv(filepath.Join(t.TempDir(), "nope.env")) // must not panic
}
