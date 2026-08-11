// Package telegram is the second way into the assistant's live conversation:
// a Telegram bot the user writes to from a phone, answering in the same
// conversation the browser shows.
//
// It is not another notification channel. A message from the paired chat goes
// through Service.Open("") and Service.Send() like a message typed into the
// composer, and every finished answer plus every job report leaves through the
// same onDone hook the notification center listens on.
//
// The bot is a door past the login, so the rules around it are narrow: exactly
// one chat may talk to it, that chat is bound from the inside with a code that
// lives ten minutes in memory and is used once, and every other chat is
// dropped without an answer. The bot token is a bearer credential in the API
// path, so nothing that leaves this package carries it, see redact.
//
//	<state-dir>/assistant-telegram.json   token, paired chat, offset, switch
package telegram

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/assistant"
)

// Conversations is the slice of the assistant's conversation service this
// channel drives. Nothing here owns a conversation: the live one is asked for
// on every message, because the user can start a new one from the browser at
// any moment.
type Conversations interface {
	Open(coderID string) (assistant.Conversation, error)
	Current() (assistant.Conversation, bool)
	Coders() []assistant.CoderInfo
	// SendFrom is Send with the origin, which is what marks a message as one
	// this chat asked for. Nothing else about a message differs.
	SendFrom(id, prompt string, attachments []assistant.Attachment, source string) (assistant.Run, error)
	LastAnswer(id string) (assistant.Message, bool)
	UploadDir(id string) (string, error)
	Running(id string) bool
}

// Files is the slice of the assistant workspace this channel needs: resolving
// a path an answer names, bounded to the workspace, and storing a downloaded
// image where a turn can reach it.
type Files interface {
	ResolveWorkspaceFile(rel string) (string, error)
	SaveUpload(dir, name string, src io.Reader) (assistant.Attachment, error)
}

// PollState is what the settings page says about the poller.
type PollState string

const (
	// StateOff means no poller exists: no token, or the channel is switched off.
	StateOff PollState = "off"
	// StateRunning means the poller is listening for messages.
	StateRunning PollState = "running"
	// StateStopped means the poller gave up and will not come back on its own.
	// Only a rejected token gets it there, everything else retries.
	StateStopped PollState = "stopped"
)

// Status is the poller's state plus, when it stopped, the sentence saying why.
type Status struct {
	State  PollState
	Reason string
}

// Settings is what the settings page renders. The token itself is never part
// of it, only whether one is stored.
type Settings struct {
	TokenSet bool
	Enabled  bool
	ChatID   int64
	ChatName string
	PairedAt time.Time
	// Answers and Reports are the two outbound choices, see State.
	Answers Delivery
	Reports Delivery
}

// Code is a pairing code with the moment it stops working.
type Code struct {
	Value   string
	Expires time.Time
}

// Remaining is how long the code is still good for, zero once it is spent.
func (c Code) Remaining(now time.Time) time.Duration {
	if left := c.Expires.Sub(now); left > 0 {
		return left
	}
	return 0
}

const (
	// codeLifetime is how long a pairing code works. Long enough to walk to the
	// phone, short enough that a code left on a screen is worthless.
	codeLifetime = 10 * time.Minute
	// staleAfter drops messages that waited through a server pause. Nobody
	// wants the assistant working through the night before last after a restart.
	staleAfter = time.Hour
	// noticeMaxAge bounds the restart notice the same way: an answer torn off
	// yesterday is not news today.
	noticeMaxAge = time.Hour
	// deliverTimeout bounds one outbound delivery, so a slow Telegram cannot
	// hold a goroutine of a finished turn forever.
	deliverTimeout = 2 * time.Minute
	// maxRememberedChats caps the set of strangers already logged.
	maxRememberedChats = 1000
)

