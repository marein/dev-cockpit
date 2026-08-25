package opencode

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
)

// assistantFlags are the flags a turn cannot work without, checked once
// against `opencode run --help`. A version that lost one of them loses the
// conversations only, its terminal integration is untouched.
var assistantFlags = []string{"--session", "--format", "--auto"}

// A conversation lives in a provider session the service addresses by an id
// it chose, and opencode cannot create a session under a caller's id. So the
// first turn creates the session through opencode's own API instead
// (createSession, runtime.go), with the cockpit's id in the session metadata,
// and every surface that takes an id resolves that mapping through the
// store: SessionExists and DeleteSession here, the resume in ResumeCommand,
// and the record guard in the parser.
type runner struct {
	sessions *sessionRepository
	create   createFunc
}

// AssistantRunner returns the conversation capability, or nil when the
// installed opencode cannot do what a turn needs. A pass holds for the
// process, a miss is probed again after a pause, see coder.CapabilityProbe.
func (p *Coder) AssistantRunner() assistant.Runner {
	if !p.assistantProbe.Passed() {
		return nil
	}
	return p.runner
}

func (p *Coder) probeAssistant() bool {
	if missing := missingAssistantFlags(); len(missing) > 0 {
		log.Printf("opencode conversations disabled, the installed CLI has no %s", strings.Join(missing, ", "))
		return false
	}
	p.runner = &runner{sessions: p.sessions, create: createSession}
	return true
}

func missingAssistantFlags() []string {
	help := clirun.Run("opencode", "run", "--help")
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
	return r.sessions.exists(sessionID)
}

func (r *runner) DeleteSession(sessionID string) error {
	return r.sessions.DeleteSession(sessionID)
}

// Command builds one non-interactive opencode turn. The first turn creates
// the session ahead of the run, named after the conversation and carrying the
// cockpit's id in its metadata; every turn then runs `opencode run --session`
// against opencode's own id. A conversation has the same tools as a coder
// terminal, and a non-interactive run cannot ask, so every turn carries
// --auto: without it opencode auto-rejects every permission it would have
// asked for.
//
// The prompt goes last, behind the end of options separator: run's message is
// positional, and the separator is what keeps a prompt that starts with a
// dash text (verified on 1.18.23, `opencode run -- -dxdebug.idekey=…`
// delivers the words).
func (r *runner) Command(req assistant.TurnRequest) (assistant.Command, error) {
	native := ""
	if req.Resume {
		native = r.sessions.nativeID(req.SessionID)
	} else {
		id, err := r.create(req.Workdir, assistant.SessionName(req.Title), req.SessionID)
		if err != nil {
			return assistant.Command{}, fmt.Errorf("the conversation session could not be created: %w", err)
		}
		native = id
	}
	args := []string{"run",
		"--session", native,
		"--format", "json",
		"--auto",
		"--", req.Prompt,
	}
	return assistant.Command{Name: "opencode", Args: args}, nil
}

// Parse reads opencode's JSONL run output. It is called again when a turn is
// picked up after a restart, so it carries no state from the start of the
// turn; the id mapping it needs is read back from the store.
func (r *runner) Parse(sessionID string, events chan<- assistant.Event) assistant.Parser {
	return &parser{
		nativeID: r.sessions.nativeID(sessionID),
		events:   events,
		sessions: r.sessions,
	}
}

// parser turns the run's JSONL records into conversation events. Each record
// carries one finished part: a text part is one markdown block (opencode
// streams no deltas in this format), a tool part names what ran, and the
// step-finish reason is what says the turn came to its own end, because the
// run writes no closing record at all. It reads past anything it cannot
// decode, noting it in the server log.
type parser struct {
	nativeID string
	events   chan<- assistant.Event
	sessions *sessionRepository
	// sentText is whether this turn has put any text out. Every text record
	// is a block of its own, so the separator goes in front of every one
	// after the first, never in front of the turn's first and never behind
	// its last.
	sentText bool
	// sawStop is whether a step ended with a reason that does not hand on
	// into a tool call. Without it the process ended mid-turn.
	sawStop bool
	// failed is whether an error record arrived; authFailed names the one
	// error the user can act on.
	failed     bool
	authFailed bool
}

type recordHead struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionID"`
}

type partRecord struct {
	Part struct {
		Type   string `json:"type"`
		Text   string `json:"text"`
		Tool   string `json:"tool"`
		Reason string `json:"reason"`
	} `json:"part"`
}

type errorRecord struct {
	Error struct {
		Name string `json:"name"`
		Data struct {
			Message string `json:"message"`
		} `json:"data"`
	} `json:"error"`
}

