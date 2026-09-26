// Package config resolves runtime settings from the environment and an
// optional .env file.
package config

import (
	"os"
	"strings"
)

// Config is the process-wide configuration read at startup.
type Config struct {
	// Addr is the listen address. Localhost only by default: there is no
	// login, so anyone who can reach the port can read your data and use your
	// API key. Set ADDR=:8080 to expose it.
	Addr string
	// DBPath is the SQLite file, created on first run.
	DBPath string
	// APIKey is ANTHROPIC_API_KEY; when set it overrides (and locks) any key
	// saved in the app.
	APIKey string
	// AnthropicBaseURL overrides the API host, for proxies and the end-to-end
	// tests' fake API. Empty means the real API.
	AnthropicBaseURL string
}

// FromEnv reads the configuration from environment variables, applying
// defaults. Call LoadDotEnv first to pick up a .env file.
func FromEnv() Config {
	return Config{
		Addr:             envOr("ADDR", "127.0.0.1:8080"),
		DBPath:           envOr("DB_PATH", "attherack.db"),
		APIKey:           os.Getenv("ANTHROPIC_API_KEY"),
		AnthropicBaseURL: os.Getenv("ANTHROPIC_BASE_URL"),
	}
}

// LoadDotEnv reads KEY=VALUE lines from path (if it exists) into the process
// environment. Variables already set in the real environment win. Blank lines
// and # comments are skipped; surrounding quotes on values are removed.
func LoadDotEnv(path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(strings.TrimPrefix(line, "export "), "=")
		key, val = strings.TrimSpace(key), strings.TrimSpace(val)
		if !ok || key == "" || val == "" {
			continue
		}
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, val)
		}
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
