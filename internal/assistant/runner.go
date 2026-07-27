package assistant

import "strings"

// TurnRequest is one prompt handed to a provider CLI.
type TurnRequest struct {
	// SessionID is the provider session this turn belongs to. The first turn
	// creates it, every later turn resumes it.
	SessionID string
	// Resume selects resume over create. The service decides it by asking the
	// runner whether the session already exists, so a failed first turn that
	// still wrote provider state cannot make the retry collide.
	Resume bool
	// Title names the provider session on creation, so a transferred conversation
	// shows up as a coder terminal with the conversation's title.
	Title string
	// Workdir is the validated project directory the process runs in.
	Workdir string
	Prompt  string
}

// MaxSessionNameBytes bounds the name a provider session is created with. A
// conversation title is written for the page and may be long; copilot refuses a
// name over 100 characters, so a turn hands over a name both CLIs take.
const MaxSessionNameBytes = 80

// SessionName cuts a conversation title down to a provider session name, on a
// rune boundary so a multi byte title never breaks in the middle of a
// character.
func SessionName(title string) string {
	name := strings.TrimSpace(title)
	if len(name) <= MaxSessionNameBytes {
		return name
	}
	cut := 0
	for i := range name {
		if i > MaxSessionNameBytes {
			break
		}
		cut = i
	}
	return strings.TrimSpace(name[:cut])
}

// EventKind classifies a runner event.
type EventKind string

const (
	// EventDelta carries assistant text to append.
	EventDelta EventKind = "delta"
	// EventTool marks that the provider started working with a tool. It
	// carries no arguments, the UI only shows that something is happening.
	EventTool EventKind = "tool"
	// EventUsage reports how full the coder's context window stands. It arrives
	// at most once per turn, at its end, because that is when the provider says
	// it; a later one replaces an earlier one.
	EventUsage EventKind = "usage"
	// EventError ends the turn with a curated, user facing message.
	EventError EventKind = "error"
)

// Event is one structured message from a provider run. The channel closing
// without an EventError means the turn completed.
type Event struct {
	Kind EventKind
	Text string
	Err  error
	// Usage carries the context reading of an EventUsage and is nil otherwise.
	Usage *ContextUsage
}

// Runner describes one provider CLI in non-interactive mode. Implementations
// live next to their coder (internal/coder/<coder>/assistant.go) and are the
// only place that knows provider flags and output shapes.
//
// A runner does not own the process. It says what to run and how to read what
// comes back, and this package starts it detached, keeps its raw output on disk
// and reads it, which is what lets a turn outlive the server: a process this
// server never started is attached with the same two calls.
type Runner interface {
	// Command builds the process for one turn.
	Command(req TurnRequest) (Command, error)
	// Parse returns a fresh parser for this coder's raw output. It is called
	// again when a turn is picked up after a restart, so it may not depend on
	// anything but its arguments.
	Parse(sessionID string, events chan<- Event) Parser
	// SessionExists reports whether the provider still holds this session.
	SessionExists(sessionID string) bool
	// DeleteSession removes the provider side conversation.
	DeleteSession(sessionID string) error
}

// CoderInfo describes one able to answer a turn coder.
type CoderInfo struct {
	ID     string
	Label  string
	Runner Runner
}

// Coders resolves the coders that can answer a turn of this installation. Implemented
// outside this package, see the package comment on the import direction.
type Coders interface {
	Available() []CoderInfo
}

// Projects validates the project directory a conversation is bound to.
type Projects interface {
	ValidatePath(raw string) (string, error)
	ProjectNameFor(path string) string
}

// Terminals promotes a conversation's provider session into a coder terminal. The web
// layer implements it over the coder managers.
type Terminals interface {
	// ResumeReserved starts a terminal on an existing provider session and
	// returns its identifier.
	ResumeReserved(coderID, sessionID, projectPath, title string) (string, error)
	// Stop kills a terminal again, used to roll a failed transfer back.
	Stop(coderID, terminalID string) error
}
