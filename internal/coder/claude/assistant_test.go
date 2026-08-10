package claude

import (
	"errors"
	"strings"
	"testing"

	"github.com/local/dev-cockpit/internal/assistant"
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/filesystem"
	"io"
)

type stubSessions struct{ ids []string }

func (s stubSessions) List() []coder.Session {
	out := make([]coder.Session, 0, len(s.ids))
	for _, id := range s.ids {
		out = append(out, coder.Session{SessionID: id})
	}
	return out
}
func (s stubSessions) DeleteSession(string) error { return nil }
func (s stubSessions) ListFiles(string) ([]filesystem.File, error) {
	return nil, nil
}
func (s stubSessions) SaveFile(string, string, io.Reader) (filesystem.File, error) {
	return filesystem.File{}, nil
}
func (s stubSessions) OpenFile(string, string) (filesystem.OpenedFile, error) {
	return filesystem.OpenedFile{}, nil
}
func (s stubSessions) DeleteFile(string, string) (filesystem.File, error) {
	return filesystem.File{}, nil
}

const sessionID = "11111111-2222-4333-8444-555555555555"

func initLine(tools string) string {
	return `{"type":"system","subtype":"init","tools":[` + tools + `],"session_id":"` + sessionID + `"}`
}

func deltaLine(text string) string {
	return `{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"` + text + `"}}}`
}

func resultLine(id string) string {
	return `{"type":"result","subtype":"success","is_error":false,"session_id":"` + id + `"}`
}

// commandOf builds the process one turn runs, which is what the runner decides
// now: the assistant package starts it and reads its file.
func commandOf(t *testing.T, resume bool, prompt string) assistant.Command {
	t.Helper()
	r := &runner{sessions: stubSessions{}}
	cmd, err := r.Command(assistant.TurnRequest{
		SessionID: sessionID,
		Resume:    resume,
		Title:     "A conversation title",
		Workdir:   t.TempDir(),
		Prompt:    prompt,
	})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	return cmd
}

// argvOf is that command as one line, which is what a test reads when it is
// about which flags a turn carries and not about where a word sits.
func argvOf(t *testing.T, resume bool) string {
	t.Helper()
	cmd := commandOf(t, resume, "hello")
	return strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
}

// runTurn feeds one output fixture through the runner's parser, line by line,
// the way the assistant reads the file a turn writes. A parser error and a
// missing closing record both end the turn, so both come back as the error
// event the service would record.
func runTurn(t *testing.T, fixture string) []assistant.Event {
	t.Helper()
	return runTurnStderr(t, fixture, "")
}

// runTurnStderr is the same with a standard error tail, the way the assistant
// hands one to the parser when a turn failed.
func runTurnStderr(t *testing.T, fixture, stderr string) []assistant.Event {
	t.Helper()
	r := &runner{sessions: stubSessions{}}
	out := make(chan assistant.Event, 256)
	parser := r.Parse(sessionID, out)

	var collected []assistant.Event
	drain := func() {
		for {
			select {
			case ev := <-out:
				collected = append(collected, ev)
			default:
				return
			}
		}
	}
	var err error
	for _, line := range strings.Split(fixture, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if err = parser.Line([]byte(line)); err != nil {
			break
		}
		drain()
	}
	drain()
	if err == nil {
		err = parser.Finish()
	}
	if err != nil {
		// A failed turn goes through the parser once more before the user reads
		// anything, which is where the cause of a turn that never got going is
		// named.
		if named := parser.Diagnose(err, stderr); named != nil {
			err = named
		}
		collected = append(collected, assistant.Event{Kind: assistant.EventError, Err: err})
	}
	return collected
}

func textOf(events []assistant.Event) string {
	var b strings.Builder
	for _, ev := range events {
		if ev.Kind == assistant.EventDelta {
			b.WriteString(ev.Text)
		}
	}
	return b.String()
}

func errorOf(events []assistant.Event) error {
	for _, ev := range events {
		if ev.Kind == assistant.EventError {
			return ev.Err
		}
	}
	return nil
}

