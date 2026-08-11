package telegram

import (
	"strings"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
)

// State is what the channel keeps on disk. It is written 0600 like the push
// channel config: the bot token is a bearer credential, anybody holding it
// speaks as this bot.
type State struct {
	BotToken string    `json:"botToken,omitempty"`
	ChatID   int64     `json:"chatId,omitempty"`
	ChatName string    `json:"chatName,omitempty"`
	PairedAt time.Time `json:"pairedAt,omitempty"`
	// Offset is the receipt for Telegram: the last handled update id plus one.
	// It has to survive a restart, otherwise the next start replays the last
	// 24 hours of messages, and it is written after a message was delivered,
	// never before. A message delivered twice is a nuisance, a message lost is
	// a question that never gets answered.
	Offset  int64 `json:"offset,omitempty"`
	Enabled bool  `json:"enabled"`
	// Answers and Reports narrow what leaves through this channel. Empty means
	// everything, which is what a freshly connected chat gets. They decide
	// nothing about the conversation: the browser always shows every question,
	// every answer and every report, whichever way it came in. This is a window
	// onto that one conversation, and these two say how wide it stands.
	Answers Delivery `json:"answers,omitempty"`
	Reports Delivery `json:"reports,omitempty"`
	// LastNoticeMessageID is the message whose torn off turn the chat was
	// already told about, so the line about a restart comes exactly once.
	LastNoticeMessageID string `json:"lastNoticeMessageId,omitempty"`
}

// Delivery is how wide one of the two outbound choices stands.
type Delivery string

const (
	// DeliveryAll sends everything, the default of a fresh channel.
	DeliveryAll Delivery = ""
	// DeliveryTelegram sends only what was started from this chat. The quiet
	// mode: whoever works at the desk does not want the phone buzzing next to
	// them for every answer they are already reading.
	DeliveryTelegram Delivery = "telegram"
)

// ParseDelivery keeps an unknown value from reaching the file: anything that
// is not the narrow choice is the wide one.
func ParseDelivery(raw string) Delivery {
	if strings.TrimSpace(raw) == string(DeliveryTelegram) {
		return DeliveryTelegram
	}
	return DeliveryAll
}

// store is the file backed state, read and written through on every call like
// the other state files.
type store struct {
	path string
	mu   sync.Mutex
}

func newStore(path string) *store { return &store{path: path} }

func (s *store) Load() State {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Update applies fn under one lock, so a read modify write cannot race the
// poller writing an offset.
func (s *store) Update(fn func(*State)) State {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.load()
	fn(&state)
	statefile.Save(s.path, 0o600, state)
	return state
}

func (s *store) load() State {
	var state State
	statefile.Load(s.path, &state)
	return state
}
