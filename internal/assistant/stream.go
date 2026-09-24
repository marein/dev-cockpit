package assistant

import (
	"sync"
	"time"
)

// Stream frame kinds. Every frame carries the run and message id it belongs
// to, so a stream that outlived a cancelled turn can never write into the
// message of a newer retry.
const (
	// FrameStart opens a generation. Clients reset their buffer on it.
	FrameStart = "start"
	// FrameDelta appends assistant text.
	FrameDelta = "delta"
	// FrameHTML carries the answer so far, rendered on the server. Markdown is
	// only additive as long as nothing is half open (a fence, a table, an
	// emphasis), so the browser never parses the stream itself: it shows the
	// raw text as it arrives and replaces it with the rendered prefix whenever
	// one of these lands, which also keeps model output out of any client side
	// parser. The prefix carries RenderMark where the text after it goes.
	FrameHTML = "html"
	// FrameTool reports that the provider is working with a tool.
	FrameTool = "tool"
	// FrameEnd closes a generation, carrying the final message state.
	FrameEnd = "end"
	// FrameMessage announces a message that appeared without a generation the
	// page was following: a check writing its report, or a prompt another
	// device sent. The client pulls that one message and appends or replaces
	// it, and touches nothing else, because a chat answer may be streaming at
	// the same time.
	FrameMessage = "message"
	// FrameGone announces a message that was taken out of the transcript, so
	// every open page drops its bubble. A retry is what does that: the answer
	// it replaces leaves.
	FrameGone = "gone"
	// FramePing proves the stream is alive to a browser that cannot see the SSE
	// keepalive, which is a comment and fires no event. Silence in the middle
	// of an answer is the normal case, thinking sends nothing at all, so only a
	// missing life sign says the socket died. It carries nothing, the client
	// stamps it and drops it, and it never travels the hub: the stream handler
	// writes it on a beat of its own.
	FramePing = "ping"
	// FrameModels announces that the assistant's model picks moved: the ring's
	// three selects on every open page of it take the fresh picks, so a pick
	// set from the CLI or on another tab shows without a reload. It is about
	// the instance and carries no message id, SetModels publishes it, and the
	// stream handler writes one on every connect as the stream's own snapshot,
	// so a page whose socket was down while a pick moved catches up with the
	// reconnect.
	FrameModels = "models"
)

// RenderMark is the one character the streaming prefix is rendered with, at
// its very end, and the page puts the text that arrived since that render
// where the mark came out. Only a Markdown parser can say whether the next
// character continues the open paragraph, list item or code block or opens a
// block of its own, so the renderer that produced the HTML says it: the page
// looks for a character instead of parsing model output, which it never does.
// It is a word joiner, invisible, so a render that swallows it (a table row
// drops what follows its last bar, raw HTML is dropped whole) leaves nothing
// on screen and the page falls back to putting the text behind the prefix,
// where it stood before.
const RenderMark = "\u2060"

// StreamEvent is one frame on an assistant's own SSE stream. Instance text never goes to
// the app wide event bus, only the coarse instances event does.
type StreamEvent struct {
	Kind      string `json:"kind"`
	RunID     string `json:"runId,omitempty"`
	MessageID string `json:"messageId,omitempty"`
	Text      string `json:"text,omitempty"`
	HTML      string `json:"html,omitempty"`
	State     string `json:"state,omitempty"`
	Error     string `json:"error,omitempty"`
	// Context rides the end frame and is how full the coder's context window
	// stands, in percent. It is left out when the turn reported nothing or the
	// model's window is unknown, and the page then leaves its ring as it is.
	Context int `json:"context,omitempty"`
	// Models rides the models frame: the picks as stored and the moment they
	// were written.
	Models *ModelPicks `json:"models,omitempty"`
}

