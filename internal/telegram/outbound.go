package telegram

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/assistant"
	"github.com/local/dev-cockpit/internal/markdown"
)

const (
	// sendAttempts is one try plus two repeats. Telegram is the convenience,
	// not the record: the browser holds the answer either way, so a delivery
	// that keeps failing is dropped and logged instead of queued on disk.
	sendAttempts = 3
	retryDelay   = 2 * time.Second
	// maxOutgoingFiles bounds what one answer may send along.
	maxOutgoingFiles = 5
)

// Notify is the outbound side of the assistant's onDone hook: a finished
// answer and every job report leave through here. It carries its own deadline,
// because it runs on a goroutine of a turn that is already over.
func (c *Channel) Notify(conversationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), deliverTimeout)
	defer cancel()
	c.deliver(ctx, conversationID)
}

func (c *Channel) deliver(ctx context.Context, conversationID string) {
	st := c.store.Load()
	if st.BotToken == "" || !st.Enabled || st.ChatID == 0 {
		return
	}
	answer, ok := c.conversations.LastAnswer(conversationID)
	if !ok {
		return
	}
	// The two choices sit here and nowhere else, right in front of the sending.
	// The conversation already holds this message, and the browser shows it
	// whichever way these stand: one conversation, and this is a window onto it.
	if !carries(st, answer) {
		return
	}
	cl := c.client(st.BotToken)
	if answer.State != assistant.StateComplete {
		// A half written answer is not sent. What it says is worth one line,
		// not the fragment itself.
		_ = c.send(ctx, cl, st.ChatID, unfinishedLine(answer.State))
		return
	}

	text, refs := extractFiles(answer.Content)
	files, notes := c.resolveFiles(refs)
	body := strings.TrimSpace(header(answer.Wake) + strings.TrimSpace(text))
	if len(notes) > 0 {
		body = strings.TrimSpace(body + "\n\n" + strings.Join(notes, "\n"))
	}
	// The text goes first, the files follow it.
	for _, part := range splitMessage(body) {
		if err := c.sendFormatted(ctx, cl, st.ChatID, part); err != nil {
			return
		}
	}
	for _, file := range files {
		c.sendFile(ctx, cl, st.ChatID, file)
	}
}

// carries decides whether this message leaves through the channel. An answer
// goes by where its question came from, a job report by where its job was
// asked for; both are written on the message itself, so nothing is looked up
// long after the job may be gone.
func carries(st State, answer assistant.Message) bool {
	if answer.Wake != nil {
		return st.Reports != DeliveryTelegram || answer.Wake.Source == assistant.SourceTelegram
	}
	return st.Answers != DeliveryTelegram || answer.Source == assistant.SourceTelegram
}

// post sends and repeats a failure twice. A refusal is not repeated: Telegram
// does not accept on the third try what it refused twice, and the caller has a
// second way to send the same thing.
func post(ctx context.Context, send func() error) error {
	var err error
	for attempt := 0; attempt < sendAttempts; attempt++ {
		if attempt > 0 && !sleepCtx(ctx, retryDelay) {
			break
		}
		if err = send(); err == nil {
			return nil
		}
		if apiCode(err) == http.StatusBadRequest {
			break
		}
	}
	return err
}

// send posts one plain message. The bot's own lines go this way: they are
// sentences, not Markdown, and they must not depend on a translator.
func (c *Channel) send(ctx context.Context, cl *client, chatID int64, text string) error {
	err := post(ctx, func() error { return cl.sendMessage(ctx, chatID, text, "") })
	if err != nil {
		log.Printf("telegram: sending a message failed, dropping it: %v", err)
	}
	return err
}

// sendFormatted posts what the assistant wrote, translated into Telegram's
// HTML subset. The fallback is the point: a 400 on the formatted message sends
// the same text again, unformatted. A bug in the translator may cost the
// markup, never the answer.
func (c *Channel) sendFormatted(ctx context.Context, cl *client, chatID int64, text string) error {
	err := post(ctx, func() error {
		return cl.sendMessage(ctx, chatID, markdown.RenderTelegramHTML(text), "HTML")
	})
	if err == nil {
		return nil
	}
	if apiCode(err) != http.StatusBadRequest {
		log.Printf("telegram: sending a message failed, dropping it: %v", err)
		return err
	}
	// Logged, because this is what says the translator needs work.
	log.Printf("telegram: the formatted message was refused, sending it as plain text: %v", err)
	return c.send(ctx, cl, chatID, text)
}

// unfinishedLine says what happened to a turn that produced no answer.
func unfinishedLine(state assistant.State) string {
	switch state {
	case assistant.StateCancelled:
		return "The last turn was stopped, so there is no answer to send."
	case assistant.StateFailed:
		return "The last turn failed, so there is no answer to send."
	}
	return "The last turn did not finish, so there is no answer to send."
}

