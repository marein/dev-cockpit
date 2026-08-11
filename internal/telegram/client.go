package telegram

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// apiBase is the Bot API root. The token sits in the path behind it, which is
// why nothing built from these URLs may be logged unredacted.
const apiBase = "https://api.telegram.org"

const (
	// pollTimeout is the long poll Telegram holds open when nothing arrives.
	pollTimeout = 30
	// pollDeadline gives that long poll room to come back before the client
	// gives up on it.
	pollDeadline = time.Duration(pollTimeout+15) * time.Second
	// callDeadline bounds the short calls: sending, getFile, and the rest.
	callDeadline = 30 * time.Second
	// downloadDeadline bounds fetching one file from the Bot API.
	downloadDeadline = 2 * time.Minute
	// maxAPIResponse bounds a JSON answer, so a broken proxy cannot feed the
	// process a stream instead of a reply.
	maxAPIResponse = 4 << 20
	// MaxDownloadBytes is where the Bot API stops handing out files.
	MaxDownloadBytes = 20 << 20
	// maxPhotoBytes and maxDocumentBytes are the outbound limits of the Bot API.
	maxPhotoBytes    = 10 << 20
	maxDocumentBytes = 50 << 20
)

// apiHTTPClient is the outbound client of this channel, built like the push
// client: bounded, never following redirects, refusing link local destinations
// at dial time after DNS resolution. The timeout is the long poll plus room,
// every call carries a context deadline of its own on top.
var apiHTTPClient = &http.Client{
	Timeout: pollDeadline + 30*time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
	Transport: &http.Transport{
		DialContext: (&net.Dialer{
			Timeout: 10 * time.Second,
			Control: refuseLinkLocal,
		}).DialContext,
	},
}

func refuseLinkLocal(network, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return err
	}
	if addr.IsLinkLocalUnicast() || addr.IsLinkLocalMulticast() {
		return fmt.Errorf("link local address %s refused", addr)
	}
	return nil
}

// APIError is what Telegram answered when it refused. The code is what the
// poller decides on: 401 is a token that will not start working by itself,
// everything else, 409 included, is treated as weather.
type APIError struct {
	Method      string
	Code        int
	Description string
}

func (e *APIError) Error() string {
	if e.Description == "" {
		return fmt.Sprintf("telegram %s answered %d", e.Method, e.Code)
	}
	return fmt.Sprintf("telegram %s answered %d: %s", e.Method, e.Code, e.Description)
}

// apiCode is the status Telegram refused with, or zero for anything that never
// reached it.
func apiCode(err error) int {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return 0
}

// client speaks the Bot API for one token.
type client struct {
	base  string
	token string
}

func (c *Channel) client(token string) *client {
	return &client{base: c.base, token: token}
}

func (c *client) methodURL(method string) string {
	return c.base + "/bot" + c.token + "/" + method
}

func (c *client) fileURL(path string) string {
	return c.base + "/file/bot" + c.token + "/" + path
}

// redact takes the token out of a string. Everything this package logs, shows
// or returns goes through it, because the token stands in every URL and a
// transport error carries the URL it failed on.
func (c *client) redact(s string) string {
	if c.token == "" {
		return s
	}
	return strings.ReplaceAll(s, c.token, "<token>")
}

// fail is the one way an error leaves this client.
func (c *client) fail(method string, err error) error {
	return errors.New(c.redact(fmt.Sprintf("telegram %s: %v", method, err)))
}

// Update is one entry of getUpdates. Only messages and the presses on the
// bot's own buttons are asked for, so nothing the bot itself sent ever comes
// back here as a message.
type Update struct {
	UpdateID      int64          `json:"update_id"`
	Message       *Message       `json:"message"`
	CallbackQuery *CallbackQuery `json:"callback_query"`
}