// ModelPicks is what the models frame carries and what a save of the ring's
// menu answers alike, so the page applies one shape from both ways in: the
// assistant the picks belong to, the three picks as stored, empty where the
// assistant follows the default, and the moment they were written, which is
// what lets a page drop a reading older than the one it applied, the way a
// draft's answer carries its own stamp. The frame travels the one
// assistant's own hub channel like every message frame, so no other
// assistant's page ever receives it, and the id is the page's own check on
// top of that: it applies a reading only for the assistant it shows.
type ModelPicks struct {
	Assistant string    `json:"assistant"`
	Chat      string    `json:"chat"`
	Check     string    `json:"check"`
	Trigger   string    `json:"trigger"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// ModelPicks answers the instance's picks the way the models frame carries
// them.
func (i Instance) ModelPicks() ModelPicks {
	return ModelPicks{Assistant: i.ID, Chat: i.Model, Check: i.CheckModel, Trigger: i.TriggerModel, UpdatedAt: i.UpdatedAt}
}

// subBuffer is generous on purpose: a fast provider can emit hundreds of small
// deltas while a slow client is still reading, and dropping one would corrupt
// the text the browser assembles.
const subBuffer = 512

type subscriber struct {
	ch     chan StreamEvent
	closed bool
}

// live is the in-memory state of one instance's stream: its subscribers and, while
// a generation runs, the text delivered so far.
type live struct {
	subs      map[*subscriber]struct{}
	running   bool
	runID     string
	messageID string
	text      string
	// html is the last rendered prefix and renderedLen how much of text it
	// covers, so a page connecting mid answer gets the formatted part plus the
	// raw tail, and never the tail twice.
	html        string
	renderedLen int
}

// hub fans generation events out to the connected instance pages.
type hub struct {
	mu        sync.Mutex
	instances map[string]*live
}

func newHub() *hub { return &hub{instances: map[string]*live{}} }

// subscribe attaches a listener and returns the current in-flight state in the
// same critical section. Doing both atomically is what lets a reconnecting
// page resume mid answer without a replay buffer: it either gets the running
// text plus every later delta, or no state at all and a FrameStart when the
// next generation begins.
func (h *hub) subscribe(instanceID string) (StreamEvent, bool, <-chan StreamEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.instances[instanceID]
	if l == nil {
		l = &live{subs: map[*subscriber]struct{}{}}
		h.instances[instanceID] = l
	}
	sub := &subscriber{ch: make(chan StreamEvent, subBuffer)}
	l.subs[sub] = struct{}{}

	snapshot := StreamEvent{}
	if l.running {
		tail := l.text
		if l.renderedLen > 0 && l.renderedLen <= len(l.text) {
			tail = l.text[l.renderedLen:]
		}
		snapshot = StreamEvent{
			Kind:      FrameStart,
			RunID:     l.runID,
			MessageID: l.messageID,
			Text:      tail,
			HTML:      l.html,
			State:     string(StateStreaming),
		}
	}
	return snapshot, l.running, sub.ch, func() { h.unsubscribe(instanceID, sub) }
}

func (h *hub) unsubscribe(instanceID string, sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.instances[instanceID]
	if l == nil {
		return
	}
	if _, ok := l.subs[sub]; ok {
		delete(l.subs, sub)
		if !sub.closed {
			sub.closed = true
			close(sub.ch)
		}
	}
	if len(l.subs) == 0 && !l.running {
		delete(h.instances, instanceID)
	}
}

// publish updates the in-flight state and fans the frame out. A subscriber
// that cannot keep up is closed instead of skipped: its page reconnects and
// resnapshots, which is correct, while a dropped delta would leave a hole in
// the text.
func (h *hub) publish(instanceID string, ev StreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.instances[instanceID]
	if l == nil {
		l = &live{subs: map[*subscriber]struct{}{}}
		h.instances[instanceID] = l
	}
	switch ev.Kind {
	case FrameStart:
		l.running = true
		l.runID = ev.RunID
		l.messageID = ev.MessageID
		l.text = ev.Text
		l.html = ev.HTML
		l.renderedLen = 0
	case FrameDelta:
		l.text += ev.Text
	case FrameHTML:
		l.html = ev.HTML
		l.renderedLen = len(l.text)
	case FrameEnd:
		l.running = false
		l.runID = ""
		l.messageID = ""
		l.text = ""
		l.html = ""
		l.renderedLen = 0
	}
	for sub := range l.subs {
		if sub.closed {
			continue
		}
		select {
		case sub.ch <- ev:
		default:
			sub.closed = true
			close(sub.ch)
			delete(l.subs, sub)
		}
	}
	if len(l.subs) == 0 && !l.running {
		delete(h.instances, instanceID)
	}
}