func TestTurnAssemblesDeltas(t *testing.T) {
	fixture := strings.Join([]string{
		initLine(`"Read","Glob","Grep","WebSearch","WebFetch"`),
		`{"type":"system","subtype":"status","status":"working"}`,
		`{"type":"rate_limit_event","rate_limit_info":{}}`,
		deltaLine("Hel"),
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"hmm"}}}`,
		deltaLine("lo"),
		`{"type":"assistant","message":{"content":"Hello"}}`,
		resultLine(sessionID),
	}, "\n")

	events := runTurn(t, fixture)
	if err := errorOf(events); err != nil {
		t.Fatalf("want a clean turn, got %v", err)
	}
	if got := textOf(events); got != "Hello" {
		t.Fatalf("want the deltas assembled without the thinking block, got %q", got)
	}
}

func TestTurnArgvFirstAndResume(t *testing.T) {
	argv := argvOf(t, false)
	for _, want := range []string{
		"claude -p ",
		"-- hello",
		"--session-id " + sessionID,
		"--name A conversation title",
		"--permission-mode auto",
		"--output-format stream-json",
		"--include-partial-messages",
		"--verbose",
	} {
		if !strings.Contains(argv, want) {
			t.Fatalf("want %q in the first turn argv, got %q", want, argv)
		}
	}
	if strings.Contains(argv, "--resume") {
		t.Fatalf("want no resume on the first turn, got %q", argv)
	}
	// A conversation runs with the same approval the cockpit gives its terminals, and
	// with nothing beyond it.
	for _, forbidden := range []string{"--dangerously-skip-permissions", "bypassPermissions"} {
		if strings.Contains(argv, forbidden) {
			t.Fatalf("want no blanket permission skip in the argv, got %q", argv)
		}
	}

	argv = argvOf(t, true)
	if !strings.Contains(argv, "--resume "+sessionID) {
		t.Fatalf("want a resumed turn, got %q", argv)
	}
	if strings.Contains(argv, "--session-id") || strings.Contains(argv, "--name") {
		t.Fatalf("want no session creation flags on a resumed turn, got %q", argv)
	}
}

// A prompt is text, whatever it starts with. It is claude's positional argument,
// so it stands last behind the end of options separator: measured on claude
// 2.1.226, `claude -p -dxdebug.idekey=PHPSTORM` reads the prompt as the short
// flag -d with a filter and exits with "Input must be provided", which the turn
// reports as a coder that stopped before it finished.
func TestATurnCarriesADashLeadingPromptAsText(t *testing.T) {
	prompts := map[string]string{
		"a php option somebody pasted": "-dxdebug.idekey=PHPSTORM",
		"a long flag":                  "--help",
		"a bare dash":                  "-",
		"the separator itself":         "--",
		"an ordinary prompt":           "Fix the login redirect",
	}
	for name, prompt := range prompts {
		for _, resume := range []bool{false, true} {
			args := commandOf(t, resume, prompt).Args
			if len(args) < 2 {
				t.Fatalf("%s (resume %v): argv too short: %v", name, resume, args)
			}
			if got := args[len(args)-1]; got != prompt {
				t.Fatalf("%s (resume %v): want the prompt last, got %q in %v", name, resume, got, args)
			}
			if got := args[len(args)-2]; got != "--" {
				t.Fatalf("%s (resume %v): want the separator before the prompt, got %q in %v", name, resume, got, args)
			}
			// Every flag stands before the separator, or the one behind it would
			// be an operand instead of the prompt.
			for _, arg := range args[:len(args)-2] {
				if arg == "--" {
					t.Fatalf("%s (resume %v): want one separator and the prompt behind it, got %v", name, resume, args)
				}
			}
		}
	}
}

func TestToolUseSurfacesAsAnEventWithItsName(t *testing.T) {
	fixture := strings.Join([]string{
		initLine(`"Read","Bash","Write","Edit"`),
		`{"type":"stream_event","event":{"type":"content_block_start","content_block":{"type":"tool_use","name":"Edit"}}}`,
		deltaLine("done"),
		resultLine(sessionID),
	}, "\n")

	events := runTurn(t, fixture)
	if err := errorOf(events); err != nil {
		t.Fatalf("want a turn that uses tools to pass, got %v", err)
	}
	var tools []string
	for _, ev := range events {
		if ev.Kind == assistant.EventTool {
			tools = append(tools, ev.Text)
		}
	}
	if len(tools) != 1 || tools[0] != "Edit" {
		t.Fatalf("want one tool event naming the tool, got %v", tools)
	}
	if textOf(events) != "done" {
		t.Fatalf("want the answer text delivered, got %q", textOf(events))
	}
}

func TestTurnRefusesAForeignSession(t *testing.T) {
	fixture := initLine(`"Read","Glob","Grep","WebSearch","WebFetch"`) + "\n" +
		deltaLine("hi") + "\n" + resultLine("99999999-2222-4333-8444-555555555555")

	events := runTurn(t, fixture)
	if errorOf(events) == nil {
		t.Fatal("want a mismatched session id to fail the turn")
	}
}

func TestTurnFailsWithoutAResult(t *testing.T) {
	fixture := initLine(`"Read","Glob","Grep","WebSearch","WebFetch"`) + "\n" + deltaLine("half an answer")

	events := runTurn(t, fixture)
	if errorOf(events) == nil {
		t.Fatal("want a turn that ends without a result to fail")
	}
}

func TestTurnFailsOnMalformedOutput(t *testing.T) {
	events := runTurn(t, "{not json at all}")
	err := errorOf(events)
	if err == nil {
		t.Fatal("want malformed output to fail the turn")
	}
	if strings.Contains(err.Error(), "{") {
		t.Fatalf("want a curated message without the raw record, got %q", err)
	}
}

// A turn whose output stops before the record that closes it is a turn that
// broke off, and what the user reads about it is one curated sentence.
func TestATurnWithoutItsClosingRecordFails(t *testing.T) {
	events := runTurn(t, initLine(`"Read"`)+"\n"+deltaLine("half an answer"))
	err := errorOf(events)
	if err == nil {
		t.Fatal("want a turn that stopped early to fail")
	}
	if !strings.Contains(err.Error(), "stopped before it finished") {
		t.Fatalf("want the curated sentence, got %q", err)
	}
	if got := textOf(events); got != "half an answer" {
		t.Fatalf("want the part that arrived kept, got %q", got)
	}
}

// Measured on claude 2.1.220 in a container nobody logged in: exit code 1,
// standard error empty to the byte, and the whole story on standard output. The
// record marked as an API error carries the CLI's own wording, the result record
// repeats it, and neither may reach the transcript.
func TestATurnWithoutALoginEndsWithTheLoginSentence(t *testing.T) {
	fixture := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"` + sessionID + `","tools":["Read"]}`,
		`{"type":"assistant","message":{"model":"<synthetic>","content":[{"type":"text","text":"Not logged in · Please run /login"}],"usage":{"input_tokens":0}},"parent_tool_use_id":null,"error":"authentication_failed","is_api_error_message":true,"session_id":"` + sessionID + `"}`,
		`{"type":"result","subtype":"success","is_error":true,"terminal_reason":"api_error","result":"Not logged in · Please run /login","session_id":"` + sessionID + `"}`,
	}, "\n")

	events := runTurn(t, fixture)
	err := errorOf(events)
	if !errors.Is(err, assistant.ErrNotLoggedIn) {
		t.Fatalf("want the login sentinel for a claude that was never logged in, got %v", err)
	}
	if got := textOf(events); strings.Contains(got, "Not logged in") {
		t.Fatalf("want the CLI's own wording kept out of the answer, got %q", got)
	}
}

