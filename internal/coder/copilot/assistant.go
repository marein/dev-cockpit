package copilot

import (
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
)

// assistantFlags are the flags this runner cannot work without.
var assistantFlags = []string{"--prompt", "--output-format", "--session-id", "--resume", "--allow-all-tools"}

// runner holds the store itself, not the repository interface: a turn's context
// reading is not on copilot's output at all, it stands in the session's own
// event log, and reading that is this store's job.
type runner struct {
	sessions *sessionRepository
}

// AssistantRunner returns the conversation capability, or nil when the
// installed copilot cannot do what a turn needs. A pass holds for the process,
// a miss is probed again after a pause, see coder.CapabilityProbe.
func (p *Coder) AssistantRunner() assistant.Runner {
	if !p.assistantProbe.Passed() {
		return nil
	}
	return p.runner
}

func (p *Coder) probeAssistant() bool {
	if missing := missingAssistantFlags(); len(missing) > 0 {
		log.Printf("copilot conversations disabled, the installed CLI has no %s", strings.Join(missing, ", "))
		return false
	}
	p.runner = &runner{sessions: p.sessions}
	return true
}

func missingAssistantFlags() []string {
	help := clirun.Run("copilot", "--help")
	if help.Err != nil && help.Stdout == "" {
		return []string{"a usable --help output"}
	}
	text := help.Stdout + help.Stderr
	var missing []string
	for _, flag := range assistantFlags {
		if !strings.Contains(text, flag) {
			missing = append(missing, flag)
		}
	}
	return missing
}

func (r *runner) SessionExists(sessionID string) bool {
	for _, s := range r.sessions.List() {
		if s.SessionID == sessionID {
			return true
		}
	}
	return false
}

func (r *runner) DeleteSession(sessionID string) error {
	return r.sessions.DeleteSession(sessionID)
}

// Command builds one non-interactive copilot turn. A conversation has the same tools as a
// coder terminal, including the ones that change files and run commands. A
// non-interactive run cannot ask for a decision, so the approval flags carry
// what an interactive session would confirm; without them every tool that
// needs a decision fails instead of doing the work.
//
// The prompt is the value of -p and never a positional, which is what keeps a
// prompt starting with a dash text: copilot's -p takes an argument it requires,
// so it consumes the next word whatever it looks like (measured on GitHub
// Copilot CLI 1.0.78, `copilot -p --output-format json` takes the flag itself as
// the prompt). An end of options separator would therefore become the prompt, so
// this argv deliberately carries none.
func (r *runner) Command(req assistant.TurnRequest) (assistant.Command, error) {
	args := []string{"-p", req.Prompt}
	if req.Resume {
		args = append(args, "--resume", req.SessionID)
	} else {
		args = append(args, "--session-id", req.SessionID)
		if name := assistant.SessionName(req.Title); name != "" {
			args = append(args, "--name", name)
		}
	}
	// --allow-all-paths lifts copilot's path verification, which checks every
	// path against the working directory and its trusted folders. An
	// assistant turn has to reach the projects, the cockpit binary and its
	// own workspace, none of which live in its working directory, so every
	// turn carries the flag.
	args = append(args,
		"--allow-all-tools",
		"--allow-all-urls",
		"--allow-all-paths",
		"--output-format", "json",
		"--log-level", "none",
		"--no-color",
	)
	return assistant.Command{Name: "copilot", Args: args}, nil
}

// Parse reads copilot's JSONL output. It is called again when a turn is picked
// up after a restart, so it carries no state from the start of the turn.
func (r *runner) Parse(sessionID string, events chan<- assistant.Event) assistant.Parser {
	return &copilotParser{sessionID: sessionID, events: events, sessions: r.sessions, delivered: map[string]bool{}}
}

// copilotParser turns the documented JSONL events into conversation events. It
// reads past anything it cannot decode, noting it in the server log; what
// stays strict is what the turn's outcome hangs on, the result record and
// Finish.
type copilotParser struct {
	sessionID string
	events    chan<- assistant.Event
	sessions  *sessionRepository
	// delivered tracks which assistant messages already arrived as deltas, so
	// the final full message record is not appended a second time. A provider
	// version that stops sending deltas still produces the complete answer.
	delivered map[string]bool
	sawResult bool
	// sentText is whether this turn has put any text out, and lastMessageID
	// which message that text belonged to. copilot carries the id on every
	// record and moves it when it starts a new message, so the seam between two
	// markdown blocks is read out of the output and never derived from the text.
	sentText      bool
	lastMessageID string
}

// copilotHead is the first pass over a record: only what routes it. Everything
// else is decoded per type, so a record whose payload has a shape this version
// does not know cannot fail records of every other type with it.
type copilotHead struct {
	Type string `json:"type"`
}

