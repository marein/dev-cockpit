// Package coderlogin runs a coder CLI's own login headless and carries what
// it prints into the browser, so a coder can be logged in without a terminal.
// The shape follows the askpass bridge: the standing flow is server state, any
// signed-in page may show and answer it, and nothing sensitive survives it.
//
// The rules the whole package is built around:
//
//   - the CLI owns the credentials: the flow runs the CLI's login command and
//     shows what it printed, the cockpit stores no token and no code.
//   - a pasted code exists exactly once, on its way from the request body into
//     the child's stdin. It is never logged and never kept.
//   - one flow per coder at a time: a second start attaches to the running
//     flow, which is how a phone and a desktop can look at the same login.
//   - cancelling kills the child process, so no login ever waits on nobody.
package coderlogin

import (
	"errors"
	"strings"
	"sync"
	"time"
)

// State is a coder's login state as the cheap probe reads it: whether the CLI
// is logged in, as whom, and one line of detail worth showing next to it.
type State struct {
	LoggedIn bool   `json:"loggedIn"`
	Account  string `json:"account,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

// Reading is what the login process wrote so far, digested: the URL to open,
// the one-time code to show (copilot), whether the CLI now waits for a pasted
// code (claude), and its complaint about the last answer.
type Reading struct {
	URL     string
	Code    string
	Waiting bool
	Note    string
}

// Completer is the optional second half of a Login: a coder implements it
// when a finished login leaves something to record, so the first terminal
// after it starts on the task instead of on a first-run question. It runs
// once, right after the login process ended well.
type Completer interface {
	LoginCompleted()
}

// Login is the per coder half of the web login. A coder that implements it
// gets the account page and the login dialog; the flow around it is this
// package's.
type Login interface {
	// Command is the login process to run.
	Command() (name string, args []string)
	// TakesCode reports whether the flow waits for a code pasted back into
	// the process, the claude shape. Without it the process finishes on its
	// own once the user authorized in the browser, the copilot shape.
	TakesCode() bool
	// Read digests the process output so far. stderr is only what arrived
	// after the last answer, so an old complaint never outlives the retry it
	// was about.
	Read(stdout, stderr string) Reading
	// Probe answers the current login state, cheap enough for a page render.
	Probe() State
}

// stateTTL is how long a probe's answer serves page renders before it is
// asked again. A finished flow invalidates it, so a fresh login shows at once.
const stateTTL = 15 * time.Second

// endedKeep is how long a finished flow stays readable. The dialog polls once
// a second, so this is plenty to deliver the outcome; after that a stale
// failure must not greet whoever opens the page days later.
const endedKeep = 2 * time.Minute

type cachedState struct {
	state State
	at    time.Time
}

// Service owns the login flows and the probe cache, one of each per coder.
type Service struct {
	logins map[string]Login

	mu     sync.Mutex
	flows  map[string]*Flow
	states map[string]cachedState
}

// NewService builds the service for the coders that support the web login.
func NewService(logins map[string]Login) *Service {
	return &Service{
		logins: logins,
		flows:  map[string]*Flow{},
		states: map[string]cachedState{},
	}
}

// Supported reports whether the coder has a web login at all.
func (s *Service) Supported(coderID string) bool {
	_, ok := s.logins[coderID]
	return ok
}

// State answers the coder's login state, cached briefly. The probe runs
// outside the lock: it spawns a process, and a page render racing another
// costs one extra cheap probe instead of a queue.
func (s *Service) State(coderID string) State {
	login, ok := s.logins[coderID]
	if !ok {
		return State{}
	}
	s.mu.Lock()
	cached, have := s.states[coderID]
	s.mu.Unlock()
	if have && time.Since(cached.at) < stateTTL {
		return cached.state
	}
	state := login.Probe()
	s.mu.Lock()
	s.states[coderID] = cachedState{state: state, at: time.Now()}
	s.mu.Unlock()
	return state
}

// Invalidate drops the cached probe, so the next State asks the CLI again.
func (s *Service) Invalidate(coderID string) {
	s.mu.Lock()
	delete(s.states, coderID)
	s.mu.Unlock()
}

// Description is the JSON the login routes answer: the probe state plus the
// running or just finished flow, if any.
type Description struct {
	State
	Flow *FlowState `json:"flow,omitempty"`
}

// Describe answers the coder's login state and flow in one read.
func (s *Service) Describe(coderID string) Description {
	description := Description{State: s.State(coderID)}
	if flow := s.flow(coderID); flow != nil {
		state := flow.Snapshot()
		description.Flow = &state
	}
	return description
}

// flow answers the coder's flow, dropping one that finished long enough ago.
func (s *Service) flow(coderID string) *Flow {
	s.mu.Lock()
	defer s.mu.Unlock()
	flow := s.flows[coderID]
	if flow == nil {
		return nil
	}
	if endedAt, ended := flow.Ended(); ended && time.Since(endedAt) > endedKeep {
		delete(s.flows, coderID)
		return nil
	}
	return flow
}

// Start begins the coder's login flow. A flow already running is the answer
// rather than an error: the second device attaches and both show the same
// login.
func (s *Service) Start(coderID string) error {
	login, ok := s.logins[coderID]
	if !ok {
		return errors.New("This coder has no web login.")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if flow := s.flows[coderID]; flow != nil {
		if _, ended := flow.Ended(); !ended {
			return nil
		}
	}
	flow, err := startFlow(login, func() { s.Invalidate(coderID) })
	if err != nil {
		return errors.New("The login could not start: " + err.Error())
	}
	s.flows[coderID] = flow
	return nil
}

// Answer hands the pasted code to the coder's waiting flow.
func (s *Service) Answer(coderID, code string) error {
	flow := s.flow(coderID)
	if flow == nil {
		return errors.New("No login is running for this coder.")
	}
	return flow.Answer(code)
}

// Cancel ends the coder's running flow, killing the child process. Without a
// flow it does nothing: cancelling twice is the same as cancelling once.
func (s *Service) Cancel(coderID string) {
	if flow := s.flow(coderID); flow != nil {
		flow.Cancel()
	}
}

// LastLine is the last non-empty line of text, terminal escapes stripped and
// capped to one message's worth. Coders use it to read a CLI complaint, the
// flow uses it for the words a failed login ends with.
func LastLine(text string) string {
	lines := strings.Split(StripEscapes(text), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return clampLine(line)
		}
	}
	return ""
}

// maxLine bounds what a CLI line may put into a dialog, the askpass bound.
const maxLine = 500

func clampLine(line string) string {
	runes := []rune(line)
	if len(runes) <= maxLine {
		return line
	}
	return string(runes[:maxLine]) + "…"
}

// StripEscapes removes ANSI escape and OSC sequences, so a hyperlinked or
// colored line reads as its text.
func StripEscapes(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c != 0x1b {
			if c != 0x07 {
				b.WriteByte(c)
			}
			continue
		}
		i++
		if i >= len(text) {
			break
		}
		switch text[i] {
		case '[':
			for i++; i < len(text); i++ {
				if text[i] >= 0x40 && text[i] <= 0x7e {
					break
				}
			}
		case ']':
			for i++; i < len(text); i++ {
				if text[i] == 0x07 {
					break
				}
				if text[i] == 0x1b && i+1 < len(text) && text[i+1] == '\\' {
					i++
					break
				}
			}
		}
	}
	return b.String()
}