// The login case is decided by the marker on the record, never by reading the
// text of a failed turn: the result carries the answer on a turn that worked, so
// a turn whose answer is about logging in somewhere would otherwise be reported
// as a coder that is not logged in.
func TestAFailedTurnTalkingAboutLoginsStaysGeneric(t *testing.T) {
	fixture := strings.Join([]string{
		deltaLine("Please log in to the box first"),
		`{"type":"result","subtype":"success","is_error":true,"result":"Please log in to the box first, then run /login there","session_id":"` + sessionID + `"}`,
	}, "\n")

	events := runTurn(t, fixture)
	err := errorOf(events)
	if err == nil {
		t.Fatal("want the failed result to fail the turn")
	}
	if errors.Is(err, assistant.ErrNotLoggedIn) {
		t.Fatalf("want no login guess from the answer text, got %v", err)
	}
	if !strings.Contains(err.Error(), "could not finish this answer") {
		t.Fatalf("want the generic sentence, got %q", err)
	}
}

// The fallback for a future claude that does write it to standard error after
// all.
func TestDiagnoseFallsBackToStandardError(t *testing.T) {
	events := runTurnStderr(t, deltaLine("half"), "Error: Not logged in. Please run /login.")
	if err := errorOf(events); !errors.Is(err, assistant.ErrNotLoggedIn) {
		t.Fatalf("want the login sentinel from the standard error tail, got %v", err)
	}
	events = runTurnStderr(t, deltaLine("half"), "panic: runtime error: index out of range")
	if err := errorOf(events); err == nil || errors.Is(err, assistant.ErrNotLoggedIn) {
		t.Fatalf("want the generic failure for a crash, got %v", err)
	}
}

