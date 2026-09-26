package server

import (
	"errors"
	"html"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/jwald3/attherack/internal/coach"
	"github.com/jwald3/attherack/internal/dates"
	"github.com/jwald3/attherack/internal/markdown"
	"github.com/jwald3/attherack/internal/store"
)

// --- Coach tab (home): conversations ---

// coachData is the model for the coach chat page.
type coachData struct {
	PageTitle   string
	Active      string
	Today       string
	ChatEnabled bool
	Settings    settingsData
	Threads     []store.ChatThread
	Thread      *store.ChatThread // nil = new, unsaved conversation
	Messages    []store.ChatMessage
}

// threadListData renders the sidebar list with the active thread highlighted.
type threadListData struct {
	Threads  []store.ChatThread
	ActiveID int64
	OOB      bool // render as an htmx out-of-band swap
}

func (d coachData) ThreadList() threadListData {
	var id int64
	if d.Thread != nil {
		id = d.Thread.ID
	}
	return threadListData{Threads: d.Threads, ActiveID: id}
}

func (app *App) renderCoach(w http.ResponseWriter, thread *store.ChatThread) {
	threads, _ := app.store.ListThreads(200)
	data := coachData{
		PageTitle:   "Coach",
		Active:      "coach",
		Today:       dates.Today(),
		ChatEnabled: app.chatEnabled(),
		Settings:    app.settingsData(),
		Threads:     threads,
		Thread:      thread,
	}
	if thread != nil {
		data.PageTitle = thread.Title
		data.Messages, _ = app.store.ListChatMessages(thread.ID, 500)
	}
	app.render(w, "coach.html", data)
}

// handleCoachHome shows a fresh conversation (the thread is created on first send).
func (app *App) handleCoachHome(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	app.renderCoach(w, nil)
}

// handleCoachThread shows an existing conversation.
func (app *App) handleCoachThread(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	t, ok := app.store.GetThread(id)
	if !ok {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	app.renderCoach(w, &t)
}

func (app *App) writeThreadList(w http.ResponseWriter, activeID int64, oob bool) {
	threads, _ := app.store.ListThreads(200)
	app.render(w, "thread_list.html", threadListData{Threads: threads, ActiveID: activeID, OOB: oob})
}

// handleDeleteThread removes a conversation. Deleting the one on screen sends
// the browser back to a new chat; otherwise just the sidebar is refreshed.
func (app *App) handleDeleteThread(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		badID(w)
		return
	}
	if err := app.store.DeleteThread(id); err != nil {
		serverError(w, err)
		return
	}
	current, _ := strconv.ParseInt(r.FormValue("current"), 10, 64)
	if current == id {
		w.Header().Set("HX-Redirect", "/")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	app.writeThreadList(w, current, false)
}

// handleRenameThread sets a conversation's title.
func (app *App) handleRenameThread(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		badID(w)
		return
	}
	if title := strings.TrimSpace(r.FormValue("title")); title != "" {
		if err := app.store.RenameThread(id, truncateTitle(title, 80)); err != nil {
			serverError(w, err)
			return
		}
	}
	current, _ := strconv.ParseInt(r.FormValue("current"), 10, 64)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	app.writeThreadList(w, current, false)
}

// truncateTitle shortens s to at most n runes, cutting at a word boundary.
func truncateTitle(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	cut := string(r[:n])
	if i := strings.LastIndex(cut, " "); i > n/2 {
		cut = cut[:i]
	}
	return cut + "…"
}

// --- Chat: sending a message and receiving the reply ---

