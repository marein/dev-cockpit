package assistant

import (
	"strings"
	"time"
)

// Status is the lifecycle state of a conversation.
type Status string

const (
	// StatusActive marks a conversation that still owns its provider session.
	StatusActive Status = "active"
	// StatusTransferred marks a conversation whose provider session was handed to a
	// coder terminal. The transcript stays readable, the composer does not.
	StatusTransferred Status = "transferred"
	// StatusArchived marks a conversation that a newer one replaced. The
	// transcript stays readable, its provider session is gone: nothing can
	// continue it, and the cockpit holds the whole conversation on disk.
	StatusArchived Status = "archived"
)

// SourceTelegram marks what came in through the chat channel. An empty source
// is the browser, which is what every message written before this existed
// carries, so an old transcript keeps meaning what it meant.
const SourceTelegram = "telegram"

// TurnSourceEnv carries the origin of the message a turn answers into that
// turn's process, and from there into the cockpit commands the turn runs. It
// is how a job knows where it was asked for: `coder-new` and `coder-steer`
// reach the server over the local socket and would otherwise arrive with
// nothing but their arguments. Unset means the browser.
const TurnSourceEnv = "DEV_COCKPIT_TURN_SOURCE"

// Source normalizes an origin from outside: only what this cockpit knows is
// kept, everything else counts as the browser. The value ends up in state
// files and in filters, so nothing unchecked goes in.
func Source(raw string) string {
	if strings.TrimSpace(raw) == SourceTelegram {
		return SourceTelegram
	}
	return ""
}

// Role distinguishes the two message authors.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
)

// State is the delivery state of one message.
type State string

const (
	StateComplete    State = "complete"
	StateStreaming   State = "streaming"
	StateCancelled   State = "cancelled"
	StateFailed      State = "failed"
	StateInterrupted State = "interrupted"
	// StateQueued marks a user message waiting for the running turn to end. It
	// sits in the transcript, deletable until the server flushes every waiting
	// message as one new turn.
	StateQueued State = "queued"
)

// Settled reports whether no further content can arrive for this state.
func (s State) Settled() bool { return s != StateStreaming }

// Retryable reports whether a turn in this state may be sent again. A
// completed turn is never resent, it would charge the user twice for an
// answer that is already on screen.
func (s State) Retryable() bool {
	return s == StateFailed || s == StateCancelled || s == StateInterrupted
}

// Limits the feature enforces before a prompt reaches a process and while a
// response is parsed. The prompt bound keeps the argv within what execve
// accepts, the output bound keeps one runaway answer from growing the state
// file without limit.
const (
	// MaxPromptBytes is the largest accepted user message.
	MaxPromptBytes = 32 << 10
	// MaxResponseBytes is the largest accepted assistant answer.
	MaxResponseBytes = 1 << 20
	// MaxTitleRunes bounds a conversation title.
	MaxTitleRunes = 120
	// MaxQueuedMessages bounds what may wait while a turn runs. The flush joins
	// every waiting message into one prompt, and that prompt has to stay within
	// what a process argument accepts.
	MaxQueuedMessages = 20
	// DefaultTitle names a conversation that has not seen a prompt yet.
	DefaultTitle = "New conversation"
)

// Summary is one index entry. It deliberately carries no messages: the list
// page renders from the index alone, so opening it never loads a transcript.
type Summary struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	CoderID       string    `json:"coderId"`
	ProjectPath   string    `json:"projectPath"`
	Status        Status    `json:"status"`
	CreatedAt     time.Time `json:"createdAt"`
	LastMessageAt time.Time `json:"lastMessageAt"`
	// Preview is the opening of the last assistant answer, bounded for the list.
	Preview string `json:"preview"`
	// Unfinished marks a conversation whose last turn did not complete, so the list can
	// show it without loading the transcript.
	Unfinished bool `json:"unfinished"`
	// MessageCount is the number of stored messages, used for the list subtitle.
	MessageCount int `json:"messageCount"`
}

// Conversation is one complete conversation with its transcript.
type Conversation struct {
	Summary
	// NativeSessionID is the provider session this conversation drives. It equals the
	// conversation id, which is UUID shaped so it also works as a tmux session name
	// once the conversation is transferred.
	NativeSessionID string `json:"nativeSessionId"`
	// TransferredSessionID is the coder terminal that took over, set once.
	TransferredSessionID string    `json:"transferredSessionId,omitempty"`
	UpdatedAt            time.Time `json:"updatedAt"`
	Messages             []Message `json:"messages"`
	// Draft is what was typed into the composer and not sent yet. It belongs to
	// the conversation, not to the browser that typed it, so the same words are
	// there after a page change and on the next device.
	Draft Draft `json:"draft,omitempty"`
	// Context is how full the coder's context window stood at the end of the
	// last turn that reported it. It lives on the conversation, not on a
	// message: it describes the whole conversation as it stands, and a reader
	// coming back has to see the number without a turn running.
	Context *ContextUsage `json:"context,omitempty"`
}