// CallbackQuery is a press on one of the bot's inline buttons. It carries the
// message the buttons sit under, which is where the chat check reads the chat
// from: a press is looked at exactly like a message.
type CallbackQuery struct {
	ID      string   `json:"id"`
	Data    string   `json:"data"`
	Message *Message `json:"message"`
}

// Chat is the chat a press came from, empty when Telegram sent no message with
// it, which is a press this channel cannot place and therefore drops.
func (q *CallbackQuery) Chat() (Chat, bool) {
	if q == nil || q.Message == nil {
		return Chat{}, false
	}
	return q.Message.Chat, true
}

// InlineButton is one button under a message. The data is what comes back on a
// press and is bounded by Telegram to 64 bytes.
type InlineButton struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

// Message is the part of a Telegram message this channel reads.
type Message struct {
	MessageID int64  `json:"message_id"`
	Date      int64  `json:"date"`
	Chat      Chat   `json:"chat"`
	Text      string `json:"text"`
	Caption   string `json:"caption"`
	// Photo carries one entry per offered size, smallest first.
	Photo    []PhotoSize `json:"photo"`
	Document *Document   `json:"document"`
}

// Chat is the conversation a message arrived in. The id is an int64 and
// negative for groups, so nothing here may narrow it.
type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	Username  string `json:"username"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
}

// Name is what the settings page shows next to the chat id.
func (c Chat) Name() string {
	name := strings.TrimSpace(strings.TrimSpace(c.FirstName) + " " + strings.TrimSpace(c.LastName))
	if name == "" {
		name = strings.TrimSpace(c.Title)
	}
	if name == "" {
		name = strings.TrimSpace(c.Username)
	}
	if name == "" {
		name = "Chat " + strconv.FormatInt(c.ID, 10)
	}
	return name
}

// PhotoSize is one offered size of a photo.
type PhotoSize struct {
	FileID   string `json:"file_id"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FileSize int64  `json:"file_size"`
}

// Document is a file sent as a file, which is what a phone does when the user
// picks "send as file" for a picture.
type Document struct {
	FileID   string `json:"file_id"`
	FileName string `json:"file_name"`
	MimeType string `json:"mime_type"`
	FileSize int64  `json:"file_size"`
}

// FileInfo is what getFile answers: the path the download uses.
type FileInfo struct {
	FileID   string `json:"file_id"`
	FileSize int64  `json:"file_size"`
	FilePath string `json:"file_path"`
}

