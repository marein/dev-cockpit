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
// records, and refuses anything that does not match.
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
}

type claudeRecord struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	// stream_event
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
	// assistant, the assembled message claude sends after its stream events.
	// The content is read on its own, so a shape this version does not know
	// costs nothing: the deltas are the normal path.
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
	Error string `json:"error"`
	// result
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
	var rec claudeRecord
	if err := json.Unmarshal(line, &rec); err != nil {
		log.Printf("claude assistant: unreadable output record: %v", err)
		return errors.New("The coder sent an answer this version cannot read.")
	}
	switch rec.Type {
	case "stream_event":
		switch rec.Event.Type {
		case "message_start":
			p.sawDelta = false
		case "content_block_delta":
			if rec.Event.Delta.Type == "text_delta" && rec.Event.Delta.Text != "" {
				p.sawDelta = true
				p.events <- assistant.Event{Kind: assistant.EventDelta, Text: rec.Event.Delta.Text}
			}
		case "content_block_start":
			if rec.Event.ContentBlock.Type == "tool_use" {
				p.events <- assistant.Event{Kind: assistant.EventTool, Text: rec.Event.ContentBlock.Name}
			}
		}
		return nil
	case "assistant":
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
			if part.Type == "text" && strings.TrimSpace(part.Text) != "" {
				p.events <- assistant.Event{Kind: assistant.EventDelta, Text: part.Text}
			}
		}
		return nil
	case "result":
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
		if rec.IsError || (rec.Subtype != "" && rec.Subtype != "success") {
			return errors.New("The coder could not finish this answer.")
		}
		return nil
	}
	return nil
}

// reportUsage sends the context reading of this turn, once, on the record that
// closes it. The window comes from the run itself where the run says it, which
// is what keeps a model with an unusual window (a long context variant of a
// model whose plain form is far smaller) from being measured against the wrong
// number.
func (p *claudeParser) reportUsage(rec claudeRecord) {
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
