// Package coach is the AI coach: an Anthropic Messages API client, the tools
// that let Claude read and write the user's data, and the tool-use loop that
// ties them together.
package coach

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/exercise"
	"github.com/jwald3/attherack/internal/store"
)

const maxTurns = 8 // safety cap on the tool-use loop

// Agent runs the Claude tool-use loop against the user's training data.
type Agent struct {
	apiKey string
	store  *store.Store
	lib    *exercise.Library
	http   *http.Client
	url    string // messages endpoint; "" means the real API
}

// New builds an agent. baseURL overrides the API host (for proxies and the
// end-to-end tests' fake API); "" means the real API.
func New(apiKey, baseURL string, st *store.Store, lib *exercise.Library) *Agent {
	base := strings.TrimRight(baseURL, "/")
	if base == "" {
		base = claudeBase
	}
	return &Agent{
		apiKey: apiKey,
		store:  st,
		lib:    lib,
		http:   &http.Client{Timeout: 5 * time.Minute},
		url:    base + "/v1/messages",
	}
}

// Chat runs a full turn: sends the conversation to Claude, executes any tool
// calls, and loops until Claude produces a final text answer. It returns the
// assistant's final text and whether the store was mutated (so the UI can
// know to refresh the log panel).
func (a *Agent) Chat(history []store.ChatMessage, userMsg string, images []store.ChatImage) (reply string, mutated bool, err error) {
	msgs := a.buildMessages(history, userMsg, images)

	sys := systemPrompt + "\n\nToday's date: " + dates.Today() +
		"\n\nRecent training snapshot:\n" + snapshot(a.store)

	for turn := 0; turn < maxTurns; turn++ {
		resp, err := a.call(apiRequest{
			Model:     claudeModel,
			MaxTokens: 4096,
			System:    sys,
			Messages:  msgs,
			Tools:     toolDefs(),
			Thinking:  map[string]any{"type": "adaptive"},
		})
		if err != nil {
			return "", mutated, err
		}

		// Append the assistant turn verbatim so tool_use blocks are preserved.
		msgs = append(msgs, apiMessage{Role: "assistant", Content: resp.Content})

		if resp.StopReason != "tool_use" {
			return collectText(resp.Content), mutated, nil
		}

		// Execute every tool_use block and gather results into one user turn.
		var results []contentPart
		for _, part := range resp.Content {
			if part.Type != "tool_use" {
				continue
			}
			out, didMutate, terr := a.runTool(part.Name, part.Input)
			if didMutate {
				mutated = true
			}
			res := contentPart{Type: "tool_result", ToolUseID: part.ID, Content: out}
			if terr != nil {
				res.IsError = true
				res.Content = "error: " + terr.Error()
			}
			results = append(results, res)
		}
		msgs = append(msgs, apiMessage{Role: "user", Content: results})
	}
	return "I got stuck working through that — try rephrasing?", mutated, nil
}

// runTool dispatches a tool call by name and returns a text result.
func (a *Agent) runTool(name string, input json.RawMessage) (result string, mutated bool, err error) {
	t, ok := toolsByName[name]
	if !ok {
		return "", false, errUnknownTool(name)
	}
	return t.run(a, input)
}

// buildMessages converts stored chat history plus the new user turn into API
// messages. User turns carry their attached photos as image blocks ahead of
// the text; earlier photos are reloaded from the store so follow-up questions
// ("what about the machine next to it?") still have the picture in context.
func (a *Agent) buildMessages(history []store.ChatMessage, userMsg string, images []store.ChatImage) []apiMessage {
	msgs := make([]apiMessage, 0, len(history)+1)
	for _, m := range history {
		// Skip placeholder replies still being generated (or failed): they have
		// no usable text and the API rejects empty assistant turns.
		if m.Role == "assistant" && (m.Pending() || strings.TrimSpace(m.Content) == "") {
			continue
		}
		var imgs []store.ChatImage
		for _, ref := range m.Images {
			if ref.Data != nil {
				imgs = append(imgs, ref)
			} else if a.store != nil {
				if full, ok := a.store.GetChatImage(ref.ID); ok {
					imgs = append(imgs, full)
				}
			}
		}
		parts := userParts(m.Content, imgs)
		if len(parts) == 0 {
			continue
		}
		msgs = append(msgs, apiMessage{Role: m.Role, Content: parts})
	}
	msgs = append(msgs, apiMessage{Role: "user", Content: userParts(userMsg, images)})
	return msgs
}

// userParts lays out a turn as [images..., text]. The API rejects empty text
// blocks, so a photo-only message carries a short stand-in caption.
func userParts(text string, images []store.ChatImage) []contentPart {
	parts := make([]contentPart, 0, len(images)+1)
	for _, img := range images {
		parts = append(parts, imagePart(img))
	}
	text = strings.TrimSpace(text)
	if text == "" && len(images) > 0 {
		text = "(photo attached, no caption)"
	}
	if text != "" {
		parts = append(parts, contentPart{Type: "text", Text: text})
	}
	return parts
}
