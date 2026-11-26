package codex

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
)

// assistantFlags are what a turn cannot work without, checked once against
// `codex exec resume --help`. A version that lost one of them loses the
// conversations only, its terminal integration is untouched. The hook trust
// flag is among them on purpose: without it the hook that asks the user about
// a tool call would not run, and a turn would call every tool unasked.
var assistantFlags = []string{"--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust"}

// A conversation lives in a session the service addresses by an id it chose,
// and `codex exec` can neither create a session under a caller's id nor name
// one. So the first turn makes the session through codex's app server
// (startThread), named after the conversation and carrying the cockpit's id
// in its thread source, and every turn, the first included, resumes codex's
// own id. Every surface that takes an id resolves that mapping through the
// store: SessionExists and DeleteSession here, the resume in ResumeCommand,
// and the thread guard and the context reading in the parser.
type runner struct {
	sessions *sessionRepository
	create   func(workdir, name, cockpitID string) (string, error)
}

// AssistantRunner returns the conversation capability, or nil when the
// installed codex cannot do what a turn needs. A pass holds for the process,
// a miss is probed again after a pause, see coder.CapabilityProbe.
func (p *Coder) AssistantRunner() assistant.Runner {
	if !p.assistantProbe.Passed() {
		return nil
	}
	return p.runner
}

func (p *Coder) probeAssistant() bool {
	if missing := missingAssistantFlags(); len(missing) > 0 {
		log.Printf("codex conversations disabled, the installed CLI has no %s", strings.Join(missing, ", "))
		return false
	}
	p.runner = &runner{sessions: p.sessions, create: startThread}
	return true
}

func missingAssistantFlags() []string {
	help := clirun.Run("codex", "exec", "resume", "--help")
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

// Command builds one non-interactive codex turn, `codex exec resume` against
// codex's own id of the session.
//
// A conversation has the same tools as a coder terminal started with
// automatic approval, and for the same reason: codex's sandbox refuses every
// socket, the cockpit's local API among them, so a turn inside it could not
// run one cockpit command. `codex exec` never asks its person anything
// either way (verified on 0.172.0, its approval policy is never whatever the
// config says), so with the sandbox off a tool codex would want approved, a
// tool of an MCP server or a connector, would simply run. That is what the
// hook is for, see toolApprovalHook: it stands in front of those calls and
// asks the user through the cockpit.
//
// The hook rides the command line and no config file knows it, so codex
// would hold it back for a review nobody can give in a turn; the trust flag
// lets it run. It lets every other hook of the user's own config run as
// well, reviewed or not, and codex says so in two warning records at the
// start of every turn, which the parser reads past.
//
// The workspace is no repository, which codex refuses without its flag. The
// session id and the prompt go last, behind the end of options, so a prompt
// that starts with a dash stays text; the model rides in the equals form for
// the same reason.
func (r *runner) Command(req assistant.TurnRequest) (assistant.Command, error) {
	native := ""
	if req.Resume {
		native = r.sessions.nativeID(req.SessionID)
	} else {
		id, err := r.create(req.Workdir, assistant.SessionName(req.Title), req.SessionID)
		if id == "" {
			return assistant.Command{}, fmt.Errorf("the conversation session could not be created: %w", err)
		}
		if err != nil {
			log.Printf("codex assistant: session %s keeps the name codex gives it: %v", id, err)
		}
		native = id
	}
	if _, err := validSessionID(native); err != nil {
		return assistant.Command{}, errors.New("The conversation session has no valid id.")
	}
	args := []string{"exec", "resume",
		"--json",
		"--skip-git-repo-check",
		"--dangerously-bypass-approvals-and-sandbox",
		"--dangerously-bypass-hook-trust",
		"-c", toolApprovalHook(req.Workdir),
	}
	if req.Model != "" {
		args = append(args, "--model="+req.Model)
	}
	args = append(args, "--", native, req.Prompt)
	return assistant.Command{Name: "codex", Args: args}, nil
}

// toolApprovalTools is what a turn asks the user about: the tools of MCP
// servers and connectors, which codex names mcp__<server>__<tool> for a
// hook. They are the calls codex itself wants approved (verified on 0.172.0:
// in its sandbox such a call fails with "MCP tool call requires approval,
// but approval policy is never"). The shell and the patch tool stay free,
// like in a coder terminal with automatic approval.
const toolApprovalTools = "^mcp__"

// toolApprovalHook is the config override that puts the cockpit's tool
// approval in front of those calls: a PreToolUse hook, the one place codex
// lets a command refuse a call whatever its approval policy says (verified on
// 0.172.0, the hook runs with approvals and sandbox bypassed and a deny
// keeps the call from reaching its server). The hook is the cockpit's own
// command behind the turn's wrapper, which asks as the assistant whose turn
// it is. Its timeout lies beyond the wait of the question, so the command
// always answers before codex gives up on it.
func toolApprovalHook(workdir string) string {
	argv := assistant.ToolApprovalHook(workdir)
	quoted := make([]string, len(argv))
	for i, arg := range argv {
		quoted[i] = clirun.ShellQuote(arg)
	}
	return fmt.Sprintf(`hooks.PreToolUse=[{matcher=%s,hooks=[{type="command",command=%s,timeout=%d}]}]`,
		tomlString(toolApprovalTools), tomlString(strings.Join(quoted, " ")), int(assistant.ToolApprovalHookTimeout.Seconds()))
}

// Parse reads the JSONL events of `codex exec --json`. It is called again
// when a turn is picked up after a restart, so it carries no state from the
// start of the turn; the id mapping it needs is read back from the store.
func (r *runner) Parse(sessionID string, events chan<- assistant.Event) assistant.Parser {
	return &parser{nativeID: r.sessions.nativeID(sessionID), events: events, sessions: r.sessions, announced: map[string]bool{}}
}

// parser turns the run's events into conversation events (verified on
// 0.172.0). An agent message arrives whole, one markdown block per item,
// codex streams no deltas in this format. A tool call is an item that starts
// and completes, some only complete. turn.completed is the record that says
// the turn came to its own end, turn.failed carries why it did not. An item
// of type error is a warning codex passes on, and an error record in between
// a retry it announces: neither ends a turn. It reads past anything it
// cannot decode or does not know, noting the first in the server log.
type parser struct {
	nativeID string
	events   chan<- assistant.Event
	sessions *sessionRepository
	// announced holds the tool items already reported, so an item that
	// started is not reported again when it completes.
	announced map[string]bool
	sentText  bool
	completed bool
	failed    error
	// compacted is whether the record of a compaction arrived, see
	// compact.go. It carries the outcome, and the rollout holds no reading
	// of the context it left until the next request.
	compacted bool
}

type eventHead struct {
	Type     string `json:"type"`
	ThreadID string `json:"thread_id"`
}

type itemRecord struct {
	Item struct {
		ID     string `json:"id"`
		Type   string `json:"type"`
		Text   string `json:"text"`
		Server string `json:"server"`
		Tool   string `json:"tool"`
	} `json:"item"`
}

type failureRecord struct {
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

// itemTools names the tool an item stands for, the way the session's own
// record names it (recordItem.tool).
var itemTools = map[string]string{
	"command_execution": "shell",
	"file_change":       "apply_patch",
	"web_search":        "web_search",
}

func (p *parser) Line(line []byte) error {
	var head eventHead
	if err := json.Unmarshal(line, &head); err != nil {
		p.skipUnreadable(line, err)
		return nil
	}
	switch head.Type {
	case "thread.started":
		if head.ThreadID != "" && p.nativeID != "" && head.ThreadID != p.nativeID {
			log.Printf("codex assistant: expected session %s, got %s", p.nativeID, head.ThreadID)
			return errors.New("The coder answered in a different conversation, so this turn was dropped.")
		}
	case "item.started", "item.completed":
		var rec itemRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			p.skipUnreadable(line, err)
			return nil
		}
		p.readItem(head.Type == "item.completed", rec)
	case "turn.completed":
		p.completed = true
	case "turn.failed":
		var rec failureRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			p.skipUnreadable(line, err)
		}
		p.failed = failure(rec.Error.Message)
	case compactRecordType:
		return p.readCompaction(line)
	}
	return nil
}