// tokenPattern is the shape the Bot API gives out: the bot id, a colon, the
// secret. Checked at the door, because a token that is not even shaped like
// one answers 404 instead of 401, and the poller would retry that forever.
var tokenPattern = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{20,}$`)

// Channel is the running channel: the state file, the poller, and the outbound
// side of the onDone hook.
type Channel struct {
	store         *store
	conversations Conversations
	files         Files
	now           func() time.Time
	// base is the Bot API root. Tests point it at their own server, nothing
	// else ever changes it.
	base string

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
	status Status
	code   Code
	// foreign remembers the chats already logged, so a stranger sending
	// messages all day writes one line, not one per message.
	foreign map[int64]bool
}

// apiURLEnv points the channel at another Bot API root. It exists for the e2e
// instances, which have to be able to set a token without a runner reaching
// api.telegram.org; the same way the self update takes its feed from
// DEV_COCKPIT_UPDATE_API_URL there.
const apiURLEnv = "DEV_COCKPIT_TELEGRAM_API_URL"

// New builds the channel over the state directory. It never fails and never
// talks to Telegram: without a token there is nothing to talk to, which is the
// normal state of every installation that does not use this.
func New(stateDir string, conversations Conversations, files Files) *Channel {
	base := apiBase
	if override := strings.TrimRight(strings.TrimSpace(os.Getenv(apiURLEnv)), "/"); override != "" {
		base = override
	}
	return &Channel{
		store:         newStore(filepath.Join(stateDir, "assistant-telegram.json")),
		conversations: conversations,
		files:         files,
		now:           time.Now,
		base:          base,
		status:        Status{State: StateOff},
		foreign:       map[int64]bool{},
	}
}

// Start brings the poller up when a token is stored and the channel is on. It
// runs at boot, not only when somebody saves the settings, otherwise the bot
// would go quiet after every restart of this cockpit.
func (c *Channel) Start() {
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return
	}
	st := c.store.Load()
	if st.BotToken == "" || !st.Enabled {
		c.status = Status{State: StateOff}
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	c.cancel, c.done, c.status = cancel, done, Status{State: StateRunning}
	client := c.client(st.BotToken)
	c.mu.Unlock()

	go c.run(ctx, client, done)
}

// Stop ends the poller and waits for it. The wait is the point: Telegram
// allows one getUpdates per bot and answers the second with 409, so the old
// poller has to be gone before a new one starts.
func (c *Channel) Stop() {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.cancel, c.done = nil, nil
	c.status = Status{State: StateOff}
	c.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	<-done
}

// Restart is the one way a token change or the switch takes effect: the old
// poller ends before the new one begins.
func (c *Channel) Restart() {
	c.Stop()
	c.Start()
}

// Status is what the settings page shows about the poller.
func (c *Channel) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.status
}

// Settings is the stored configuration without the token.
func (c *Channel) Settings() Settings {
	st := c.store.Load()
	return Settings{
		TokenSet: st.BotToken != "",
		Enabled:  st.Enabled,
		ChatID:   st.ChatID,
		ChatName: st.ChatName,
		PairedAt: st.PairedAt,
		Answers:  st.Answers,
		Reports:  st.Reports,
	}
}

// SetDelivery stores the two outbound choices. They narrow what this channel
// sends and nothing else: the conversation keeps every message either way.
func (c *Channel) SetDelivery(answers, reports Delivery) {
	c.store.Update(func(s *State) {
		s.Answers, s.Reports = answers, reports
	})
}

// Delivers reports whether an answer actually reaches a chat right now. The
// assistant's instructions ask it: naming a file it wants sent is only worth
// writing down while there is a channel that carries one.
func (c *Channel) Delivers() bool {
	st := c.store.Load()
	return st.BotToken != "" && st.Enabled && st.ChatID != 0
}

// SetToken stores a new bot token and restarts the poller. Turning the channel
// on with it is deliberate: somebody who just entered a token wants the bot to
// answer, not to find a second switch.
func (c *Channel) SetToken(raw string) error {
	token := strings.TrimSpace(raw)
	if token == "" {
		return errors.New("Paste the bot token from BotFather first.")
	}
	if !tokenPattern.MatchString(token) {
		return errors.New("That does not look like a bot token. BotFather hands out something like 123456789:AA....")
	}
	c.store.Update(func(s *State) {
		s.BotToken = token
		s.Enabled = true
		// The offset belongs to the old bot. A new one starts with whatever
		// Telegram has queued for it, bounded by the age check on delivery.
		s.Offset = 0
	})
	c.Restart()
	return nil
}

// ClearToken removes the token and stops the poller. The paired chat stays: a
// chat id belongs to the person, not to the bot, so a replacement token finds
// the same chat.
func (c *Channel) ClearToken() {
	c.Stop()
	c.store.Update(func(s *State) {
		s.BotToken = ""
		s.Offset = 0
	})
}

// SetEnabled switches the channel without touching the token.
func (c *Channel) SetEnabled(on bool) {
	c.store.Update(func(s *State) { s.Enabled = on })
	c.Restart()
}

// Unpair drops the bound chat. Nothing goes out afterwards, and the next code
// binds whoever answers it.
func (c *Channel) Unpair() {
	c.store.Update(func(s *State) {
		s.ChatID = 0
		s.ChatName = ""
		s.PairedAt = time.Time{}
		s.LastNoticeMessageID = ""
	})
}

// NewCode hands out a pairing code. It refuses while a chat is bound, so the
// only way to move the channel to another chat is to release the old one
// first, in the browser, behind the login.
func (c *Channel) NewCode() (Code, error) {
	if c.store.Load().ChatID != 0 {
		return Code{}, errors.New("A chat is connected already. Disconnect it first.")
	}
	value, err := newCodeValue()
	if err != nil {
		return Code{}, errors.New("The pairing code could not be created.")
	}
	code := Code{Value: value, Expires: c.now().Add(codeLifetime)}
	c.mu.Lock()
	c.code = code
	c.mu.Unlock()
	return code, nil
}

// Code is the pairing code still waiting to be used, if there is one. It lives
// in memory on purpose: a restart during the pairing means a fresh code.
func (c *Channel) Code() (Code, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.code.Value == "" || c.code.Remaining(c.now()) == 0 {
		return Code{}, false
	}
	return c.code, true
}

// consumeCode reports whether text is the valid pairing code and spends it.
// It is the only path on which a message from an unknown chat is looked at.
func (c *Channel) consumeCode(text string) bool {
	candidate := strings.ToUpper(strings.TrimSpace(text))
	if candidate == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.code.Value == "" || c.code.Remaining(c.now()) == 0 {
		return false
	}
	if candidate != c.code.Value {
		return false
	}
	c.code = Code{}
	return true
}

// noteForeign logs one line per unknown chat. Per chat, not per message: a bot
// name is public, anybody can write to it, and a log that grows with their
// patience is a log nobody reads.
func (c *Channel) noteForeign(chatID int64) {
	c.mu.Lock()
	seen := c.foreign[chatID]
	// The set only exists to keep the log short, so it is capped instead of
	// growing with however many strangers write. Starting over costs one
	// repeated line per chat.
	if len(c.foreign) >= maxRememberedChats {
		c.foreign = map[int64]bool{}
		seen = false
	}
	c.foreign[chatID] = true
	c.mu.Unlock()
	if !seen {
		log.Printf("telegram: ignoring messages from chat %d, it is not the connected chat", chatID)
	}
}

// codeAlphabet leaves out the characters that are read wrong off a screen.
const codeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newCodeValue() (string, error) {
	raw := make([]byte, 8)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	out := make([]byte, len(raw))
	for i, b := range raw {
		out[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(out), nil
}