func (app *App) handleChat(w http.ResponseWriter, r *http.Request) {
	agent := app.getAgent()
	if agent == nil {
		writeChatBubble(w, "assistant", "Chat is disabled. Add your Anthropic API key (API key button, bottom left) to enable Claude.", false)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxChatFormBytes)
	if err := r.ParseMultipartForm(maxChatFormBytes); err != nil {
		// Plain (non-multipart) posts still work, e.g. from tests or curl.
		if !errors.Is(err, http.ErrNotMultipart) {
			http.Error(w, "That upload is too large. Try fewer or smaller photos.", http.StatusRequestEntityTooLarge)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	}
	userMsg := strings.TrimSpace(r.FormValue("message"))
	images, err := readChatImages(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	if userMsg == "" && len(images) == 0 {
		return
	}

	// Resolve the thread, creating one on the first message of a new chat.
	threadID, _ := strconv.ParseInt(r.FormValue("thread_id"), 10, 64)
	isNew := false
	if _, ok := app.store.GetThread(threadID); !ok {
		title := truncateTitle(userMsg, 40)
		if title == "" {
			title = "Photo"
		}
		id, err := app.store.CreateThread(title)
		if err != nil {
			serverError(w, err)
			return
		}
		threadID, isNew = id, true
	}

	history, _ := app.store.ListChatMessages(threadID, 40)
	msgID, err := app.store.AddChatMessageWithImages(threadID, "user", userMsg, images)
	if err != nil {
		serverError(w, err)
		return
	}
	saved, _ := app.store.GetChatMessage(msgID)

	// Insert a pending assistant reply and generate it in the background, so the
	// answer completes and is saved even if the user navigates away or reloads.
	// The UI polls /chat/msg/{id} until it flips to done/error.
	pendingID, err := app.store.AddPendingAssistant(threadID)
	if err != nil {
		serverError(w, err)
		return
	}
	go app.generateReply(agent, threadID, pendingID, history, userMsg, images, isNew)

	if isNew {
		// Put the new thread in the URL so reload/back work like a normal page.
		w.Header().Set("HX-Push-Url", "/c/"+strconv.FormatInt(threadID, 10))
	}
	// The user bubble was shown optimistically client-side; re-render it
	// authoritatively, then a polling placeholder for the reply. Refresh the
	// sidebar and thread id out-of-band so follow-ups land in the same thread.
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	writeUserBubble(w, saved)
	writePendingBubble(w, pendingID)
	w.Write([]byte(`<input type="hidden" name="thread_id" id="thread-id" value="` + strconv.FormatInt(threadID, 10) + `" hx-swap-oob="true">`))
	app.writeThreadList(w, threadID, true)
}

// generateReply runs the coach's tool-use loop off the request path and writes
// the result into the pending assistant row. It deliberately uses no request
// context, so a client disconnect (tab switch, reload, close) never cancels it.
func (app *App) generateReply(agent *coach.Agent, threadID, pendingID int64, history []store.ChatMessage, userMsg string, images []store.ChatImage, isNew bool) {
	reply, mutated, err := agent.Chat(history, userMsg, images)
	status := store.StatusDone
	if err != nil {
		log.Printf("chat: %v", err)
		reply = "Something went wrong talking to Claude: " + err.Error()
		status = store.StatusError
	}
	if e := app.store.FinishChatMessage(pendingID, reply, status, mutated); e != nil {
		log.Printf("chat: save reply: %v", e)
	}
	if isNew && status == store.StatusDone {
		if userMsg == "" {
			userMsg = "(sent a photo)"
		}
		if title := agent.TitleFor(userMsg, reply); title != "" {
			_ = app.store.RenameThread(threadID, title)
		}
	}
}

// handleChatMessage is the poll endpoint for a single assistant reply. While the
// reply is pending it returns the same placeholder (which keeps polling); once
// it's done or errored it returns the final bubble with no poll trigger, so
// polling stops, plus an out-of-band sidebar refresh to pick up a new title.
func (app *App) handleChatMessage(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	m, ok := app.store.GetChatMessage(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if m.Pending() {
		writePendingBubble(w, id)
		return
	}
	writeChatBubble(w, "assistant", m.Content, m.Mutated)
	// The thread may have just been auto-titled; refresh the sidebar so it shows.
	app.writeThreadList(w, m.ThreadID, true)
}

// handleChatImage serves an attached photo for display in the conversation.
func (app *App) handleChatImage(w http.ResponseWriter, r *http.Request) {
	id, _ := pathID(r)
	img, ok := app.store.GetChatImage(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	writeImage(w, img.MediaType, img.Data)
}

// --- Chat bubbles ---

// writeChatBubble emits a single chat message bubble. Assistant messages are
// rendered as Markdown; user messages stay plain text. If mutated is true, it
// includes an HTMX trigger that refreshes the workout log panel.
func writeChatBubble(w http.ResponseWriter, role, content string, mutated bool) {
	var body string
	if role == "assistant" {
		body = string(markdown.Render(content))
	} else {
		body = strings.ReplaceAll(html.EscapeString(content), "\n", "<br>")
	}
	trigger := ""
	if mutated {
		// This attribute nudges the log panel to reload via HTMX.
		trigger = ` data-refresh-log="1"`
	}
	writeHTML(w, `<div class="bubble `+role+` md"`+trigger+`>`+body+`</div>`)
}

// writeUserBubble renders a stored user message, thumbnails first.
func writeUserBubble(w http.ResponseWriter, m store.ChatMessage) {
	body := imagesHTML(m.Images) + strings.ReplaceAll(html.EscapeString(m.Content), "\n", "<br>")
	writeHTML(w, `<div class="bubble user md">`+body+`</div>`)
}

// imagesHTML renders the thumbnail strip for a message's attached photos.
func imagesHTML(imgs []store.ChatImage) string {
	if len(imgs) == 0 {
		return ""
	}
	var sb strings.Builder
	sb.WriteString(`<div class="bubble-images">`)
	for _, img := range imgs {
		src := "/chat/img/" + strconv.FormatInt(img.ID, 10)
		sb.WriteString(`<a href="` + src + `" target="_blank" rel="noopener"><img src="` + src + `" alt="Attached photo" loading="lazy"></a>`)
	}
	sb.WriteString(`</div>`)
	return sb.String()
}

// writePendingBubble emits the "thinking" placeholder for an in-flight reply.
// It polls GET /chat/msg/{id} every 1.5s and replaces itself with the result;
// because it re-renders from the database, it resumes automatically after a
// reload or when the user returns to the tab. The id lets the poller find it.
func writePendingBubble(w http.ResponseWriter, id int64) {
	sid := strconv.FormatInt(id, 10)
	w.Write([]byte(`<div class="bubble assistant pending" ` +
		`hx-get="/chat/msg/` + sid + `" hx-trigger="load delay:1500ms" ` +
		`hx-swap="outerHTML" hx-target="this">` +
		`<span class="typing"><i></i><i></i><i></i></span></div>`))
}