// call posts one Bot API method and unmarshals its result.
func (c *client) call(ctx context.Context, method string, body, out any) error {
	payload, err := json.Marshal(body)
	if err != nil {
		return c.fail(method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(method), bytes.NewReader(payload))
	if err != nil {
		return c.fail(method, err)
	}
	req.Header.Set("Content-Type", "application/json")
	return c.do(req, method, out)
}

func (c *client) do(req *http.Request, method string, out any) error {
	resp, err := apiHTTPClient.Do(req)
	if err != nil {
		return c.fail(method, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAPIResponse))
	if err != nil {
		return c.fail(method, err)
	}
	var envelope struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		ErrorCode   int             `json:"error_code"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil {
		// Something that is not the Bot API answered, a proxy or a captive
		// portal. The body is not ours to repeat, only the status is.
		return &APIError{Method: method, Code: resp.StatusCode}
	}
	if !envelope.OK {
		code := envelope.ErrorCode
		if code == 0 {
			code = resp.StatusCode
		}
		return &APIError{Method: method, Code: code, Description: c.redact(envelope.Description)}
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Result, out); err != nil {
		return c.fail(method, err)
	}
	return nil
}

// getUpdates is the long poll. Only messages are asked for, so an edit, a
// reaction or the bot's own posts never wake this channel.
func (c *client) getUpdates(ctx context.Context, offset int64, timeout int) ([]Update, error) {
	ctx, cancel := context.WithTimeout(ctx, pollDeadline)
	defer cancel()
	body := map[string]any{
		"timeout": timeout,
		// Messages and the presses on the bot's own buttons, nothing else. What
		// the bot itself sends never comes back, so the assistant cannot end up
		// answering itself.
		"allowed_updates": []string{"message", "callback_query"},
	}
	if offset > 0 {
		body["offset"] = offset
	}
	var updates []Update
	if err := c.call(ctx, "getUpdates", body, &updates); err != nil {
		return nil, err
	}
	return updates, nil
}

// sendMessage posts one message. An empty parseMode is plain text, which is
// what the bot's own lines are and what a refused formatted message falls back
// to; "HTML" is the subset an answer is translated into.
func (c *client) sendMessage(ctx context.Context, chatID int64, text, parseMode string) error {
	ctx, cancel := context.WithTimeout(ctx, callDeadline)
	defer cancel()
	body := map[string]any{
		"chat_id": chatID,
		"text":    text,
	}
	if parseMode != "" {
		body["parse_mode"] = parseMode
	}
	return c.call(ctx, "sendMessage", body, nil)
}

// sendButtons posts a message with one inline button per row, the shape a
// question with a fixed set of answers takes in a chat.
func (c *client) sendButtons(ctx context.Context, chatID int64, text string, buttons []InlineButton) error {
	ctx, cancel := context.WithTimeout(ctx, callDeadline)
	defer cancel()
	rows := make([][]InlineButton, 0, len(buttons))
	for _, button := range buttons {
		rows = append(rows, []InlineButton{button})
	}
	return c.call(ctx, "sendMessage", map[string]any{
		"chat_id":      chatID,
		"text":         text,
		"reply_markup": map[string]any{"inline_keyboard": rows},
	}, nil)
}

// answerCallback closes the press. Without it the button keeps spinning in the
// chat, which reads as a bot that did not hear.
func (c *client) answerCallback(ctx context.Context, callbackID string) error {
	ctx, cancel := context.WithTimeout(ctx, callDeadline)
	defer cancel()
	return c.call(ctx, "answerCallbackQuery", map[string]any{"callback_query_id": callbackID}, nil)
}

// sendFile uploads one file, as a photo or as a document.
func (c *client) sendFile(ctx context.Context, method, field string, chatID int64, name string, src io.Reader) error {
	ctx, cancel := context.WithTimeout(ctx, downloadDeadline)
	defer cancel()

	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	if err := form.WriteField("chat_id", strconv.FormatInt(chatID, 10)); err != nil {
		return c.fail(method, err)
	}
	part, err := form.CreateFormFile(field, name)
	if err != nil {
		return c.fail(method, err)
	}
	if _, err := io.Copy(part, src); err != nil {
		return c.fail(method, err)
	}
	if err := form.Close(); err != nil {
		return c.fail(method, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.methodURL(method), bytes.NewReader(buf.Bytes()))
	if err != nil {
		return c.fail(method, err)
	}
	req.Header.Set("Content-Type", form.FormDataContentType())
	return c.do(req, method, nil)
}

// getFile asks where a file the bot was sent can be downloaded.
func (c *client) getFile(ctx context.Context, fileID string) (FileInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, callDeadline)
	defer cancel()
	var info FileInfo
	if err := c.call(ctx, "getFile", map[string]any{"file_id": fileID}, &info); err != nil {
		return FileInfo{}, err
	}
	return info, nil
}

// download fetches one file by the path getFile answered. The caller closes
// the body and bounds what it reads.
func (c *client) download(ctx context.Context, filePath string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.fileURL(filePath), nil)
	if err != nil {
		return nil, c.fail("download", err)
	}
	resp, err := apiHTTPClient.Do(req)
	if err != nil {
		return nil, c.fail("download", err)
	}
	if resp.StatusCode >= 300 {
		resp.Body.Close()
		return nil, &APIError{Method: "download", Code: resp.StatusCode}
	}
	return resp.Body, nil
}
