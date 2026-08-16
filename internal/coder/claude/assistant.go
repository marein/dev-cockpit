package claude

import (
	"encoding/json"
	"errors"
	"log"
	"strings"

	"github.com/local/dev-cockpit/internal/assistant"
	"github.com/local/dev-cockpit/internal/clirun"
	"github.com/local/dev-cockpit/internal/coder"
)

// assistantFlags are the flags this runner cannot work without. Their presence is
// checked once against --help; a claude version that lost one of them loses
// conversation support only, its terminal integration is untouched.
var assistantFlags = []string{"--print", "--output-format", "--include-partial-messages", "--verbose", "--session-id", "--resume", "--permission-mode"}

type runner struct {
	sessions coder.SessionRepository
}

// AssistantRunner returns the conversation capability, or nil when the
// installed claude cannot do what a turn needs. A pass holds for the process,
// a miss is probed again after a pause, see coder.CapabilityProbe.
func (p *Coder) AssistantRunner() assistant.Runner {
	if !p.assistantProbe.Passed() {
		return nil
	}
	return p.runner
}

func (p *Coder) probeAssistant() bool {
	if missing := missingAssistantFlags(); len(missing) > 0 {
		log.Printf("claude conversations disabled, the installed CLI has no %s", strings.Join(missing, ", "))
		return false
	}
	p.runner = &runner{sessions: p.sessions}
	return true
}

func missingAssistantFlags() []string {
	help := clirun.Run("claude", "--help")
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

// Command builds one non-interactive claude turn. The first turn creates the
// session under the conversation's own id and names it, so a transferred conversation shows up
// as a coder terminal carrying the conversation title; every later turn resumes that
// exact id.
//
// A conversation has the same tools as a coder terminal, including the ones that
// change files and run commands, and the same automatic approval the cockpit
// starts its terminals with. A non-interactive turn cannot ask, so without it
// every tool that needs a decision would fail instead of doing the work.
//
// Every flag comes first and the prompt goes last, behind endOfOptions: it is
// claude's positional argument, so that separator is the one thing that keeps a
// prompt somebody typed from being read as an option.
func (r *runner) Command(req assistant.TurnRequest) (assistant.Command, error) {
	args := []string{"-p"}
	if req.Resume {
		args = append(args, "--resume", req.SessionID)
	} else {
		args = append(args, "--session-id", req.SessionID)
		if name := assistant.SessionName(req.Title); name != "" {
			args = append(args, "--name", name)
		}
	}
	args = append(args,
		"--permission-mode", "auto",
		"--output-format", "stream-json",
		"--include-partial-messages",
		"--verbose",
		endOfOptions, req.Prompt,
	)
	return assistant.Command{Name: "claude", Args: args}, nil
}

// Parse reads claude's stream-json output. It is called again when a turn is
// picked up after a restart, so it carries no state from the start of the turn.
func (r *runner) Parse(sessionID string, events chan<- assistant.Event) assistant.Parser {
	return &claudeParser{sessionID: sessionID, events: events}
}

// claudeParser turns the documented stream-json records into conversation events. It
// accepts assistant text and the final result, ignores documented progress
// records, and reads past anything it cannot decode, noting it in the server
// log. What stays strict is what the turn's outcome hangs on: the result
// record and Finish.
type claudeParser struct {
	sessionID string
	events    chan<- assistant.Event
	sawResult bool
	// sawDelta tracks whether the message being assembled streamed any text.
	// The assembled record repeats what the deltas already carried, so it only
	// speaks when they stayed silent, which is how a turn that never got to
	// stream (an API error, a refusal) still shows what happened.
	sawDelta bool
	// model and tokens are the context reading of the last assembled assistant
	// message. Later messages of the same turn overwrite it, so what is left at
	// the end is where the conversation stands.
	model  string
	tokens int
	// authFailed is set by the record claude sends when it was never logged in
	// on this machine. It is remembered instead of reported at once, because the
	// record that closes the turn is where a turn's outcome is decided.
	authFailed bool
	// sentText is whether this turn has put any text out, and blockPending
	// whether a content block boundary stands between that text and whatever
	// comes next. The stream names those boundaries itself, so the separator
	// between two markdown blocks is read out of the output and never derived
	// from the text.
	sentText     bool
	blockPending bool
}

// claudeHead is the first pass over a record: only what routes it. Everything
// else is decoded per type, because the same field name carries different
// shapes across types. A system record's message is a string where an
// assistant record's is an object, and one struct over every record let a
// permission denial claude sends in passing fail the whole line and kill a
// turn claude itself finished.
type claudeHead struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
}

