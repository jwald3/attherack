package coach

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/jwald3/attherack/internal/store"
)

const (
	claudeBase  = "https://api.anthropic.com"
	claudeURL   = claudeBase + "/v1/messages"
	claudeModel = "claude-opus-4-8"
	haikuModel  = "claude-haiku-4-5" // cheap model for quick field inference
	apiVersion  = "2023-06-01"
)

// --- Anthropic wire types (only the fields we use) ---

type apiMessage struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

// contentPart is a union over text / thinking / tool_use / tool_result blocks.
type contentPart struct {
	Type string `json:"type"`

	// text
	Text string `json:"text,omitempty"`

	// thinking (adaptive thinking) — must be echoed back verbatim, including
	// the signature, or the API rejects the next turn.
	Thinking  string `json:"thinking,omitempty"`
	Signature string `json:"signature,omitempty"`
	Data      string `json:"data,omitempty"` // redacted_thinking payload

	// tool_use
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`

	// tool_result
	ToolUseID string `json:"tool_use_id,omitempty"`
	Content   string `json:"content,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`

	// image (user-attached photo, sent inline as base64)
	Source *imageSource `json:"source,omitempty"`
}

// imageSource is the base64 payload of an image content block.
type imageSource struct {
	Type      string `json:"type"` // always "base64"
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

// imagePart builds an image content block from stored bytes.
func imagePart(img store.ChatImage) contentPart {
	return contentPart{Type: "image", Source: &imageSource{
		Type:      "base64",
		MediaType: img.MediaType,
		Data:      base64.StdEncoding.EncodeToString(img.Data),
	}}
}

// MarshalJSON emits only the fields valid for each block type. This matters for
// thinking blocks: the API requires the `thinking` field to be present (even
// when empty under display:"omitted"), while text blocks must not carry it.
func (c contentPart) MarshalJSON() ([]byte, error) {
	m := map[string]any{"type": c.Type}
	switch c.Type {
	case "thinking":
		m["thinking"] = c.Thinking // always present, even if ""
		if c.Signature != "" {
			m["signature"] = c.Signature
		}
	case "redacted_thinking":
		m["data"] = c.Data
	case "tool_use":
		m["id"] = c.ID
		m["name"] = c.Name
		if c.Input != nil {
			m["input"] = c.Input
		} else {
			m["input"] = map[string]any{}
		}
	case "tool_result":
		m["tool_use_id"] = c.ToolUseID
		m["content"] = c.Content
		if c.IsError {
			m["is_error"] = true
		}
	case "image":
		m["source"] = c.Source
	default: // "text" and anything else
		m["text"] = c.Text
	}
	return json.Marshal(m)
}

type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

type apiRequest struct {
	Model        string         `json:"model"`
	MaxTokens    int            `json:"max_tokens"`
	System       string         `json:"system,omitempty"`
	Messages     []apiMessage   `json:"messages"`
	Tools        []toolDef      `json:"tools,omitempty"`
	Thinking     map[string]any `json:"thinking,omitempty"`
	OutputConfig map[string]any `json:"output_config,omitempty"`
}

type apiResponse struct {
	Content    []contentPart `json:"content"`
	StopReason string        `json:"stop_reason"`
	Error      *struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// apiError is a non-200 reply from the Messages API.
type apiError struct {
	StatusCode int
	Message    string // the API's own error message, if the body parsed
	Body       string // raw body, for when it didn't
}

func (e *apiError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("claude api (%d): %s", e.StatusCode, e.Message)
	}
	return fmt.Sprintf("claude api returned %d: %s", e.StatusCode, truncate(e.Body, 300))
}

// endpoint is the URL call sites use; Agents built without New (tests) fall
// back to the real API.
func (a *Agent) endpoint() string {
	if a.url == "" {
		return claudeURL
	}
	return a.url
}

// call sends one Messages request. Non-200 replies come back as *apiError.
func (a *Agent) call(req apiRequest) (*apiResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequest("POST", a.endpoint(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", a.apiKey)
	httpReq.Header.Set("anthropic-version", apiVersion)

	resp, err := a.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		// Surface the API's own error message when we can parse it.
		apiErr := &apiError{StatusCode: resp.StatusCode, Body: string(raw)}
		var parsed apiResponse
		if json.Unmarshal(raw, &parsed) == nil && parsed.Error != nil {
			apiErr.Message = parsed.Error.Message
		}
		return nil, apiErr
	}
	var out apiResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decoding claude response: %w", err)
	}
	return &out, nil
}

// collectText joins a reply's text blocks.
func collectText(parts []contentPart) string {
	var sb strings.Builder
	for _, p := range parts {
		if p.Type == "text" {
			sb.WriteString(p.Text)
		}
	}
	s := strings.TrimSpace(sb.String())
	if s == "" {
		return "(no response)"
	}
	return s
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