func (p *parser) Line(line []byte) error {
	var head recordHead
	if err := json.Unmarshal(line, &head); err != nil {
		p.skipUnreadable(line, err)
		return nil
	}
	if head.SessionID != "" && head.SessionID != p.nativeID {
		log.Printf("opencode assistant: expected session %s, got %s", p.nativeID, head.SessionID)
		return errors.New("The coder answered in a different conversation, so this turn was dropped.")
	}
	switch head.Type {
	case "text", "tool_use", "step_finish":
		var rec partRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			p.skipUnreadable(line, err)
			return nil
		}
		switch head.Type {
		case "text":
			if rec.Part.Text != "" {
				p.emitText(rec.Part.Text)
			}
		case "tool_use":
			p.events <- assistant.Event{Kind: assistant.EventTool, Text: rec.Part.Tool}
		case "step_finish":
			if rec.Part.Reason != "" && rec.Part.Reason != "tool-calls" {
				p.sawStop = true
			}
		}
	case "error":
		var rec errorRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			p.skipUnreadable(line, err)
		}
		p.failed = true
		if rec.Error.Name == "ProviderAuthError" {
			p.authFailed = true
		}
	}
	return nil
}

// emitText sends one text part, with the block separator in front of it when
// text went out before. A part is a complete block in this output format, so
// the seam between two of them comes out of the records themselves and never
// out of the text.
func (p *parser) emitText(text string) {
	if p.sentText {
		p.events <- assistant.Event{Kind: assistant.EventDelta, Text: assistant.BlockSeparator}
	}
	p.sentText = true
	p.events <- assistant.Event{Kind: assistant.EventDelta, Text: text}
}

// skipUnreadable notes a line this parser could not decode and lets the turn
// read on. The line travels into the log in shortened form, so the next time
// a record like it arrives the log says what it was.
func (p *parser) skipUnreadable(line []byte, err error) {
	log.Printf("opencode assistant: unreadable output record: %v: %s", err, assistant.UnreadableLine(line))
}

func (p *parser) Finish() error {
	// The context reading is read here and nowhere else: opencode writes the
	// token counts onto the assistant message in its store, and nothing of it
	// reaches the run's output records.
	p.reportUsage()
	if p.authFailed {
		return assistant.ErrNotLoggedIn
	}
	if p.failed {
		return errors.New("The coder could not finish this answer.")
	}
	if !p.sawStop {
		return errors.New("The coder stopped before it finished the answer.")
	}
	return nil
}

// Diagnose reads what opencode said when it never got going, off standard
// error; the auth error a running turn reports arrives as an error record and
// is decided in Line.
func (p *parser) Diagnose(err error, stderr string) error {
	if assistant.LooksLikeLogin(stderr) {
		return assistant.ErrNotLoggedIn
	}
	return nil
}

// usageQuery reads what the newest assistant message consumed. The context is
// everything that went in, cached or not: a cache read is context the model
// saw, it was only cheaper to send.
const usageQuery = `SELECT json_extract(data,'$.modelID') AS model,` +
	` COALESCE(json_extract(data,'$.tokens.input'),0)` +
	` + COALESCE(json_extract(data,'$.tokens.cache.read'),0)` +
	` + COALESCE(json_extract(data,'$.tokens.cache.write'),0) AS tokens` +
	` FROM message WHERE session_id = '%s' AND json_extract(data,'$.role') = 'assistant'` +
	` ORDER BY time_created DESC, id DESC LIMIT 1`

// reportUsage sends what the finished run recorded about its context. A run
// that recorded nothing readable reports nothing, so the page keeps the last
// number it had instead of showing a guess. The window stays unmeasured until
// a reading proves what an opencode model holds, an unknown window shows
// tokens without a fill.
func (p *parser) reportUsage() {
	if p.sessions == nil {
		return
	}
	native, err := validSessionID(p.nativeID)
	if err != nil {
		return
	}
	if _, ok := p.sessions.dbStamp(); !ok {
		return
	}
	out, err := p.sessions.query(fmt.Sprintf(usageQuery, native))
	if err != nil {
		return
	}
	var rows []struct {
		Model  string `json:"model"`
		Tokens int    `json:"tokens"`
	}
	if err := json.Unmarshal(out, &rows); err != nil || len(rows) == 0 || rows[0].Tokens <= 0 {
		return
	}
	p.events <- assistant.Event{
		Kind: assistant.EventUsage,
		Usage: &assistant.ContextUsage{
			Model:  rows[0].Model,
			Tokens: rows[0].Tokens,
			Window: assistant.ContextWindow("opencode", rows[0].Model, ""),
		},
	}
}
