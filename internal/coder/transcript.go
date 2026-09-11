package coder

import "github.com/marein/dev-cockpit/internal/terminal"

// A reading is bounded twice, and the two bounds do different jobs.
//
// TranscriptPage is how many messages a sheet opens with. It is counted in
// messages because that is what a person scrolls through, and it is a first
// page rather than a limit: what it left out is said in the text and asking
// for more is one request. Sessions here run to tens of megabytes, so opening
// with everything is not an option, and cutting inside a message would leave
// something nobody can copy.
//
// TranscriptCap is the backstop: no answer is larger than this however many
// messages were asked for, because one message can be a whole file somebody
// pasted. It bounds what travels and what a browser has to hold, nothing else.
const (
	TranscriptPage = 50
	TranscriptCap  = 1 << 20
)

// Transcript is a session's recorded conversation as text: what was said,
// whole and in the order it was said. It is the source the copy sheet wants,
// because a terminal draws to a canvas and holds no text at all, and because a
// full screen program's picture is its layout, not its conversation: boxes,
// rules and a transcript cut to the width of somebody's window.
//
// Screen says the text is the terminal picture instead, which is what a coder
// without a record of its own leaves as the only source. A reader has to be
// told, the picture carries the coder's input line and whatever draft stands
// in it, and a draft is not a message. It is the same distinction Activity
// makes, for the same reason.
type Transcript struct {
	Text   string
	Screen bool
	// Dropped is how many messages the bounds left out at the top, so a reader
	// can say the conversation starts earlier and offer to fetch more.
	Dropped int
}

// Recording is what a coder answers with: its record as text, and how many
// messages the bounds left out at the top.
type Recording struct {
	Text    string
	Dropped int
}

// TranscriptReader is the optional capability of a coder to hand over a
// session's recorded conversation. A coder implements it when its CLI keeps a
// record it can read; for a coder that keeps none the manager falls back to
// the terminal picture. It sits next to ActivityReporter on purpose: same
// record, same optionality, different question. Activity asks what the session
// last did and may flatten and cut to answer it; this asks for what was said
// and may not.
//
// messages is how many of the newest recorded messages to keep; zero or less
// means every one of them. cap is the largest answer in runes, and a reading
// stops at it whatever the message count said; zero or less means no cap. A
// reading that left something out has to say so in the text, never end in a
// bare ellipsis.
type TranscriptReader interface {
	SessionTranscript(sessionID string, messages, cap int) (Recording, error)
}

// Transcript answers with the session's recorded conversation, or with the
// terminal picture when the coder keeps no record. The order is the same one
// Activity uses: the coder's own record first, because it is reliable, the
// screen only when there is nothing else.
func (s *Manager) Transcript(rawID string, messages, cap int) (Transcript, error) {
	id, err := terminal.ValidateIdentifier(rawID)
	if err != nil {
		return Transcript{}, err
	}
	if reader, ok := s.coder.(TranscriptReader); ok {
		recorded, err := reader.SessionTranscript(id, messages, cap)
		if err == nil {
			return Transcript{Text: recorded.Text, Dropped: recorded.Dropped}, nil
		}
	}
	running, err := s.ResolveRunning(id)
	if err != nil {
		return Transcript{}, err
	}
	text, err := s.tmux.CapturePane(running.TmuxSession, screenTranscriptLines)
	if err != nil {
		return Transcript{}, err
	}
	return Transcript{Text: text, Screen: true}, nil
}

// screenTranscriptLines is how much of a terminal the screen fallback reads.
// A pane on the alternate screen, which is what a full screen coder runs on,
// has no scrollback at all, so this only ever reaches past the screen for a
// coder that does not use one.
const screenTranscriptLines = 5000