// claudeStreamRecord is a stream_event record, the normal path of an answer.
type claudeStreamRecord struct {
	Event struct {
		Type  string `json:"type"`
		Delta struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"delta"`
		ContentBlock struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"content_block"`
	} `json:"event"`
}

// claudeAssistantRecord is the assembled message claude sends after its stream
// events. The content is read on its own, so a shape this version does not
// know costs nothing: the deltas are the normal path.
type claudeAssistantRecord struct {
	Message struct {
		Content json.RawMessage `json:"content"`
		Model   string          `json:"model"`
		Usage   claudeUsage     `json:"usage"`
	} `json:"message"`
	// ParentToolUseID is set on a record that belongs to a subagent claude
	// started inside this turn. Such a message has a context of its own, so its
	// usage says nothing about the conversation's.
	ParentToolUseID string `json:"parent_tool_use_id"`
	// Error names what an API error record is about, the field claude sets next
	// to is_api_error_message. It is the machine readable half of that record,
	// which is why it and not the text next to it decides anything.
	Error looseString `json:"error"`
}

// claudeResultRecord is the record that closes a turn.
type claudeResultRecord struct {
	IsError   bool   `json:"is_error"`
	SessionID string `json:"session_id"`
	// ModelUsage is what the result record says about every model the turn
	// used, keyed by the name claude called it. It carries the context window,
	// which is why the window a claude turn is measured against never has to be
	// guessed: the run reports it.
	ModelUsage map[string]struct {
		ContextWindow  int    `json:"contextWindow"`
		CanonicalModel string `json:"canonicalModel"`
	} `json:"modelUsage"`
}

// looseString is a field that is a string in the documented records but has
// arrived as an object in the wild. Any shape but a string reads as empty
// instead of failing the record, because a field this parser only compares
// must never decide whether a line is readable.
type looseString string

func (s *looseString) UnmarshalJSON(data []byte) error {
	var text string
	if json.Unmarshal(data, &text) == nil {
		*s = looseString(text)
	}
	return nil
}

