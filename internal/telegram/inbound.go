package telegram

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"unicode"

	"github.com/local/dev-cockpit/internal/assistant"
	"github.com/local/dev-cockpit/internal/statefile"
)

// The three lines this channel says on its own. They are English like the rest
// of the interface.
const (
	startLine       = "This chat talks to the assistant of your dev-cockpit. Write what you want, it answers here, and the reports of the jobs it steers arrive here too. /new starts a new conversation, /context says how full this one is."
	pairedLine      = "This chat is connected. Write what you want, the assistant answers here."
	busyLine        = "The assistant is still writing an answer. Wait for it, then start a new conversation."
	unsupportedLine = "Only text and images arrive here. Voice messages, videos and stickers are not read."
	tooLargeLine    = "That file is larger than 20 MB, which is where the Telegram bot API stops. Put it somewhere I can read instead."
	fetchFailedLine = "That image could not be fetched from Telegram."
	newTextLine     = "/new takes no text, it only asks which coder answers. Send your message after the new conversation is open."
	cancelledLine   = "Nothing changed, the conversation is the one it was."
	goneLine        = "That question is no longer open. Send /new again."
	noCodersLine    = "No coder is installed that could answer a conversation."
)

// newPrefix marks the presses that belong to /new. The data of a button is
// bounded to 64 bytes by Telegram, so it stays short: the prefix and a coder id.
const newPrefix = "new:"

// newCancel is the button that changes nothing.
const newCancel = newPrefix + "cancel"

// handle takes one update. Everything that is not the paired chat ends in
// handleUnpaired, which answers nothing.
func (c *Channel) handle(ctx context.Context, cl *client, update Update) {
	st := c.store.Load()
	if press := update.CallbackQuery; press != nil {
		// A press goes behind the same door as a message: a button from a chat
		// that is not the connected one is dropped, and it is not even answered.
		chat, ok := press.Chat()
		if !ok {
			return
		}
		if st.ChatID == 0 || chat.ID != st.ChatID {
			c.noteForeign(chat.ID)
			return
		}
		c.handlePress(ctx, cl, press, chat.ID)
		return
	}
	msg := update.Message
	if msg == nil {
		return
	}
	if st.ChatID == 0 || msg.Chat.ID != st.ChatID {
		c.handleUnpaired(ctx, cl, msg)
		return
	}
	c.handlePaired(ctx, cl, msg)
}

// handlePress runs a press on one of the bot's buttons. Only /new has any.
func (c *Channel) handlePress(ctx context.Context, cl *client, press *CallbackQuery, chatID int64) {
	// Answered first, so the button stops spinning whatever happens next.
	if err := cl.answerCallback(ctx, press.ID); err != nil {
		log.Printf("telegram: %v", err)
	}
	data := strings.TrimSpace(press.Data)
	if !strings.HasPrefix(data, newPrefix) {
		_ = c.send(ctx, cl, chatID, goneLine)
		return
	}
	if data == newCancel {
		_ = c.send(ctx, cl, chatID, cancelledLine)
		return
	}
	c.startNewConversation(ctx, cl, chatID, strings.TrimPrefix(data, newPrefix))
}

// handleUnpaired is the whole story for a chat that is not the paired one: a
// message that is exactly the valid pairing code binds the chat, everything
// else is dropped without an answer. Whoever writes gets silence and does not
// even learn that something is running here.
func (c *Channel) handleUnpaired(ctx context.Context, cl *client, msg *Message) {
	if !c.consumeCode(msg.Text) {
		c.noteForeign(msg.Chat.ID)
		return
	}
	c.store.Update(func(s *State) {
		s.ChatID = msg.Chat.ID
		s.ChatName = msg.Chat.Name()
		s.PairedAt = c.now().UTC()
		s.LastNoticeMessageID = ""
	})
	log.Printf("telegram: chat %d connected", msg.Chat.ID)
	_ = c.send(ctx, cl, msg.Chat.ID, pairedLine)
}

// handlePaired is a message from the one chat that may talk: a command, an
// image, or a prompt for the live conversation.
func (c *Channel) handlePaired(ctx context.Context, cl *client, msg *Message) {
	if command, rest := splitCommand(msg.Text); command != "" {
		if c.handleCommand(ctx, cl, msg.Chat.ID, command, rest) {
			return
		}
	}
	file, kind := pickMedia(msg)
	if kind == mediaOther {
		// A prompt that vanishes without a word is worse than a refusal: the
		// user waits for an answer that is never coming.
		_ = c.send(ctx, cl, msg.Chat.ID, unsupportedLine)
		return
	}
	text := strings.TrimSpace(msg.Text)
	if kind == mediaImage {
		text = strings.TrimSpace(msg.Caption)
	}
	if text == "" && kind == mediaNone {
		return
	}

	// The live conversation is asked for on every message: the user can start
	// a new one in the browser between two messages.
	conversation, err := c.conversations.Open("")
	if err != nil {
		_ = c.send(ctx, cl, msg.Chat.ID, err.Error())
		return
	}
	var attachments []assistant.Attachment
	if kind == mediaImage {
		attachment, err := c.fetchImage(ctx, cl, conversation.ID, file)
		if err != nil {
			_ = c.send(ctx, cl, msg.Chat.ID, err.Error())
			return
		}
		attachments = append(attachments, attachment)
	}
	if _, err := c.conversations.SendFrom(conversation.ID, text, attachments, assistant.SourceTelegram); err != nil {
		// Send writes its refusals for people, a full queue among them.
		_ = c.send(ctx, cl, msg.Chat.ID, err.Error())
	}
}