// Draft is an unsent prompt with the files that were already uploaded for it.
// The files travel with the text, otherwise coming back shows a message whose
// attachments are gone.
type Draft struct {
	Text        string       `json:"text,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	UpdatedAt   time.Time    `json:"updatedAt,omitempty"`
}

// Empty reports whether there is nothing to restore.
func (d Draft) Empty() bool { return d.Text == "" && len(d.Attachments) == 0 }

// Same reports whether a draft would store exactly what is already stored,
// which is how a repeated flush avoids rewriting the transcript.
func (d Draft) Same(text string, attachments []Attachment) bool {
	if d.Text != text || len(d.Attachments) != len(attachments) {
		return false
	}
	for i := range attachments {
		if d.Attachments[i].Path != attachments[i].Path {
			return false
		}
	}
	return true
}

// Attachment is one file a prompt carries. The cockpit stores it inside the
// conversation's own files directory and hands the coder the absolute path, so a coder
// that can look at images gets a real file instead of a copy of the bytes.
type Attachment struct {
	Name string `json:"name"`
	// Path is the absolute host path. It stays in the transcript so a later
	// turn can point the coder at the same file again.
	Path string `json:"path"`
	// Media classifies the file for the browser: image, video, audio or file.
	Media string `json:"media"`
	Size  int64  `json:"size"`
}

// Message is one turn half, a user prompt or an assistant answer.
type Message struct {
	ID          string       `json:"id"`
	Role        Role         `json:"role"`
	Content     string       `json:"content"`
	Attachments []Attachment `json:"attachments,omitempty"`
	CreatedAt   time.Time    `json:"createdAt"`
	// RunID ties an assistant message to the generation that produced it, so a
	// stale stream cannot write into a newer retry.
	RunID string `json:"runId,omitempty"`
	State State  `json:"state"`
	// Error is a curated, user facing sentence. Provider stderr, argv and
	// paths never reach it.
	Error string `json:"error,omitempty"`
	// Source is where this message came from, empty for the browser. An answer
	// inherits it from the question it answers, which is what lets a channel
	// send only what was asked there.
	Source string `json:"source,omitempty"`
	// Wake marks a message a check wrote, so the page can show where it came
	// from. A check's prompt is never stored, only what it concluded, which is
	// why nothing in a transcript can look like something the user said.
	Wake *WakeNote `json:"wake,omitempty"`
}

// WakeNote says which terminal a check was about and what it concluded.
type WakeNote struct {
	Terminal string `json:"terminal"`
	// Name is the coder's name as the job carried it when this report was
	// written. It is written down instead of looked up later because the job
	// is gone by then, or worse, the terminal is steered again and the lookup
	// answers with its successor. An older report simply carries none.
	Name string `json:"name,omitempty"`
	// Project travels with the name and for the same reason, so a report says
	// where that coder worked without asking anybody.
	Project string `json:"project,omitempty"`
	Verdict string `json:"verdict"`
	// Source is where the job was asked for, copied from the job when the
	// report is written and for the same reason as the name: by the time a
	// channel decides whether to carry this report, the job may be gone or the
	// terminal steered again.
	Source string `json:"source,omitempty"`
}

// Last returns the final message of the transcript.
func (c Conversation) Last() (Message, bool) {
	if len(c.Messages) == 0 {
		return Message{}, false
	}
	return c.Messages[len(c.Messages)-1], true
}

// Idle reports whether the conversation currently has no unfinished assistant turn.
func (c Conversation) Idle() bool {
	last, ok := c.Last()
	return !ok || last.State.Settled()
}

// summarize refreshes the index fields derived from the transcript.
func (c *Conversation) summarize() {
	c.MessageCount = len(c.Messages)
	c.Preview = ""
	c.Unfinished = false
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.Role != RoleAssistant {
			continue
		}
		c.Preview = preview(m.Content)
		c.Unfinished = m.State != StateComplete
		break
	}
	if last, ok := c.Last(); ok {
		c.LastMessageAt = last.CreatedAt
	}
}
