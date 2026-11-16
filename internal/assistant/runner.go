package assistant

import "strings"

// TurnRequest is one prompt handed to a provider CLI.
type TurnRequest struct {
	// Instance is the assistant this turn belongs to, the one whose workspace
	// it runs in and whose instructions are rebuilt before it starts. A check
	// carries the assistant whose job it is.
	Instance string
	// SessionID is the provider session this turn belongs to. The first turn
	// creates it, every later turn resumes it.
	SessionID string
	// Resume selects resume over create. The service decides it by asking the
	// runner whether the session already exists, so a failed first turn that
	// still wrote provider state cannot make the retry collide.
	Resume bool
	// Title names the provider session on creation, so the coder's own session
	// list shows it under the assistant's name.
	Title string
	// Workdir is the instance's workspace, the directory the process runs in.
	Workdir string
	Prompt  string
	// Model is the model the turn runs on, the name the coder's CLI takes
	// behind its own flag. Empty leaves the choice to the CLI, which is what
	// every turn ran on before a model could be picked; which model a turn
	// gets is ModelFor's answer.
	Model string
}

// MaxSessionNameBytes bounds the name a provider session is created with. A
// instance title is written for the page and may be long; copilot refuses a
// name over 100 characters, so a turn hands over a name both CLIs take.
const MaxSessionNameBytes = 80

// SessionName cuts an assistant title down to a provider session name, on a
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

// BlockSeparator stands between two text blocks of one turn. A turn that works
// with tools answers in several blocks, what it says before a call and the
// answer after it, and every runner hands those over as one stream of deltas
// that is appended as it arrives, so without it the end of one block and the
// start of the next are welded into one word. It is the blank line markdown
// needs between two blocks, and a runner writes it only where the provider's
// own output says a block ended: never in front of a turn's first block, never
// behind its last, and never guessed from the text that is already there.
const BlockSeparator = "\n\n"

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
	// Defaults reads the coder's stored model defaults, the purpose fallback
	// behind an assistant's own pick, fresh on every turn. Nil means none.
	Defaults func() ModelDefaults
}

// ModelDefaults answers the coder's stored defaults, empty without a reading.
func (c CoderInfo) ModelDefaults() ModelDefaults {
	if c.Defaults == nil {
		return ModelDefaults{}
	}
	return c.Defaults()
}

// Coders resolves the coders that can answer a turn of this installation. Implemented
// outside this package, see the package comment on the import direction.
type Coders interface {
	Available() []CoderInfo
}

// Workdirs resolves the directory an instance's turns run in. The assistant
// workspace implements it.
type Workdirs interface {
	// Workdir is the workspace of one instance, created when it is missing.
	Workdir(instanceID string) (string, error)
}

// WorkdirTruster is the optional capability of a runner whose CLI stops a
// directory it has never seen on a trust dialog and keeps the answer in its
// own configuration. A turn runs in a workspace the CLI may never have seen,
// and a non interactive turn cannot answer a dialog, so the answer is written
// ahead of the start, the way the coder manager writes it for a terminal.
type WorkdirTruster interface {
	TrustWorkdir(dir string) error
}