// header names a job report for what it is. Without it a report about some
// coder reads like the answer to the last question asked here.
func header(wake *assistant.WakeNote) string {
	if wake == nil {
		return ""
	}
	title := "Job report."
	switch assistant.Verdict(wake.Verdict) {
	case assistant.VerdictDone:
		title = "Job done."
	case assistant.VerdictBlocked:
		title = "Job blocked."
	case assistant.VerdictExpired:
		title = "Job expired."
	}
	name := strings.TrimSpace(wake.Name)
	if name == "" {
		name = strings.TrimSpace(wake.Terminal)
	}
	// Markdown, like everything else on this path: the translator makes the
	// verdict bold, so the report below it reads as the report.
	line := "**" + title + "**"
	if name != "" {
		line += fmt.Sprintf(" %q", name)
		if project := strings.TrimSpace(wake.Project); project != "" {
			line += " - " + project
		}
	}
	return line + "\n\n"
}

// fileLine is how an answer says that a file belongs to it. One line of its
// own, nothing guessed: what leaves this machine is written out.
var fileLine = regexp.MustCompile(`(?m)^[ \t]*\[\[[ \t]*datei:[ \t]*([^\]\n]+?)[ \t]*\]\][ \t]*$`)

// blankRun closes the gap a removed file line leaves behind.
var blankRun = regexp.MustCompile(`\n{3,}`)

// extractFiles takes the file lines out of an answer and returns the text
// without them plus what they named, in order.
func extractFiles(content string) (string, []string) {
	matches := fileLine.FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		return content, nil
	}
	refs := make([]string, 0, len(matches))
	for _, match := range matches {
		if ref := strings.TrimSpace(match[1]); ref != "" {
			refs = append(refs, ref)
		}
	}
	// The removed lines leave their blank lines behind, which would show up as
	// a gap in the middle of an answer.
	text := blankRun.ReplaceAllString(fileLine.ReplaceAllString(content, ""), "\n\n")
	return strings.TrimSpace(text), refs
}

// outgoing is one file an answer sends along.
type outgoing struct {
	Path  string
	Name  string
	Photo bool
}

// resolveFiles turns what an answer named into files that may leave, and into
// the lines about the ones that may not.
//
// The boundary is the assistant's workspace and it is the point of the whole
// thing: a path is resolved inside it, symlinks are followed, and what does
// not land under the workspace afterwards is dropped. An answer repeats what
// coders report, a report can carry anything, and without the boundary one
// line in one report would carry any file of this machine out of the house.
func (c *Channel) resolveFiles(refs []string) ([]outgoing, []string) {
	var files []outgoing
	var notes []string
	for _, ref := range refs {
		if len(files) >= maxOutgoingFiles {
			notes = append(notes, fmt.Sprintf("Only the first %d files of this answer were sent.", maxOutgoingFiles))
			break
		}
		path, err := c.files.ResolveWorkspaceFile(ref)
		if err != nil {
			notes = append(notes, fmt.Sprintf("%s was not sent: %s", ref, err.Error()))
			continue
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			notes = append(notes, fmt.Sprintf("%s was not sent: File not found.", ref))
			continue
		}
		if info.Size() > maxDocumentBytes {
			notes = append(notes, fmt.Sprintf("%s was not sent: it is larger than %d MB.", ref, maxDocumentBytes>>20))
			continue
		}
		files = append(files, outgoing{
			Path:  path,
			Name:  filepath.Base(path),
			Photo: isPhoto(path) && info.Size() <= maxPhotoBytes,
		})
	}
	return files, notes
}

// photoExtensions are what Telegram takes as a photo. Everything else goes as
// a document, which is also where a picture too large for a photo lands.
var photoExtensions = map[string]bool{
	".png":  true,
	".jpg":  true,
	".jpeg": true,
	".gif":  true,
	".webp": true,
}

func isPhoto(path string) bool { return photoExtensions[strings.ToLower(filepath.Ext(path))] }

func (c *Channel) sendFile(ctx context.Context, cl *client, chatID int64, file outgoing) {
	method, field := "sendDocument", "document"
	if file.Photo {
		method, field = "sendPhoto", "photo"
	}
	var err error
	for attempt := 0; attempt < sendAttempts; attempt++ {
		if attempt > 0 && !sleepCtx(ctx, retryDelay) {
			break
		}
		var handle *os.File
		if handle, err = os.Open(file.Path); err != nil {
			break
		}
		err = cl.sendFile(ctx, method, field, chatID, file.Name, handle)
		handle.Close()
		if err == nil {
			return
		}
		if apiCode(err) == http.StatusBadRequest {
			break
		}
	}
	log.Printf("telegram: sending %s failed, dropping it: %v", file.Name, err)
}
