package coder

import (
	"errors"
	"fmt"
	"io/fs"
	"time"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/terminal"
)

// A reading is bounded twice, and the two bounds do different jobs.
//
// TranscriptPage is how many messages the copy view opens with. It is counted
// in messages because that is what a person scrolls through, and it is a first
// page rather than a limit: what it left out is counted and asking for more is
// one request. Sessions here run to tens of megabytes, so opening with
// everything is not an option, and cutting inside a message would leave
// something nobody can copy.
//
// TranscriptCap is the backstop: no answer is larger than this however many
// messages were asked for, because one message can be a whole file somebody
// pasted. It bounds what travels and what a browser has to hold, nothing else.
const (
	TranscriptPage = 50
	TranscriptCap  = 1 << 20
)

// Conversation is one page of a session's recorded conversation, the shape
// the copy view renders as bubbles: who said it, when, and what a tool call
// was about. It reads the same record the activity reading reads and keeps
// the same rules about what counts as conversation, it only leaves the
// structure standing instead of flattening it to text.
type Conversation struct {
	Messages []Message
	// Dropped is how many older messages stand before the first one of the
	// page, so a surface can offer to fetch them.
	Dropped int
}

// Message is one turn of the conversation. A coder's turn holds its words and
// its tool calls in the order they were recorded, so a call stands between
// two paragraphs where it was made.
type Message struct {
	// ID names the message across two readings of the same record, the
	// record's own id where it has one, so a page can ask for what stands
	// before a message it shows.
	ID string
	// Role is RoleUser or RoleCoder.
	Role string
	// Kind tells a user message apart from a command the user ran: a
	// KindCommand message carries the command line as its text.
	Kind string
	// Time is when the message was recorded, zero when the record does not say.
	Time  time.Time
	Parts []Part
}

const (
	RoleUser  = "user"
	RoleCoder = "coder"

	KindWords   = ""
	KindCommand = "command"
)

// Part is one piece of a message: a text as recorded, or a tool call reduced
// to the one line that says what it was about. A tool's result is never a
// part, it is the coder's bookkeeping.
type Part struct {
	Kind string
	// Text is the recorded text of a text part, markdown as the coder wrote
	// it, or the line that says what a tool call was about.
	Text string
	// Tool is the tool's name on a tool part.
	Tool string
}

const (
	PartText = "text"
	PartTool = "tool"
)

// Text answers the message's words alone, the text parts joined.
func (m Message) Text() string {
	out := ""
	for _, p := range m.Parts {
		if p.Kind != PartText || p.Text == "" {
			continue
		}
		if out != "" {
			out += "\n\n"
		}
		out += p.Text
	}
	return out
}

// Cost is what the message weighs against TranscriptCap: its recorded text,
// because a single pasted file is the one thing that makes a reading large.
func (m Message) Cost() int {
	cost := 0
	for _, p := range m.Parts {
		cost += utf8.RuneCountInString(p.Text) + 2
	}
	return cost
}

// ConversationReader is the optional capability of a coder to hand over a
// session's recorded conversation with its structure standing, whole and
// oldest first. It sits next to ActivityReporter on purpose: same record,
// another question. Every call reads the record anew, the view it serves is a
// snapshot taken when somebody opens it. A session whose record is not
// written yet answers an error that matches fs.ErrNotExist (NoRecordYet), any
// other error is a record that could not be read.
type ConversationReader interface {
	SessionConversation(sessionID string) ([]Message, error)
}

// ErrNoConversation says the coder keeps no record a conversation could be
// built from.
var ErrNoConversation = errors.New("This coder keeps no record to read a conversation from.")

// ErrConversationChanged says the message a page was asked to stand before is
// no longer in the record: claude rewrote it, a compaction or a /clear into
// the same file, so the page a view holds belongs to another reading. The
// newest page would stand above the oldest message shown, a conversation
// twice, so the view is told to open again instead.
var ErrConversationChanged = errors.New("The conversation changed, reopen the view.")

