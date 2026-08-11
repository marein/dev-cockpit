package telegram

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/local/dev-cockpit/internal/assistant"
)

const testToken = "123456789:AAtest-token-value-0123456789"

// botAPI plays the Bot API. Every test drives the channel against this and
// never against api.telegram.org.
type botAPI struct {
	server *httptest.Server

	mu sync.Mutex
	// pollStatus are refusals answered before the scripted updates, one per
	// poll, so a test can script "409, 409, then work".
	pollStatus []int
	// description is what a refusal says, used for the redaction test.
	description string
	// refuseFormatted answers every message that carries a parse_mode with the
	// 400 Telegram gives for markup it cannot parse.
	refuseFormatted bool
	polls           int
	sent            []sentMessage
	callbacks       []string
	files           []sentFile
	filePath        string
	fileBody        []byte
	getFileSize     int64

	updates chan []Update
}

// sentMessage is one message the bot posted, with how it was formatted and
// which buttons rode along.
type sentMessage struct {
	Text      string
	ParseMode string
	Buttons   []InlineButton
}

type sentFile struct {
	Method string
	Name   string
	Body   []byte
}

func newBotAPI(t *testing.T) *botAPI {
	t.Helper()
	api := &botAPI{updates: make(chan []Update, 16), filePath: "photos/file_1.jpg", fileBody: []byte("image-bytes")}
	api.server = httptest.NewServer(http.HandlerFunc(api.serve))
	t.Cleanup(api.server.Close)
	return api
}

func (a *botAPI) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/file/bot") {
		w.Write(a.fileBody)
		return
	}
	method := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]
	switch method {
	case "getUpdates":
		a.serveUpdates(w, r)
	case "sendMessage":
		var body struct {
			Text        string `json:"text"`
			ParseMode   string `json:"parse_mode"`
			ReplyMarkup struct {
				InlineKeyboard [][]InlineButton `json:"inline_keyboard"`
			} `json:"reply_markup"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		a.mu.Lock()
		refuse := a.refuseFormatted && body.ParseMode != ""
		if !refuse {
			var buttons []InlineButton
			for _, row := range body.ReplyMarkup.InlineKeyboard {
				buttons = append(buttons, row...)
			}
			a.sent = append(a.sent, sentMessage{Text: body.Text, ParseMode: body.ParseMode, Buttons: buttons})
		}
		a.mu.Unlock()
		if refuse {
			// What Telegram answers when it cannot parse the markup.
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": 400, "description": "Bad Request: can't parse entities"})
			return
		}
		writeResult(w, json.RawMessage(`{"message_id":1}`))
	case "answerCallbackQuery":
		var body struct {
			ID string `json:"callback_query_id"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		a.mu.Lock()
		a.callbacks = append(a.callbacks, body.ID)
		a.mu.Unlock()
		writeResult(w, json.RawMessage(`true`))
	case "sendPhoto", "sendDocument":
		a.serveUpload(w, r, method)
	case "getFile":
		a.mu.Lock()
		info := FileInfo{FilePath: a.filePath, FileSize: a.getFileSize}
		a.mu.Unlock()
		raw, _ := json.Marshal(info)
		writeResult(w, raw)
	default:
		writeResult(w, json.RawMessage(`{}`))
	}
}

func (a *botAPI) serveUpdates(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	a.polls++
	var status int
	if len(a.pollStatus) > 0 {
		status, a.pollStatus = a.pollStatus[0], a.pollStatus[1:]
	}
	description := a.description
	a.mu.Unlock()

	if status != 0 {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(map[string]any{"ok": false, "error_code": status, "description": description})
		return
	}
	select {
	case batch := <-a.updates:
		raw, _ := json.Marshal(batch)
		writeResult(w, raw)
	case <-r.Context().Done():
	// The real long poll waits thirty seconds. Here it comes back quickly, so
	// a test that pushes a batch after the start does not wait for it.
	case <-time.After(100 * time.Millisecond):
		writeResult(w, json.RawMessage(`[]`))
	}
}

func (a *botAPI) serveUpload(w http.ResponseWriter, r *http.Request, method string) {
	file := sentFile{Method: method}
	if err := r.ParseMultipartForm(8 << 20); err == nil {
		field := "document"
		if method == "sendPhoto" {
			field = "photo"
		}
		if uploaded, header, err := r.FormFile(field); err == nil {
			file.Name = header.Filename
			file.Body, _ = io.ReadAll(uploaded)
			uploaded.Close()
		}
	}
	a.mu.Lock()
	a.files = append(a.files, file)
	a.mu.Unlock()
	writeResult(w, json.RawMessage(`{"message_id":2}`))
}