// claudeUsage is the token part of an assistant message. What the context holds
// is everything that went in, cached or not: a cache read is context the model
// saw, it was only cheaper to send.
type claudeUsage struct {
	InputTokens              int `json:"input_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (u claudeUsage) contextTokens() int {
	return u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
}

func (p *claudeParser) Line(line []byte) error {
	var head claudeHead
	if err := json.Unmarshal(line, &head); err != nil {
		p.skipUnreadable(line, err)
		return nil
	}
	switch head.Type {
	case "stream_event":
		var rec claudeStreamRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			p.skipUnreadable(line, err)
			return nil
		}
		switch rec.Event.Type {
		case "message_start":
			p.sawDelta = false
		case "content_block_delta":
			if rec.Event.Delta.Type == "text_delta" && rec.Event.Delta.Text != "" {
				p.sawDelta = true
				p.emitText(rec.Event.Delta.Text)
			}
		case "content_block_start":
			// A block began, so whatever text comes next belongs to another one
			// than what went out before it. Which block it is does not matter,
			// a thinking or tool block between two text blocks is a boundary
			// all the same.
			p.blockPending = true
			if rec.Event.ContentBlock.Type == "tool_use" {
				p.events <- assistant.Event{Kind: assistant.EventTool, Text: rec.Event.ContentBlock.Name}
			}
		}
		return nil
	case "assistant":
		var rec claudeAssistantRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			p.skipUnreadable(line, err)
			return nil
		}
		// The failure the user can act on arrives here, on standard output: a
		// claude nobody logged in on this machine sends a record marked as an API
		// error carrying its own wording. That is not an answer, so it is noted
		// and nothing of it is streamed, and the turn is failed on the record
		// that closes it.
		if rec.Error == "authentication_failed" {
			p.authFailed = true
			return nil
		}
		// A subagent's message is not this conversation's context: it runs on a
		// window of its own, and its reading would understate the turn's.
		if tokens := rec.Message.Usage.contextTokens(); tokens > 0 && rec.ParentToolUseID == "" {
			p.model, p.tokens = rec.Message.Model, tokens
		}
		if p.sawDelta || len(rec.Message.Content) == 0 {
			return nil
		}
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(rec.Message.Content, &blocks); err != nil {
			return nil
		}
		for _, part := range blocks {
			// Every entry of the assembled content is a block of its own, the
			// same seam content_block_start marks on the streaming path.
			p.blockPending = true
			if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
				p.emitText(part.Text)
			}
		}
		return nil
	case "result":
		var rec claudeResultRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			// The one record the outcome hangs on. Skipping it leaves sawResult
			// unset, so Finish names the broken turn; inventing an outcome out
			// of a line nobody could read would be worse.
			p.skipUnreadable(line, err)
			return nil
		}
		p.sawResult = true
		if rec.SessionID != "" && rec.SessionID != p.sessionID {
			log.Printf("claude assistant: expected session %s, got %s", p.sessionID, rec.SessionID)
			return errors.New("The coder answered in a different conversation, so this turn was dropped.")
		}
		// The reading goes out before the outcome is judged: a turn that failed
		// after it answered still consumed what it consumed, and the window it
		// was measured against is on this record.
		p.reportUsage(rec)
		if p.authFailed {
			return assistant.ErrNotLoggedIn
		}
		if rec.IsError || (head.Subtype != "" && head.Subtype != "success") {
			return errors.New("The coder could not finish this answer.")
		}
		return nil
	}
	return nil
}

// emitText sends one piece of assistant text, with the block separator in front
// of it when a block boundary stands between it and the text that went out
// before. The boundary is the stream's own, so the first block of a turn gets
// nothing in front of it, the last gets nothing behind it, and a block that
// carried no text at all (a tool call, a thinking block) leaves one seam and
// not two.
func (p *claudeParser) emitText(text string) {
	if p.sentText && p.blockPending {
		p.events <- assistant.Event{Kind: assistant.EventDelta, Text: assistant.BlockSeparator}
	}
	p.blockPending = false
	p.sentText = true
	p.events <- assistant.Event{Kind: assistant.EventDelta, Text: text}
}

// skipUnreadable notes a line this parser could not decode and lets the turn
// read on. The line travels into the log in shortened form, so the next time a
// record like it arrives the log says what it was. Ending the turn here is
// what this parser used to do, and that killed conversations over records
// claude sends in passing while claude itself carried on to a successful
// result.
func (p *claudeParser) skipUnreadable(line []byte, err error) {
	log.Printf("claude assistant: unreadable output record: %v: %s", err, assistant.UnreadableLine(line))
}

// reportUsage sends the context reading of this turn, once, on the record that
// closes it. The window comes from the run itself where the run says it, which
// is what keeps a model with an unusual window (a long context variant of a
// model whose plain form is far smaller) from being measured against the wrong
// number.
func (p *claudeParser) reportUsage(rec claudeResultRecord) {
	if p.tokens <= 0 {
		return
	}
	window := 0
	for name, model := range rec.ModelUsage {
		if model.ContextWindow <= 0 {
			continue
		}
		if name == p.model || model.CanonicalModel == p.model {
			window = model.ContextWindow
			break
		}
	}
	if window == 0 {
		// The table is keyed by coder, because the same model does not have the
		// same window under every CLI. claude has no context tiers.
		window = assistant.ContextWindow("claude", p.model, "")
	}
	p.events <- assistant.Event{
		Kind:  assistant.EventUsage,
		Usage: &assistant.ContextUsage{Model: p.model, Tokens: p.tokens, Window: window},
	}
}

func (p *claudeParser) Finish() error {
	if !p.sawResult {
		return errors.New("The coder stopped before it finished the answer.")
	}
	return nil
}

// Diagnose has nothing left to name for a missing login: claude writes that into
// a record on standard output and leaves standard error empty, measured on a CLI
// that was never logged in, so Line already decided it. The pattern over
// standard error stays as a fallback for a version that starts writing it there
// after all. Nothing here looks at the raw output or at the result text: both
// carry the answer on a turn that worked, and a turn whose answer talks about
// logins is not a login failure.
func (p *claudeParser) Diagnose(err error, stderr string) error {
	if assistant.LooksLikeLogin(stderr) {
		return assistant.ErrNotLoggedIn
	}
	return nil
}
