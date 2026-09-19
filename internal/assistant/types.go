package assistant

import (
	"strings"
	"time"
)

// Role distinguishes the three message authors: the user, the assistant, and
// the cockpit itself, which writes a note when something happened that nobody
// typed and the assistant did not answer, a check's report or an event.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	// RoleCockpit is the third kind of message, a note: the cockpit speaks. It
	// is never the user's words and never the assistant's, see Note.
	RoleCockpit Role = "cockpit"
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
	// sits in the transcript, deletable until the assistant flushes every
	// waiting message as one new turn.
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
	// MaxQueuedMessages bounds what may wait while a turn runs. The flush joins
	// every waiting message into one prompt, and that prompt has to stay within
	// what a process argument accepts.
	MaxQueuedMessages = 20
	// MaxTitleRunes bounds an assistant's name.
	MaxTitleRunes = 120
	// PreviewRunes is how much of a message a preview carries, wherever one
	// stands: the body of a push, the line a toast and the bell's list hold,
	// and the line the thread folds a note and a pushed answer under. It is
	// written for the narrowest of them, a phone's notification body, which
	// gives it three or four lines; the thread reads the same words, so the
	// message that brought somebody here is what stands in front of them when
	// they arrive. One number, because two would put more in one surface than
	// in the other for no reason anybody could name.
	PreviewRunes = 140
	// DefaultTitle names an assistant that has not seen a prompt yet. The first
	// prompt renames it, and the user may rename it at any time.
	DefaultTitle = "New assistant"
)

// Summary is one index entry. It deliberately carries no messages: the list
// page renders from the index alone, so opening it never loads a transcript.
type Summary struct {
	ID            string    `json:"id"`
	Title         string    `json:"title"`
	CoderID       string    `json:"coderId"`
	CreatedAt     time.Time `json:"createdAt"`
	LastMessageAt time.Time `json:"lastMessageAt"`
	// Preview is the opening of the last assistant answer, bounded for the list.
	Preview string `json:"preview"`
	// Running marks an assistant whose last answer is still being written. It is
	// the working state, not a fault, and it is the one the list shows while a turn
	// is under way.
	Running bool `json:"running"`
	// Unfinished marks an assistant whose last turn stopped before it was done,
	// cancelled, failed or interrupted. A turn that is still running is never one of
	// them, that is Running, so the two never stand at the same time and neither has
	// to be guessed from the other.
	Unfinished bool `json:"unfinished"`
	// MessageCount is the number of stored messages, used for the list subtitle.
	MessageCount int `json:"messageCount"`
}

// Instance is one complete instance with its transcript.
type Instance struct {
	Summary
	// NativeSessionID is the provider session this instance drives. It equals
	// the instance id.
	NativeSessionID string    `json:"nativeSessionId"`
	UpdatedAt       time.Time `json:"updatedAt"`
	Messages        []Message `json:"messages"`
	// Context is how full the coder's context window stood at the end of the
	// last turn that reported it. It lives on the instance, not on a
	// message: it describes the whole instance as it stands, and a reader
	// coming back has to see the number without a turn running.
	Context *ContextUsage `json:"context,omitempty"`
}