func TestSessionExistsUsesTheProviderRepository(t *testing.T) {
	runner := &runner{sessions: stubSessions{ids: []string{sessionID}}}
	if !runner.SessionExists(sessionID) {
		t.Fatal("want a stored session to be found")
	}
	if runner.SessionExists("22222222-2222-4333-8444-555555555555") {
		t.Fatal("want an unknown session to be missing")
	}
}

func TestCapabilityCheckNamesTheFlagsItNeeds(t *testing.T) {
	for _, flag := range []string{"--print", "--output-format", "--include-partial-messages", "--verbose", "--session-id", "--resume", "--permission-mode"} {
		found := false
		for _, have := range assistantFlags {
			if have == flag {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("%s is part of every turn, so its absence must disable conversation", flag)
		}
	}
}

// A turn that never streamed still has to show what happened: an API error
// arrives as the assembled message, without a single delta before it.
func TestParserShowsAnAnswerThatNeverStreamed(t *testing.T) {
	events := make(chan assistant.Event, 8)
	p := &claudeParser{sessionID: "s1", events: events}
	lines := []string{
		`{"type":"system","subtype":"init","session_id":"s1"}`,
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"API Error: 529 Overloaded."}]}}`,
	}
	for _, line := range lines {
		if err := p.Line([]byte(line)); err != nil {
			t.Fatalf("line %s: %v", line, err)
		}
	}
	close(events)
	var text string
	for ev := range events {
		if ev.Kind == assistant.EventDelta {
			text += ev.Text
		}
	}
	if text != "API Error: 529 Overloaded." {
		t.Fatalf("want the message text surfaced, got %q", text)
	}
}

// The normal path streams and then repeats itself in the assembled message.
// That repetition must not reach the transcript twice.
func TestParserDoesNotRepeatStreamedText(t *testing.T) {
	events := make(chan assistant.Event, 8)
	p := &claudeParser{sessionID: "s1", events: events}
	lines := []string{
		`{"type":"stream_event","event":{"type":"message_start"}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"Hel"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","delta":{"type":"text_delta","text":"lo"}}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Hello"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"session_id":"s1"}`,
	}
	for _, line := range lines {
		if err := p.Line([]byte(line)); err != nil {
			t.Fatalf("line %s: %v", line, err)
		}
	}
	close(events)
	var text string
	for ev := range events {
		if ev.Kind == assistant.EventDelta {
			text += ev.Text
		}
	}
	if text != "Hello" {
		t.Fatalf("want the streamed text once, got %q", text)
	}
}

// usageOf is the context reading a turn reported, or the zero value when it
// reported none.
func usageOf(events []assistant.Event) assistant.ContextUsage {
	for _, ev := range events {
		if ev.Kind == assistant.EventUsage && ev.Usage != nil {
			return *ev.Usage
		}
	}
	return assistant.ContextUsage{}
}

// What the context holds is everything that went in, cached or not: a cache read
// is context the model saw, it was only cheaper to send. The window comes off the
// result record, so the long context variant of a model is measured against its
// own size and not against the plain model's.
func TestTurnReportsTheContextOfTheAnsweringModel(t *testing.T) {
	fixture := strings.Join([]string{
		initLine(`"Read"`),
		deltaLine("hi"),
		`{"type":"assistant","message":{"model":"claude-opus-5","usage":{"input_tokens":2,"cache_creation_input_tokens":6228,"cache_read_input_tokens":15273,"output_tokens":4},"content":[{"type":"text","text":"hi"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"session_id":"` + sessionID + `","modelUsage":{"claude-haiku-4-5-20251001":{"contextWindow":200000,"canonicalModel":"claude-haiku-4-5"},"claude-opus-5[1m]":{"contextWindow":1000000,"canonicalModel":"claude-opus-5"}}}`,
	}, "\n")

	usage := usageOf(runTurn(t, fixture))
	if usage.Tokens != 21503 {
		t.Fatalf("want input plus both cache counts, got %d", usage.Tokens)
	}
	if usage.Window != 1000000 {
		t.Fatalf("want the window of the model that answered, not the side model's, got %d", usage.Window)
	}
	if usage.Percent() != 2 {
		t.Fatalf("want 2 percent of a million, got %d", usage.Percent())
	}
}

// The last assembled message of a turn is where the conversation stands: the
// earlier ones were smaller, and reporting one of those would show a context that
// has already grown past it.
func TestTurnReportsTheLastMessageOfTheTurn(t *testing.T) {
	fixture := strings.Join([]string{
		`{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":10000},"content":[]}}`,
		`{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":60000},"content":[]}}`,
		resultLine(sessionID),
	}, "\n")

	usage := usageOf(runTurn(t, fixture))
	if usage.Tokens != 60000 {
		t.Fatalf("want the last message's reading, got %d", usage.Tokens)
	}
	// No modelUsage on this result, so the shared table answers.
	if usage.Window != 200000 || usage.Percent() != 30 {
		t.Fatalf("want the table's window for a run that reported none, got %d of %d", usage.Tokens, usage.Window)
	}
}

// A turn that never reported usage reports nothing, and a turn answering in
// another conversation reports nothing either: its reading is not this
// conversation's.
func TestTurnWithoutUsageOrWithAForeignSessionReportsNothing(t *testing.T) {
	for name, fixture := range map[string]string{
		"no usage": deltaLine("hi") + "\n" + resultLine(sessionID),
		"foreign session": `{"type":"assistant","message":{"model":"claude-sonnet-5","usage":{"input_tokens":9000},"content":[]}}` + "\n" +
			resultLine("99999999-2222-4333-8444-555555555555"),
	} {
		if usage := usageOf(runTurn(t, fixture)); usage.Tokens != 0 {
			t.Fatalf("%s: want no reading, got %+v", name, usage)
		}
	}
}

// A subagent claude starts inside a turn answers on a window of its own, so its
// message must not become the conversation's reading: it would understate a
// context the turn has already filled.
func TestTurnIgnoresASubagentsUsage(t *testing.T) {
	fixture := strings.Join([]string{
		`{"type":"assistant","parent_tool_use_id":null,"message":{"model":"claude-sonnet-5","usage":{"input_tokens":150000},"content":[]}}`,
		`{"type":"assistant","parent_tool_use_id":"toolu_1","message":{"model":"claude-sonnet-5","usage":{"input_tokens":900},"content":[]}}`,
		resultLine(sessionID),
	}, "\n")

	if usage := usageOf(runTurn(t, fixture)); usage.Tokens != 150000 {
		t.Fatalf("want the turn's own reading, got %d", usage.Tokens)
	}
}