// copilotDataRecord carries the message and tool payloads, the records that
// stream an answer.
type copilotDataRecord struct {
	Data struct {
		MessageID    string `json:"messageId"`
		DeltaContent string `json:"deltaContent"`
		Content      string `json:"content"`
		ToolName     string `json:"toolName"`
	} `json:"data"`
}

// copilotResultRecord is the record that closes a turn.
type copilotResultRecord struct {
	SessionID string `json:"sessionId"`
	ExitCode  *int   `json:"exitCode"`
}

func (p *copilotParser) Line(line []byte) error {
	var head copilotHead
	if err := json.Unmarshal(line, &head); err != nil {
		p.skipUnreadable(line, err)
		return nil
	}
	switch head.Type {
	case "assistant.message_delta", "assistant.message", "tool.execution_start":
		var rec copilotDataRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			p.skipUnreadable(line, err)
			return nil
		}
		switch head.Type {
		case "assistant.message_delta":
			if rec.Data.DeltaContent == "" {
				return nil
			}
			p.delivered[rec.Data.MessageID] = true
			p.emitText(rec.Data.MessageID, rec.Data.DeltaContent)
		case "assistant.message":
			if rec.Data.Content == "" || p.delivered[rec.Data.MessageID] {
				return nil
			}
			p.delivered[rec.Data.MessageID] = true
			p.emitText(rec.Data.MessageID, rec.Data.Content)
		case "tool.execution_start":
			p.events <- assistant.Event{Kind: assistant.EventTool, Text: rec.Data.ToolName}
		}
	case "result":
		var rec copilotResultRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// The one record the outcome hangs on. Skipping it leaves sawResult
			// unset, so Finish names the broken turn; inventing an outcome out
			// of a line nobody could read would be worse.
			p.skipUnreadable(line, err)
			return nil
		}
		p.sawResult = true
		if rec.SessionID != "" && rec.SessionID != p.sessionID {
			log.Printf("copilot assistant: expected session %s, got %s", p.sessionID, rec.SessionID)
			return errors.New("The coder answered in a different conversation, so this turn was dropped.")
		}
		if rec.ExitCode != nil && *rec.ExitCode != 0 {
			return errors.New("The coder could not finish this answer.")
		}
	}
	return nil
}

// emitText sends one piece of assistant text, with the block separator in front
// of it when it belongs to another message than the text that went out before.
// The first message of a turn gets nothing in front of it and the last gets
// nothing behind it, and a version that stops carrying the id keeps the plain
// appended answer it had.
func (p *copilotParser) emitText(messageID, text string) {
	if p.sentText && messageID != p.lastMessageID {
		p.events <- assistant.Event{Kind: assistant.EventDelta, Text: assistant.BlockSeparator}
	}
	p.lastMessageID = messageID
	p.sentText = true
	p.events <- assistant.Event{Kind: assistant.EventDelta, Text: text}
}

// skipUnreadable notes a line this parser could not decode and lets the turn
// read on. The line travels into the log in shortened form, so the next time a
// record like it arrives the log says what it was. Ending the turn here would
// kill a conversation over a record copilot sends in passing while the run
// itself carries on to its result.
func (p *copilotParser) skipUnreadable(line []byte, err error) {
	log.Printf("copilot assistant: unreadable output record: %v: %s", err, assistant.UnreadableLine(line))
}

func (p *copilotParser) Finish() error {
	// The context reading is read here and nowhere else: copilot writes it into
	// the session's event log when the run shuts down, and a turn is one
	// non-interactive run, so the record exists exactly once per turn and only
	// after the process is gone. Nothing of it reaches standard output.
	p.reportUsage()
	if !p.sawResult {
		return errors.New("The coder stopped before it finished the answer.")
	}
	return nil
}

// Diagnose reads what copilot said when it never got going. A run without a
// login writes nothing to standard output at all and prints its whole complaint
// on standard error, so the general path is the only one there is here.
func (p *copilotParser) Diagnose(err error, stderr string) error {
	if assistant.LooksLikeLogin(stderr) {
		return assistant.ErrNotLoggedIn
	}
	return nil
}

// reportUsage sends what the finished run recorded about its context. A run
// that recorded nothing readable reports nothing, so the page keeps the last
// number it had instead of showing a guess.
func (p *copilotParser) reportUsage() {
	if p.sessions == nil {
		return
	}
	usage, ok := p.sessions.contextUsage(p.sessionID)
	if !ok {
		return
	}
	p.events <- assistant.Event{Kind: assistant.EventUsage, Usage: &usage}
}