// handleCommand runs the two commands and reports whether it took the message.
// Anything else that starts with a slash is a normal message: a path is not a
// command, and swallowing one would be worse than answering it.
func (c *Channel) handleCommand(ctx context.Context, cl *client, chatID int64, command, rest string) bool {
	switch command {
	case "/start":
		_ = c.send(ctx, cl, chatID, startLine)
		return true
	case "/new":
		c.askForCoder(ctx, cl, chatID, rest)
		return true
	case "/context":
		_ = c.send(ctx, cl, chatID, c.contextLine())
		return true
	}
	return false
}

// askForCoder is the first half of /new: the installed coders as buttons, and
// nothing happens yet. A new conversation is not something a slipped thumb
// should produce, so the command asks and the press decides.
func (c *Channel) askForCoder(ctx context.Context, cl *client, chatID int64, rest string) {
	coders := c.conversations.Coders()
	if len(coders) == 0 {
		_ = c.send(ctx, cl, chatID, noCodersLine)
		return
	}
	lines := []string{"Which coder answers the new conversation?"}
	if current, live := c.conversations.Current(); live {
		if label := coderLabel(coders, current.CoderID); label != "" {
			lines = append(lines, "The one running now is "+label+".")
		}
	}
	if rest != "" {
		// Said, not swallowed: a stray line behind the command would otherwise
		// become the first message of a conversation nobody has opened yet.
		lines = append(lines, newTextLine)
	}
	buttons := make([]InlineButton, 0, len(coders)+1)
	for _, coder := range coders {
		buttons = append(buttons, InlineButton{Text: coder.Label, Data: newPrefix + coder.ID})
	}
	buttons = append(buttons, InlineButton{Text: "Cancel", Data: newCancel})
	if err := cl.sendButtons(ctx, chatID, strings.Join(lines, "\n"), buttons); err != nil {
		log.Printf("telegram: %v", err)
	}
}

// startNewConversation is the second half of /new, on the press. Open with a
// coder id creates a conversation as soon as the live one carries messages and
// hands back an untouched one otherwise, so pressing twice leaves no trail of
// empty conversations.
func (c *Channel) startNewConversation(ctx context.Context, cl *client, chatID int64, coderID string) {
	if coderLabel(c.conversations.Coders(), coderID) == "" {
		_ = c.send(ctx, cl, chatID, "That coder is not installed here anymore.")
		return
	}
	// Asked here and not when the command arrived: between the question and the
	// press lies time, and a turn may have started in it.
	if current, live := c.conversations.Current(); live && c.conversations.Running(current.ID) {
		// Pulling the conversation out from under a running turn would tear
		// that turn off, and a torn off turn is paid for and gone.
		_ = c.send(ctx, cl, chatID, busyLine)
		return
	}
	if _, err := c.conversations.Open(coderID); err != nil {
		_ = c.send(ctx, cl, chatID, err.Error())
		return
	}
	_ = c.send(ctx, cl, chatID, "New conversation, "+coderLabel(c.conversations.Coders(), coderID)+" answers.")
}

// coderLabel is what a coder is called here, empty when it is not installed.
func coderLabel(coders []assistant.CoderInfo, id string) string {
	for _, coder := range coders {
		if coder.ID == id {
			if coder.Label != "" {
				return coder.Label
			}
			return coder.ID
		}
	}
	return ""
}

// contextLine says how full the context window of the live conversation
// stands. Nothing is worked out here, the reading sits on the conversation;
// what this does is say it, and say plainly when there is nothing to say.
func (c *Channel) contextLine() string {
	conversation, live := c.conversations.Current()
	if !live {
		return "No conversation is open yet."
	}
	usage := conversation.Context
	if usage == nil {
		return "No turn has run in this conversation yet, so there is no reading."
	}
	reading := *usage
	if reading.Window == 0 {
		// The same resolution the page does: what the turn reported is the
		// model, how large its window is stays a lookup.
		reading.Window = assistant.ContextWindow(conversation.CoderID, reading.Model, reading.Tier)
	}
	coder := coderLabel(c.conversations.Coders(), conversation.CoderID)
	if coder == "" {
		coder = conversation.CoderID
	}
	if !reading.Known() {
		if reading.Model == "" {
			return "The context of this conversation is unknown: the last turn reported no reading. Coder " + coder + "."
		}
		return fmt.Sprintf("The context of this conversation is unknown: how much %s holds is not known here. Coder %s, %s tokens used.",
			reading.Model, coder, thousands(reading.Tokens))
	}
	return fmt.Sprintf("Context %d%% full, %s of %s tokens. Model %s, coder %s.",
		reading.Percent(), thousands(reading.Tokens), thousands(reading.Window), reading.Model, coder)
}

