package assistant

import "sync"

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
	// parser.
	FrameHTML = "html"
	// FrameTool reports that the provider is working with a tool.
	FrameTool = "tool"
	// FrameEnd closes a generation, carrying the final message state.
	FrameEnd = "end"
	// FrameMessage announces a message that appeared without a generation the
	// page was following: a wake writing its report, or a message that queued
	// while a turn ran. The client pulls that one message and appends or
	// replaces it, and touches nothing else, because a chat answer may be
	// streaming at the same time.
	FrameMessage = "message"
	// FrameGone announces a message that was removed while it waited, so every
	// open page drops its bubble.
	FrameGone = "gone"
	// FramePing proves the stream is alive to a browser that cannot see the SSE
	// keepalive, which is a comment and fires no event. Silence in the middle
	// of an answer is the normal case, thinking sends nothing at all, so only a
	// missing life sign says the socket died. It carries nothing, the client
	// stamps it and drops it, and it never travels the hub: the stream handler
	// writes it on a beat of its own.
	FramePing = "ping"
)

// StreamEvent is one frame on a conversation's own SSE stream. Conversation text never goes to
// the app wide event bus, only the coarse conversations event does.
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
}

// subBuffer is generous on purpose: a fast provider can emit hundreds of small
// deltas while a slow client is still reading, and dropping one would corrupt
// the text the browser assembles.
const subBuffer = 512

type subscriber struct {
	ch     chan StreamEvent
	closed bool
}

// live is the in-memory state of one conversation's stream: its subscribers and, while
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

// hub fans generation events out to the connected conversation pages.
type hub struct {
	mu            sync.Mutex
	conversations map[string]*live
}

func newHub() *hub { return &hub{conversations: map[string]*live{}} }

// subscribe attaches a listener and returns the current in-flight state in the
// same critical section. Doing both atomically is what lets a reconnecting
// page resume mid answer without a replay buffer: it either gets the running
// text plus every later delta, or no state at all and a FrameStart when the
// next generation begins.
func (h *hub) subscribe(conversationID string) (StreamEvent, bool, <-chan StreamEvent, func()) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.conversations[conversationID]
	if l == nil {
		l = &live{subs: map[*subscriber]struct{}{}}
		h.conversations[conversationID] = l
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
	return snapshot, l.running, sub.ch, func() { h.unsubscribe(conversationID, sub) }
}

func (h *hub) unsubscribe(conversationID string, sub *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.conversations[conversationID]
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
		delete(h.conversations, conversationID)
	}
}

// publish updates the in-flight state and fans the frame out. A subscriber
// that cannot keep up is closed instead of skipped: its page reconnects and
// resnapshots, which is correct, while a dropped delta would leave a hole in
// the text.
func (h *hub) publish(conversationID string, ev StreamEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	l := h.conversations[conversationID]
	if l == nil {
		l = &live{subs: map[*subscriber]struct{}{}}
		h.conversations[conversationID] = l
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
		delete(h.conversations, conversationID)
	}
}