func writeResult(w http.ResponseWriter, result json.RawMessage) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func (a *botAPI) push(updates ...Update) { a.updates <- updates }

func (a *botAPI) refuse(statuses ...int) {
	a.mu.Lock()
	a.pollStatus = append(a.pollStatus, statuses...)
	a.mu.Unlock()
}

// messages are the texts the bot posted, which is what most checks read.
func (a *botAPI) messages() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.sent))
	for _, m := range a.sent {
		out = append(out, m.Text)
	}
	return out
}

func (a *botAPI) posts() []sentMessage {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]sentMessage(nil), a.sent...)
}

// buttons are the ones under the last message that carried any.
func (a *botAPI) buttons() []InlineButton {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := len(a.sent) - 1; i >= 0; i-- {
		if len(a.sent[i].Buttons) > 0 {
			return a.sent[i].Buttons
		}
	}
	return nil
}

func (a *botAPI) answered() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]string(nil), a.callbacks...)
}

func (a *botAPI) uploads() []sentFile {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]sentFile(nil), a.files...)
}

func (a *botAPI) pollCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.polls
}

// fakeConversations stands in for the assistant's conversation service. It
// keeps the parts the channel touches: the live conversation, what was sent
// into it, and whether a turn is running.
type fakeConversations struct {
	mu        sync.Mutex
	live      assistant.Conversation
	hasLive   bool
	opened    []string
	sent      []sentPrompt
	running   bool
	uploadDir string
	answer    assistant.Message
	hasAnswer bool
	openErr   error
	sendErr   error
	created   int
	// coders stands in for the installed coders, nil meaning the two a normal
	// host has.
	coders []assistant.CoderInfo
	// onSend runs inside Send, which is how a test looks at the world at the
	// moment a message reached the conversation.
	onSend func()
}

type sentPrompt struct {
	Conversation string
	Text         string
	Attachments  []assistant.Attachment
	Source       string
}

func newFakeConversations(t *testing.T) *fakeConversations {
	t.Helper()
	return &fakeConversations{
		live:      assistant.Conversation{Summary: assistant.Summary{ID: "conv-1", CoderID: "claude", Status: assistant.StatusActive}},
		hasLive:   true,
		uploadDir: t.TempDir(),
	}
}

func (f *fakeConversations) Open(coderID string) (assistant.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.openErr != nil {
		return assistant.Conversation{}, f.openErr
	}
	f.opened = append(f.opened, coderID)
	// The real Open creates a new conversation when a coder is named and the
	// live one already carries messages, and hands back an untouched one
	// otherwise.
	if coderID != "" && len(f.live.Messages) > 0 {
		f.created++
		f.live = assistant.Conversation{Summary: assistant.Summary{
			ID:      "conv-" + string(rune('1'+f.created)),
			CoderID: coderID,
			Status:  assistant.StatusActive,
		}}
	}
	f.hasLive = true
	return f.live, nil
}

func (f *fakeConversations) Current() (assistant.Conversation, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live, f.hasLive
}

func (f *fakeConversations) Coders() []assistant.CoderInfo {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.coders == nil {
		return []assistant.CoderInfo{{ID: "claude", Label: "Claude"}, {ID: "copilot", Label: "Copilot"}}
	}
	return f.coders
}

func (f *fakeConversations) SendFrom(id, prompt string, attachments []assistant.Attachment, source string) (assistant.Run, error) {
	if f.onSend != nil {
		f.onSend()
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sendErr != nil {
		return assistant.Run{}, f.sendErr
	}
	f.sent = append(f.sent, sentPrompt{Conversation: id, Text: prompt, Attachments: attachments, Source: source})
	if id == f.live.ID {
		f.live.Messages = append(f.live.Messages, assistant.Message{
			ID:      "msg-" + string(rune('a'+len(f.live.Messages))),
			Role:    assistant.RoleUser,
			Content: prompt,
			State:   assistant.StateComplete,
		})
	}
	return assistant.Run{MessageID: "run"}, nil
}

func (f *fakeConversations) LastAnswer(string) (assistant.Message, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.answer, f.hasAnswer
}

func (f *fakeConversations) UploadDir(string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.uploadDir, nil
}

func (f *fakeConversations) Running(string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.running
}

func (f *fakeConversations) prompts() []sentPrompt {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]sentPrompt(nil), f.sent...)
}

func (f *fakeConversations) setAnswer(msg assistant.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.answer, f.hasAnswer = msg, true
}