// thousands groups a number the way a person reads it off a phone.
func thousands(n int) string {
	digits := strconv.Itoa(n)
	if n < 0 {
		return digits
	}
	var b strings.Builder
	for i, r := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return b.String()
}

// splitCommand takes the leading command off a message and returns the rest,
// which becomes the first message of a new conversation. A command may carry
// the bot name in a group, /new@somebot, so that is cut off.
func splitCommand(text string) (command, rest string) {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "/") {
		return "", ""
	}
	cut := strings.IndexFunc(trimmed, unicode.IsSpace)
	if cut < 0 {
		cut = len(trimmed)
	}
	command = strings.ToLower(trimmed[:cut])
	if at := strings.IndexByte(command, '@'); at > 0 {
		command = command[:at]
	}
	return command, strings.TrimSpace(trimmed[cut:])
}

// mediaKind is what a message carries besides its text.
type mediaKind int

const (
	// mediaNone is a plain text message.
	mediaNone mediaKind = iota
	// mediaImage is a photo or a picture sent as a file.
	mediaImage
	// mediaOther is everything this channel does not take.
	mediaOther
)

// mediaFile is the part of a photo or document the download needs.
type mediaFile struct {
	ID   string
	Size int64
	MIME string
}

// pickMedia decides what a message carries. Of a photo's offered sizes the
// largest is taken, which is the one the user actually sent. A picture sent as
// a file is a document with an image type and goes the same way, because that
// is what a phone does when the user picks "send as file".
func pickMedia(msg *Message) (mediaFile, mediaKind) {
	if len(msg.Photo) > 0 {
		largest := msg.Photo[0]
		for _, size := range msg.Photo[1:] {
			if size.Width*size.Height >= largest.Width*largest.Height {
				largest = size
			}
		}
		return mediaFile{ID: largest.FileID, Size: largest.FileSize, MIME: "image/jpeg"}, mediaImage
	}
	if msg.Document != nil {
		if !strings.HasPrefix(strings.ToLower(msg.Document.MimeType), "image/") {
			return mediaFile{}, mediaOther
		}
		return mediaFile{ID: msg.Document.FileID, Size: msg.Document.FileSize, MIME: msg.Document.MimeType}, mediaImage
	}
	if strings.TrimSpace(msg.Text) != "" || strings.TrimSpace(msg.Caption) != "" {
		return mediaFile{}, mediaNone
	}
	// A voice message, a video, a sticker, a location: everything without text
	// and without a picture lands here.
	return mediaFile{}, mediaOther
}

// fetchImage downloads one picture into the conversation's own upload
// directory and returns it as an attachment a turn can open.
func (c *Channel) fetchImage(ctx context.Context, cl *client, conversationID string, file mediaFile) (assistant.Attachment, error) {
	if file.Size > MaxDownloadBytes {
		return assistant.Attachment{}, errors.New(tooLargeLine)
	}
	info, err := cl.getFile(ctx, file.ID)
	if err != nil {
		log.Printf("telegram: %v", err)
		return assistant.Attachment{}, errors.New(fetchFailedLine)
	}
	if info.FileSize > MaxDownloadBytes {
		return assistant.Attachment{}, errors.New(tooLargeLine)
	}
	dir, err := c.conversations.UploadDir(conversationID)
	if err != nil {
		return assistant.Attachment{}, errors.New(fetchFailedLine)
	}
	body, err := cl.download(ctx, info.FilePath)
	if err != nil {
		log.Printf("telegram: %v", err)
		return assistant.Attachment{}, errors.New(fetchFailedLine)
	}
	defer body.Close()

	// The name is made here, nothing of it comes out of the answer: a name
	// from a foreign source is not something to build a path from. Only the
	// extension is derived from the media type, through a whitelist.
	attachment, err := c.files.SaveUpload(dir, "telegram-"+statefile.NewID()+imageExt(file.MIME), io.LimitReader(body, MaxDownloadBytes))
	if err != nil {
		log.Printf("telegram: storing an image failed: %v", err)
		return assistant.Attachment{}, errors.New(fetchFailedLine)
	}
	// Named explicitly instead of derived from the extension, so a picture in
	// a type this cockpit does not know still arrives as a picture.
	attachment.Media = "image"
	return attachment, nil
}

// imageExtensions is the whitelist an incoming picture's name is built with.
var imageExtensions = map[string]string{
	"image/jpeg":    ".jpg",
	"image/jpg":     ".jpg",
	"image/png":     ".png",
	"image/gif":     ".gif",
	"image/webp":    ".webp",
	"image/bmp":     ".bmp",
	"image/tiff":    ".tiff",
	"image/avif":    ".avif",
	"image/heic":    ".heic",
	"image/heif":    ".heif",
	"image/svg+xml": ".svg",
}

func imageExt(mime string) string {
	if ext, ok := imageExtensions[strings.ToLower(strings.TrimSpace(mime))]; ok {
		return ext
	}
	return ".img"
}