func (p *parser) readItem(completed bool, rec itemRecord) {
	item := rec.Item
	if item.Type == "agent_message" {
		if completed && item.Text != "" {
			p.emitText(item.Text)
		}
		return
	}
	name := itemTools[item.Type]
	if item.Type == "mcp_tool_call" {
		name = strings.TrimSpace(item.Tool)
	}
	if name == "" || (item.ID != "" && p.announced[item.ID]) {
		return
	}
	p.announced[item.ID] = true
	p.events <- assistant.Event{Kind: assistant.EventTool, Text: name}
}

// emitText sends one agent message, with the block separator in front of it
// when text went out before. A message is a complete block in this output
// format, so the seam between two of them comes out of the records
// themselves and never out of the text.
func (p *parser) emitText(text string) {
	if p.sentText {
		p.events <- assistant.Event{Kind: assistant.EventDelta, Text: assistant.BlockSeparator}
	}
	p.sentText = true
	p.events <- assistant.Event{Kind: assistant.EventDelta, Text: text}
}

// unsupportedModel is the sentence codex fails a turn with for a model it
// does not have, "The 'no-such-model' model is not supported when using
// Codex with a ChatGPT account." (0.172.0).
var unsupportedModel = regexp.MustCompile(`^The '([^']*)' model is not supported`)

// failure names what a failed turn's own record says. A codex nobody logged
// in fails with the API's 401 in that record (verified on 0.172.0 with an
// empty home), a model it does not have with the sentence above; everything
// else, a usage limit among it, is quoted behind the cockpit's sentence.
func failure(message string) error {
	if match := unsupportedModel.FindStringSubmatch(strings.TrimSpace(message)); match != nil {
		return assistant.UnknownModel(match[1])
	}
	if assistant.LooksLikeLogin(message) {
		return assistant.ErrNotLoggedIn
	}
	return assistant.Quote(errors.New("The coder could not finish this answer."), message)
}

func (p *parser) skipUnreadable(line []byte, err error) {
	log.Printf("codex assistant: unreadable output record: %v: %s", err, assistant.UnreadableLine(line))
}

func (p *parser) Finish() error {
	if p.compacted {
		return nil
	}
	// The context reading is read here and nowhere else: the run's own usage
	// record sums every request of the turn, which says nothing about how
	// full the context stands, and the session's record holds the last one.
	p.reportUsage()
	if p.failed != nil {
		return p.failed
	}
	if !p.completed {
		return errors.New("The coder stopped before it finished the answer.")
	}
	return nil
}

// Diagnose reads what codex said when it never got going, off standard
// error; a failure of a running turn arrives as its own record and is
// decided in Line.
func (p *parser) Diagnose(err error, stderr string) error {
	if assistant.LooksLikeLogin(stderr) {
		return assistant.ErrNotLoggedIn
	}
	return nil
}

// reportUsage sends what the session's record says about its context. A
// record that says nothing readable reports nothing, so the page keeps the
// last number it had instead of showing a guess.
func (p *parser) reportUsage() {
	if p.sessions == nil {
		return
	}
	usage, ok := p.sessions.contextUsage(p.nativeID)
	if !ok {
		return
	}
	p.events <- assistant.Event{Kind: assistant.EventUsage, Usage: &usage}
}