// NoRecordYet is the error of a session whose record is not written yet, a
// coder that was started and nobody has spoken to. It reads as the sentence
// it is given and answers errors.Is(err, fs.ErrNotExist), which is how a
// reader tells an empty conversation from a record that could not be read.
func NoRecordYet(sentence string) error { return noRecordYet(sentence) }

type noRecordYet string

func (e noRecordYet) Error() string { return string(e) }

func (e noRecordYet) Unwrap() error { return fs.ErrNotExist }

// Conversation answers one page of the session's recorded conversation: the
// newest messages, or with before set the ones that stand before the message
// of that id. messages bounds the page, TranscriptCap its size. A before the
// record no longer holds answers ErrConversationChanged. It never falls back
// to the screen: a picture cannot be cut into messages.
func (s *Manager) Conversation(rawID string, messages int, before string) (Conversation, error) {
	id, err := terminal.ValidateIdentifier(rawID)
	if err != nil {
		return Conversation{}, err
	}
	reader, ok := s.coder.(ConversationReader)
	if !ok {
		return Conversation{}, ErrNoConversation
	}
	all, err := reader.SessionConversation(id)
	if err != nil {
		return Conversation{}, err
	}
	if before != "" {
		found := false
		for i, m := range all {
			if m.ID == before {
				all, found = all[:i], true
				break
			}
		}
		if !found {
			return Conversation{}, ErrConversationChanged
		}
	}
	page, dropped := Window(all, messages, TranscriptCap)
	return Conversation{Messages: page, Dropped: dropped}, nil
}

// Window keeps the newest messages whole and drops the oldest ones off the
// top: messages is how many to keep (zero or less means all), cap is the
// largest reading in runes (zero or less means no cap), and a message that
// alone exceeds what is left still stands when it is the newest one, because
// an empty answer helps nobody. It reports how many messages fell off.
func Window(all []Message, messages, cap int) ([]Message, int) {
	if len(all) == 0 {
		return nil, 0
	}
	kept := 0
	remaining := cap
	for i := len(all) - 1; i >= 0; i-- {
		if messages > 0 && kept >= messages {
			break
		}
		cost := all[i].Cost()
		if cap > 0 && cost > remaining && kept > 0 {
			break
		}
		kept++
		remaining -= cost
	}
	dropped := len(all) - kept
	return all[dropped:], dropped
}

// TurnBuilder is what the record readers share: the messages so far and the
// coder turn that is open. A coder's turn is one message from the words that
// started it to the next words of the user, whatever rounds the coder took in
// between. The zero value is ready to use.
type TurnBuilder struct {
	all []Message
	// open is the index of the open coder message plus one, zero for none.
	open    int
	ordinal int
}

// Messages answers what was built so far.
func (b *TurnBuilder) Messages() []Message {
	return b.all
}

// Add closes the open turn and appends a message that is no coder turn. id
// names it where the record does; without one it gets an ordinal, which a
// second reading of the same record hands out the same way.
func (b *TurnBuilder) Add(message Message, id string, at time.Time) {
	b.open = 0
	b.ordinal++
	message.ID = id
	if message.ID == "" {
		message.ID = fmt.Sprintf("m%d", b.ordinal)
	}
	message.Time = at
	b.all = append(b.all, message)
}

// AddPart appends a part to the open coder turn, opening one under id when
// none is open.
func (b *TurnBuilder) AddPart(id string, at time.Time, part Part) {
	if b.open == 0 {
		b.Add(Message{Role: RoleCoder}, id, at)
		b.open = len(b.all)
	}
	turn := &b.all[b.open-1]
	turn.Parts = append(turn.Parts, part)
}

// CloseTurn ends the open coder turn, so the next part opens a new one.
func (b *TurnBuilder) CloseTurn() {
	b.open = 0
}