func (f *fakeConversations) setLast(msg assistant.Message) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live.Messages = append(f.live.Messages, msg)
}

func (f *fakeConversations) createdCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.created
}

func (f *fakeConversations) setContext(usage *assistant.ContextUsage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live.Context = usage
}

func (f *fakeConversations) setRunning(running bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.running = running
}

// fakeFiles is the workspace side: the same boundary the real one draws, so
// the tests about what may not leave run against the real rule.
type fakeFiles struct {
	root string
}

func newFakeFiles(t *testing.T) *fakeFiles {
	t.Helper()
	root := t.TempDir()
	// The workspace itself may sit behind a symlink (/tmp on macOS), so the
	// tests compare against the resolved root like the real code does.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	return &fakeFiles{root: root}
}

// ResolveWorkspaceFile mirrors assistant.Workspace.ResolveWorkspaceFile: a
// path is joined onto the workspace, symlinks are resolved, and what does not
// land under the workspace afterwards is refused.
func (f *fakeFiles) ResolveWorkspaceFile(rel string) (string, error) {
	clean := strings.TrimSpace(rel)
	if clean == "" {
		return "", errors.New("A file is required.")
	}
	target := filepath.Join(f.root, filepath.FromSlash(clean))
	if !under(target, f.root) {
		return "", errors.New("Refusing to access a file outside the assistant workspace.")
	}
	real, err := filepath.EvalSymlinks(target)
	if err != nil {
		return "", errors.New("File not found.")
	}
	if !under(real, f.root) {
		return "", errors.New("Refusing to access a file outside the assistant workspace.")
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("File not found.")
	}
	return real, nil
}

func (f *fakeFiles) SaveUpload(dir, name string, src io.Reader) (assistant.Attachment, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return assistant.Attachment{}, err
	}
	path := filepath.Join(dir, name)
	handle, err := os.Create(path)
	if err != nil {
		return assistant.Attachment{}, err
	}
	defer handle.Close()
	size, err := io.Copy(handle, src)
	if err != nil {
		return assistant.Attachment{}, err
	}
	return assistant.Attachment{Name: name, Path: path, Media: assistant.MediaKind(name), Size: size}, nil
}

func (f *fakeFiles) write(t *testing.T, rel string, size int) string {
	t.Helper()
	path := filepath.Join(f.root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create dir: %v", err)
	}
	if err := os.WriteFile(path, make([]byte, size), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	return path
}

func under(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// testChannel is a channel wired against the stub, with a clock the test owns.
type testChannel struct {
	*Channel
	api      *botAPI
	convs    *fakeConversations
	files    *fakeFiles
	stateDir string
	clock    time.Time
}

func newTestChannel(t *testing.T) *testChannel {
	t.Helper()
	api := newBotAPI(t)
	convs := newFakeConversations(t)
	files := newFakeFiles(t)
	dir := t.TempDir()
	channel := New(dir, convs, files)
	channel.base = api.server.URL
	tc := &testChannel{Channel: channel, api: api, convs: convs, files: files, stateDir: dir, clock: time.Now()}
	channel.now = func() time.Time { return tc.clock }
	t.Cleanup(channel.Stop)
	return tc
}

// pair puts a token and a connected chat into the state file, the normal
// state of a channel that is set up.
func (tc *testChannel) pair(chatID int64) {
	tc.store.Update(func(s *State) {
		s.BotToken = testToken
		s.Enabled = true
		s.ChatID = chatID
		s.ChatName = "marein"
		s.PairedAt = tc.clock
	})
}

func (tc *testChannel) state() State { return tc.store.Load() }

// press builds a press on one of the bot's buttons, from the given chat.
func (tc *testChannel) press(updateID, chatID int64, data string) Update {
	return Update{
		UpdateID: updateID,
		CallbackQuery: &CallbackQuery{
			ID:   "cb-" + data,
			Data: data,
			Message: &Message{
				MessageID: updateID,
				Date:      tc.clock.Unix(),
				Chat:      Chat{ID: chatID, Type: "private", FirstName: "marein"},
			},
		},
	}
}

// message builds an update from the connected chat.
func (tc *testChannel) message(updateID int64, chatID int64, text string) Update {
	return Update{
		UpdateID: updateID,
		Message: &Message{
			MessageID: updateID,
			Date:      tc.clock.Unix(),
			Chat:      Chat{ID: chatID, Type: "private", FirstName: "marein"},
			Text:      text,
		},
	}
}

// waitFor polls a condition, the way a test waits on a goroutine that has no
// hook of its own.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
