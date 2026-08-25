package opencode

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
)

const cockpitID = "11111111-2222-4333-8444-555555555555"
const nativeID = "ses_fc6374637ffeU6S5j7RziNAGjI"

// testRunner is the runner over a store that already maps the conversation:
// the pre-created session carries the cockpit's id in its metadata, which is
// what every turn resolves through.
func testRunner(t *testing.T) *runner {
	t.Helper()
	return &runner{
		sessions: fixtureRepository(t, sessionFixtures),
		create: func(workdir, title, cockpitID string) (string, error) {
			t.Fatal("this turn must not create a session")
			return "", nil
		},
	}
}

// Fixture lines recorded from `opencode run --format json` on 1.18.23.
func textLine(session, message, text string) string {
	return `{"type":"text","timestamp":1787675751105,"sessionID":"` + session + `","part":{"id":"prt_1","messageID":"` + message + `","sessionID":"` + session + `","type":"text","text":"` + text + `","time":{"start":1787675750965,"end":1787675751089}}}`
}

func toolLine(session, tool string) string {
	return `{"type":"tool_use","timestamp":1787675808870,"sessionID":"` + session + `","part":{"type":"tool","tool":"` + tool + `","callID":"call_1","state":{"status":"completed","input":{},"output":"","title":""}}}`
}

func stepFinishLine(session, reason string) string {
	return `{"type":"step_finish","timestamp":1787675751105,"sessionID":"` + session + `","part":{"id":"prt_2","reason":"` + reason + `","messageID":"msg_1","sessionID":"` + session + `","type":"step-finish","tokens":{"total":8484,"input":6678,"output":14,"reasoning":0,"cache":{"write":0,"read":1792}},"cost":0}}`
}

