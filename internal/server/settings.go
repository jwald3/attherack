package server

import (
	"log"
	"net/http"
	"strings"

	"github.com/jwald3/attherack/internal/coach"
	"github.com/jwald3/attherack/internal/store"
)

// --- API key state ---

// InitAPIKey resolves the key at startup: envKey (ANTHROPIC_API_KEY) wins and
// locks the UI; otherwise fall back to a key saved via the settings panel on a
// previous run.
func (app *App) InitAPIKey(envKey string) {
	if envKey != "" {
		app.setKey(envKey, true)
		log.Print("Claude chat enabled (ANTHROPIC_API_KEY set — locked in UI)")
		return
	}
	if saved, err := app.store.GetSetting(store.SettingAPIKey); err == nil && saved != "" {
		app.setKey(saved, false)
		log.Print("Claude chat enabled (key loaded from settings)")
		return
	}
	log.Print("Claude chat DISABLED — add a key in the coach panel or set ANTHROPIC_API_KEY")
}

// setKey installs a new API key at runtime and (re)builds the agent. Pass
// fromEnv=true only for the boot-time environment key.
func (app *App) setKey(key string, fromEnv bool) {
	app.mu.Lock()
	defer app.mu.Unlock()
	app.apiKey = key
	app.envKey = fromEnv
	if key == "" {
		app.agent = nil
		return
	}
	app.agent = coach.New(key, app.baseURL, app.store, app.lib)
}

// clearKey removes a UI-configured key. No-op if the key came from the env.
func (app *App) clearKey() {
	app.mu.Lock()
	defer app.mu.Unlock()
	if app.envKey {
		return
	}
	app.apiKey = ""
	app.agent = nil
}

func (app *App) getAgent() *coach.Agent {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.agent
}

func (app *App) chatEnabled() bool {
	return app.getAgent() != nil
}

func (app *App) envLocked() bool {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return app.envKey
}

// maskedKey returns a display-safe preview like "sk-ant-…a1b2", never the full key.
func (app *App) maskedKey() string {
	app.mu.RLock()
	defer app.mu.RUnlock()
	return maskKey(app.apiKey)
}

func maskKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 12 {
		return "••••"
	}
	return k[:7] + "…" + k[len(k)-4:]
}

// --- Settings panel ---

// settingsData drives the coach settings/API-key sub-panel.
type settingsData struct {
	Enabled   bool
	EnvLocked bool   // key came from ANTHROPIC_API_KEY; UI can't change it
	Masked    string // masked preview of the active key, or ""
	Msg       string // optional status message (shown after save/clear)
	Kind      string // "ok" or "err" — styles the status message
}

func (app *App) settingsData() settingsData {
	return settingsData{
		Enabled:   app.chatEnabled(),
		EnvLocked: app.envLocked(),
		Masked:    app.maskedKey(),
	}
}

// handleSettingsFragment re-renders just the settings sub-panel.
func (app *App) handleSettingsFragment(w http.ResponseWriter, r *http.Request) {
	app.render(w, "settings.html", app.settingsData())
}

func (app *App) handleSaveKey(w http.ResponseWriter, r *http.Request) {
	if app.envLocked() {
		app.renderSettings(w, "The key is set via ANTHROPIC_API_KEY and can't be changed here.", "err")
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	key := strings.TrimSpace(r.FormValue("api_key"))
	if key == "" {
		app.renderSettings(w, "Paste a key first.", "err")
		return
	}
	if !strings.HasPrefix(key, "sk-ant-") {
		app.renderSettings(w, "That doesn't look like an Anthropic key (should start with \"sk-ant-\").", "err")
		return
	}
	if err := app.store.SetSetting(store.SettingAPIKey, key); err != nil {
		app.renderSettings(w, "Couldn't save the key: "+err.Error(), "err")
		return
	}
	app.setKey(key, false)
	app.renderSettings(w, "Key saved — the coach is now enabled.", "ok")
}

func (app *App) handleClearKey(w http.ResponseWriter, r *http.Request) {
	if app.envLocked() {
		app.renderSettings(w, "The key is set via ANTHROPIC_API_KEY; unset the env var and restart to remove it.", "err")
		return
	}
	_ = app.store.DeleteSetting(store.SettingAPIKey)
	app.clearKey()
	app.renderSettings(w, "Key removed. The coach is disabled.", "ok")
}

// renderSettings re-renders the settings panel with a status message. The JS
// listener on "settings-changed" reloads the page so the coach's enabled state
// (chat input, empty note) flips to match.
func (app *App) renderSettings(w http.ResponseWriter, msg, kind string) {
	data := app.settingsData()
	data.Msg = msg
	data.Kind = kind
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("HX-Trigger", "settings-changed")
	app.render(w, "settings.html", data)
}