// Draft is an unsent prompt with the files that were already uploaded for it.
// It belongs to the assistant, not to the browser that typed it, so the same
// words are there after a page change and on the next device. The files travel
// with the text, otherwise coming back shows a message whose attachments are
// gone. It lives in a file of its own, see DraftStore.
type Draft struct {
	Text        string       `json:"text,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	UpdatedAt   time.Time    `json:"updatedAt,omitempty"`
}

// Empty reports whether there is nothing to restore.
func (d Draft) Empty() bool { return d.Text == "" && len(d.Attachments) == 0 }

// Same reports whether a draft would store exactly what is already stored,
// which is how a repeated flush writes nothing and wakes nobody.
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
// instance's own files directory and hands the coder the absolute path, so a coder
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
	// Note is set on a message the cockpit wrote, the third role: a check's
	// report. It says where the message came from and carries its headline;
	// Content is the body. A check's prompt is never stored, only what it
	// concluded, which is why nothing in a transcript can look like something
	// the user said.
	Note *Note `json:"note,omitempty"`
	// Auto marks an answer that was started without the user: a reaction to an
	// event the assistant set a trigger on, run in a session of its own and
	// pushed into the thread. Origin says which event and which task, the page
	// shows it as a folded header over the answer.
	Auto   bool  `json:"auto,omitempty"`
	Origin *Note `json:"origin,omitempty"`
	// Wake is the shape a check's report was stored in before notes existed.
	// It is read once, on load, and turned into a Note (see Store.load);
	// nothing writes it any more. TODO(v2.0.0): drop the key and the type.
	Wake *WakeNote `json:"wake,omitempty"`
}

// Note is what the cockpit says when it writes into a thread: where the
// message comes from and one line to read it by. The body is the message's
// Content. Source says what wrote it: a check's report (NoteCheck, the verdict
// it concluded in Verdict) is a note of its own, and an event that fired a
// trigger (NoteEvent, the trigger in Trigger, its task in Task and the events
// it carries in Count) is the origin a pushed answer carries, never a note in
// the thread.
type Note struct {
	Source string `json:"source"`
	// Headline is the one line this note is read by, and it is the one every
	// surface with room for a single line takes: the notification and its
	// push, the header over a pushed answer, the line a chat prompt is
	// preceded by. A trigger the user named stands here under that name, and
	// what happened moves into Event, so the name reaches every one of them
	// at once, which is what a name is for.
	Headline string `json:"headline"`
	// Event is what fired a named trigger, the headline this note would have
	// carried without a name. Empty where the headline already says it, so a
	// surface with room for both renders it exactly where the name pushed
	// something out, see Occasion.
	Event string `json:"event,omitempty"`
	// Verdict is what a check concluded, for a check's report; an event note
	// carries the kind of its event here (done, blocked, ended, tick, ...).
	Verdict string `json:"verdict,omitempty"`
	// Terminal, Name and Project say which coder the note is about, written
	// down at the time: the job is gone by the time somebody reads it, or the
	// terminal is steered again and a lookup would answer with its successor.
	Terminal string `json:"terminal,omitempty"`
	Name     string `json:"name,omitempty"`
	Project  string `json:"project,omitempty"`
	// Trigger is the trigger an event fired, Task what that trigger asks the
	// assistant to do, Count how many events the reaction bundles (a batch
	// window turns several into one turn).
	Trigger string `json:"trigger,omitempty"`
	Task    string `json:"task,omitempty"`
	Count   int    `json:"count,omitempty"`
}

// The note sources.
const (
	NoteCheck = "check"
	NoteEvent = "event"
)

// Occasion is what happened, for the readers with room for the name and it
// both: the event's own headline, which is the headline itself wherever no
// name pushed it out.
func (n Note) Occasion() string {
	if n.Event != "" {
		return n.Event
	}
	return n.Headline
}

// IsNote reports whether the cockpit wrote this message.
func (m Message) IsNote() bool { return m.Role == RoleCockpit && m.Note != nil }

// Speakable reports whether this message can be read aloud: the user did not
// write it, it holds words, and it is finished. A note is one of them, a
// check's report is spoken like every other message the reader did not ask
// for. It is the one reading of that question: the transcript renders the
// speaker from it and the audio route asks it again for a page from before a
// settings change, so a button that stands and a route that answers cannot
// disagree.
func (m Message) Speakable() bool {
	return m.Role != RoleUser && m.Content != "" && m.State == StateComplete
}

// carries answers whether a message holds the word, compared case
// insensitively the way `job-list --contains` compares; the word arrives
// already lowered and trimmed. Searched is what a reader of the thread sees:
// the body, and the headline standing over it. A headline is the one text
// that is nowhere else, a schedule's tick carries its spec there and a job's
// note the coder and the verdict, and it is what rides into the next prompt,
// so it is what a note is remembered by. The task behind a fold is left out
// on purpose: a trigger's task is the same words on every fire and would make
// one trigger a hit on every answer it ever pushed, which is not what tells
// two of them apart; it is searched where it is written, on the trigger.
func (m Message) carries(word string) bool {
	fields := []string{m.Content}
	for _, note := range []*Note{m.Note, m.Origin} {
		if note != nil {
			fields = append(fields, note.Headline)
		}
	}
	for _, field := range fields {
		if strings.Contains(strings.ToLower(field), word) {
			return true
		}
	}
	return false
}

// noteFromWake turns a report stored before notes existed into the note it
// is: a check's report, headline built the way recordWake builds it now.
// TODO(v2.0.0): goes with WakeNote.
func noteFromWake(w *WakeNote) *Note {
	return &Note{
		Source:   NoteCheck,
		Headline: checkHeadline(w.Verdict, w.Name, w.Terminal),
		Verdict:  w.Verdict,
		Terminal: w.Terminal,
		Name:     w.Name,
		Project:  w.Project,
	}
}

// checkHeadline is the one line a check's report is read by: the verdict in
// capitals and the job's name, "DONE: readme-task".
func checkHeadline(verdict, name, terminal string) string {
	word := strings.ToUpper(strings.TrimSpace(verdict))
	if word == "" {
		word = "CHECKED"
	}
	if strings.TrimSpace(name) == "" {
		name = terminal
	}
	return word + ": " + strings.TrimSpace(name)
}

// WakeNote says which terminal a check was about and what it concluded. It is
// the stored shape of a report from before notes existed, read on load and
// written nowhere. TODO(v2.0.0): drop with Message.Wake.
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
}

// Last returns the final message of the transcript.
func (c Instance) Last() (Message, bool) {
	if len(c.Messages) == 0 {
		return Message{}, false
	}
	return c.Messages[len(c.Messages)-1], true
}

// Idle reports whether the instance currently has no unfinished assistant turn.
func (c Instance) Idle() bool {
	last, ok := c.Last()
	return !ok || last.State.Settled()
}

// summarize refreshes the index fields derived from the transcript.
func (c *Instance) summarize() {
	c.MessageCount = len(c.Messages)
	c.Preview = ""
	c.Running = false
	c.Unfinished = false
	for i := len(c.Messages) - 1; i >= 0; i-- {
		m := c.Messages[i]
		if m.Role != RoleAssistant {
			continue
		}
		c.Preview = preview(m.Content)
		c.Running = !m.State.Settled()
		c.Unfinished = m.State.Settled() && m.State != StateComplete
		break
	}
	if last, ok := c.Last(); ok {
		c.LastMessageAt = last.CreatedAt
	}
}