// runTurn feeds one output fixture through the runner's parser, line by line,
// the way the assistant reads the file a turn writes.
func runTurn(t *testing.T, r *runner, fixture string) []assistant.Event {
	t.Helper()
	out := make(chan assistant.Event, 256)
	parser := r.Parse(cockpitID, out)

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
	// Finishing is what reads the context reading, so the channel is drained
	// once more: it is the only event that arrives after the last line.
	drain()
	if err != nil {
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

// The first turn creates the session through opencode's own API, because run
// cannot create one under a caller's id: the conversation's id goes into the
// metadata and opencode's own id into the argv.
func TestTheFirstTurnCreatesTheSessionAhead(t *testing.T) {
	var gotTitle, gotCockpit string
	r := &runner{
		sessions: fixtureRepository(t, sessionFixtures),
		create: func(workdir, title, cockpit string) (string, error) {
			gotTitle, gotCockpit = title, cockpit
			return "ses_fresh", nil
		},
	}
	cmd, err := r.Command(assistant.TurnRequest{
		SessionID: "99999999-2222-4333-8444-555555555555",
		Title:     "A conversation title",
		Workdir:   t.TempDir(),
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if gotTitle != "A conversation title" || gotCockpit != "99999999-2222-4333-8444-555555555555" {
		t.Fatalf("created %q for %q, want the conversation's name and id", gotTitle, gotCockpit)
	}
	argv := strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
	for _, want := range []string{"opencode run", "--session ses_fresh", "--format json", "--auto", "-- hello"} {
		if !strings.Contains(argv, want) {
			t.Fatalf("want %q in the first turn argv, got %q", want, argv)
		}
	}
}

// A creation that fails has to fail the turn: without the session there is
// nothing the run could resume.
func TestAFailedCreationFailsTheTurn(t *testing.T) {
	r := &runner{
		sessions: fixtureRepository(t, sessionFixtures),
		create: func(string, string, string) (string, error) {
			return "", errors.New("no server")
		},
	}
	if _, err := r.Command(assistant.TurnRequest{SessionID: cockpitID, Prompt: "hello"}); err == nil {
		t.Fatal("want the creation failure reported")
	}
}

// A resumed turn runs against opencode's own id, resolved through the
// metadata the creation left on the session.
func TestAResumedTurnResolvesTheCockpitId(t *testing.T) {
	cmd, err := testRunner(t).Command(assistant.TurnRequest{
		SessionID: cockpitID,
		Resume:    true,
		Workdir:   t.TempDir(),
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	argv := strings.Join(cmd.Args, " ")
	if !strings.Contains(argv, "--session "+nativeID) {
		t.Fatalf("want opencode's own id in the argv, got %q", argv)
	}
	if strings.Contains(argv, cockpitID) {
		t.Fatalf("the cockpit's id means nothing to opencode, got %q", argv)
	}
}

// A prompt is text, whatever it starts with. run's message is positional, so
// it goes behind the end of options separator, which run folds into the
// message (verified on 1.18.23).
func TestATurnCarriesADashLeadingPromptAsText(t *testing.T) {
	for name, prompt := range map[string]string{
		"a php option somebody pasted": "-dxdebug.idekey=PHPSTORM",
		"a long flag":                  "--help",
		"a bare dash":                  "-",
		"an ordinary prompt":           "Fix the login redirect",
	} {
		cmd, err := testRunner(t).Command(assistant.TurnRequest{
			SessionID: cockpitID, Resume: true, Workdir: t.TempDir(), Prompt: prompt,
		})
		if err != nil {
			t.Fatalf("%s: command: %v", name, err)
		}
		if len(cmd.Args) < 2 || cmd.Args[len(cmd.Args)-2] != "--" || cmd.Args[len(cmd.Args)-1] != prompt {
			t.Fatalf("%s: want the prompt last behind the separator, got %v", name, cmd.Args)
		}
	}
}

func TestSessionExistsAnswersUnderTheCockpitId(t *testing.T) {
	r := testRunner(t)
	if !r.SessionExists(cockpitID) {
		t.Fatal("the mapped conversation has to exist under the cockpit's id")
	}
	if r.SessionExists("99999999-2222-4333-8444-555555555555") {
		t.Fatal("an unknown id must not exist")
	}
}

// Every text record is a block of its own in this output format, so the
// blank line between two of them comes out of the records and never out of
// the text.
func TestTextPartsKeepTheirBlankLine(t *testing.T) {
	fixture := strings.Join([]string{
		textLine(nativeID, "msg_1", "Wasser trägt Leben."),
		toolLine(nativeID, "bash"),
		stepFinishLine(nativeID, "tool-calls"),
		textLine(nativeID, "msg_2", "Feuer verlangt Respekt."),
		stepFinishLine(nativeID, "stop"),
	}, "\n")
	events := runTurn(t, testRunner(t), fixture)
	if err := errorOf(events); err != nil {
		t.Fatalf("want a clean turn, got %v", err)
	}
	if got := textOf(events); got != "Wasser trägt Leben.\n\nFeuer verlangt Respekt." {
		t.Fatalf("want the two blocks apart, got %q", got)
	}
	var tools []string
	for _, ev := range events {
		if ev.Kind == assistant.EventTool {
			tools = append(tools, ev.Text)
		}
	}
	if strings.Join(tools, ",") != "bash" {
		t.Fatalf("want the tool named, got %v", tools)
	}
}

// A single block keeps its own ends: nothing in front of the turn's first
// text and nothing behind its last.
func TestASingleBlockStaysUntouched(t *testing.T) {
	fixture := textLine(nativeID, "msg_1", "ok") + "\n" + stepFinishLine(nativeID, "stop")
	if got := textOf(runTurn(t, testRunner(t), fixture)); got != "ok" {
		t.Fatalf("want the answer untouched at both ends, got %q", got)
	}
}

// The run writes no closing record at all: its own end is the step that
// finished with a reason that does not hand on into a tool call. A run that
// stops earlier was killed mid-turn.
func TestATurnWithoutItsEndFails(t *testing.T) {
	events := runTurn(t, testRunner(t), textLine(nativeID, "msg_1", "half"))
	err := errorOf(events)
	if err == nil || !strings.Contains(err.Error(), "stopped before it finished") {
		t.Fatalf("want the missing end named, got %v", err)
	}

	events = runTurn(t, testRunner(t), textLine(nativeID, "msg_1", "half")+"\n"+stepFinishLine(nativeID, "tool-calls"))
	if errorOf(events) == nil {
		t.Fatal("a step that handed on into a tool call is not an end")
	}
}

func TestAnErrorRecordFailsTheTurn(t *testing.T) {
	fixture := strings.Join([]string{
		textLine(nativeID, "msg_1", "half"),
		`{"type":"error","timestamp":1,"sessionID":"` + nativeID + `","error":{"name":"APIError","data":{"message":"boom"}}}`,
		stepFinishLine(nativeID, "stop"),
	}, "\n")
	err := errorOf(runTurn(t, testRunner(t), fixture))
	if err == nil || errors.Is(err, assistant.ErrNotLoggedIn) {
		t.Fatalf("want the generic failure, got %v", err)
	}
}

// The one error the user can act on: opencode names a provider without
// usable credentials in its own error record.
func TestAMissingLoginIsNamed(t *testing.T) {
	fixture := `{"type":"error","timestamp":1,"sessionID":"` + nativeID + `","error":{"name":"ProviderAuthError","data":{"providerID":"github-copilot","message":"no credentials"}}}`
	if err := errorOf(runTurn(t, testRunner(t), fixture)); !errors.Is(err, assistant.ErrNotLoggedIn) {
		t.Fatalf("want the login sentinel, got %v", err)
	}
}

func TestTurnRefusesAForeignSession(t *testing.T) {
	fixture := textLine("ses_somebodyelse", "msg_1", "hi") + "\n" + stepFinishLine("ses_somebodyelse", "stop")
	if errorOf(runTurn(t, testRunner(t), fixture)) == nil {
		t.Fatal("want a mismatched session id to fail the turn")
	}
}

// A line nobody can decode is logged and read past: the run keeps going after
// such a record, so the turn's outcome stays with the records the parser does
// evaluate. Noise alone still fails the turn, through the missing end.
func TestMalformedOutputIsReadPastAndTheEndDecides(t *testing.T) {
	fixture := "{not json at all}\n" + textLine(nativeID, "msg_1", "hi") + "\n" + stepFinishLine(nativeID, "stop")
	events := runTurn(t, testRunner(t), fixture)
	if err := errorOf(events); err != nil {
		t.Fatalf("want the noisy line skipped, got %v", err)
	}
	if got := textOf(events); got != "hi" {
		t.Fatalf("want the answer kept around the noise, got %q", got)
	}
	if errorOf(runTurn(t, testRunner(t), "{not json at all}")) == nil {
		t.Fatal("want a turn with nothing readable to fail")
	}
}

// The primary login path is the ProviderAuthError record, decided in Line;
// Diagnose stays the fallback for a run that never got going and only spoke
// on standard error, read with the shared login pattern.
func TestDiagnoseNamesAMissingLogin(t *testing.T) {
	r := testRunner(t)
	parser := r.Parse(cockpitID, make(chan assistant.Event, 1))
	generic := errors.New("The coder stopped before it finished the answer.")
	if err := parser.Diagnose(generic, "Error: 401 Unauthorized, the provider rejected the request"); !errors.Is(err, assistant.ErrNotLoggedIn) {
		t.Fatalf("want the login sentinel, got %v", err)
	}
	if err := parser.Diagnose(generic, "panic: runtime error: index out of range"); err != nil {
		t.Fatalf("want a crash left unnamed, so the generic failure stands, got %v", err)
	}
}

func TestCapabilityCheckNamesTheFlagsItNeeds(t *testing.T) {
	for _, flag := range []string{"--session", "--format", "--auto"} {
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

// usageRunner is a runner whose store also answers the usage query: the
// context reading is not on the run's output at all, it stands on the newest
// assistant message in opencode's store.
func usageRunner(t *testing.T, usage []map[string]any) *runner {
	t.Helper()
	base := fixtureRepository(t, sessionFixtures)
	plain := base.query
	base.query = func(sql string) ([]byte, error) {
		if strings.Contains(sql, "AS tokens") {
			data, err := json.Marshal(usage)
			if err != nil {
				t.Fatal(err)
			}
			return data, nil
		}
		return plain(sql)
	}
	return &runner{sessions: base}
}

func usageOf(events []assistant.Event) assistant.ContextUsage {
	for _, ev := range events {
		if ev.Kind == assistant.EventUsage && ev.Usage != nil {
			return *ev.Usage
		}
	}
	return assistant.ContextUsage{}
}

// opencode writes what a turn consumed onto the assistant message in its
// store; the window stays unmeasured until a reading proves what an opencode
// model holds, so the page shows tokens without a fill.
func TestTurnReportsTheContextFromTheStore(t *testing.T) {
	r := usageRunner(t, []map[string]any{{"model": "big-pickle", "tokens": 8471}})
	events := runTurn(t, r, textLine(nativeID, "msg_1", "ok")+"\n"+stepFinishLine(nativeID, "stop"))
	usage := usageOf(events)
	if usage.Tokens != 8471 || usage.Model != "big-pickle" {
		t.Fatalf("want the store's reading, got %+v", usage)
	}
	if usage.Window != 0 || usage.Known() {
		t.Fatalf("want no window until a reading proves one, got %d", usage.Window)
	}
}

// A session that recorded nothing reports nothing: the page keeps the number
// it had instead of showing a guess.
func TestTurnWithoutARecordReportsNothing(t *testing.T) {
	r := usageRunner(t, []map[string]any{})
	for _, ev := range runTurn(t, r, textLine(nativeID, "msg_1", "ok")+"\n"+stepFinishLine(nativeID, "stop")) {
		if ev.Kind == assistant.EventUsage {
			t.Fatalf("want no reading without a record, got %+v", ev.Usage)
		}
	}
}
