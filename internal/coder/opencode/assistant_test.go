package opencode

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
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

// A turn runs headless, and --auto answers permissions only: a model that
// called the question tool left the run waiting forever for an answer nobody
// could give. So every turn takes the tool away through opencode's config
// environment, exactly this JSON, which is what 1.18.30 was verified against.
func TestATurnTakesTheQuestionToolAway(t *testing.T) {
	cmd, err := testRunner(t).Command(assistant.TurnRequest{
		SessionID: cockpitID, Resume: true, Workdir: t.TempDir(), Prompt: "hello",
	})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	const want = `OPENCODE_CONFIG_CONTENT={"tools":{"question":false}}`
	if len(cmd.Env) != 1 || cmd.Env[0] != want {
		t.Fatalf("want the environment %q, got %v", want, cmd.Env)
	}
	var parsed struct {
		Tools map[string]bool `json:"tools"`
	}
	if err := json.Unmarshal([]byte(assistantConfig), &parsed); err != nil {
		t.Fatalf("the config has to be valid JSON: %v", err)
	}
	if enabled, ok := parsed.Tools["question"]; !ok || enabled {
		t.Fatalf("want the question tool switched off, got %v", parsed.Tools)
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
		textLine(nativeID, "msg_1", "Water carries life."),
		toolLine(nativeID, "bash"),
		stepFinishLine(nativeID, "tool-calls"),
		textLine(nativeID, "msg_2", "Fire demands respect."),
		stepFinishLine(nativeID, "stop"),
	}, "\n")
	events := runTurn(t, testRunner(t), fixture)
	if err := errorOf(events); err != nil {
		t.Fatalf("want a clean turn, got %v", err)
	}
	if got := textOf(events); got != "Water carries life.\n\nFire demands respect." {
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

// A model the turn names rides behind -m, before the end of options separator;
// a turn that names none carries no such flag, so opencode's own default
// stands as it always did.
func TestATurnCarriesTheModelBehindItsFlag(t *testing.T) {
	cmd, err := testRunner(t).Command(assistant.TurnRequest{SessionID: cockpitID, Resume: true, Workdir: t.TempDir(), Prompt: "hello", Model: "github-copilot/claude-haiku-4.5"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	argv := strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
	if !strings.Contains(argv, " -m github-copilot/claude-haiku-4.5 -- hello") {
		t.Fatalf("want the model behind -m and before the prompt, got %q", argv)
	}
	cmd, err = testRunner(t).Command(assistant.TurnRequest{SessionID: cockpitID, Resume: true, Workdir: t.TempDir(), Prompt: "hello"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if strings.Contains(strings.Join(cmd.Args, " "), " -m ") {
		t.Fatalf("want no model flag on a turn that names none, got %v", cmd.Args)
	}
	// A check and a reaction come as a fresh session of their own with the
	// model the service resolved, the ring's chat pick where nothing narrower
	// stands, and that request builds the same flag behind the created
	// session.
	fresh := &runner{
		sessions: fixtureRepository(t, sessionFixtures),
		create: func(string, string, string) (string, error) {
			return "ses_fresh", nil
		},
	}
	held, err := fresh.Command(assistant.TurnRequest{SessionID: "99999999-2222-4333-8444-555555555555", Title: "cockpit check: readme-task", Workdir: t.TempDir(), Prompt: "check", Model: "fable"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	argv = strings.Join(append([]string{held.Name}, held.Args...), " ")
	if !strings.Contains(argv, " --session ses_fresh ") || !strings.Contains(argv, " -m fable -- check") {
		t.Fatalf("want a check's fresh session started on the resolved model, got %q", argv)
	}
}

// `opencode models` prints one provider/model per line (recorded from
// 1.18.30). Everything that is not one is dropped: a blank line, a warning
// printed on the way, a name no turn could carry.
func TestTheModelListReadsOneNamePerLine(t *testing.T) {
	names := parseModelList("opencode/big-pickle\n\ngithub-copilot/claude-haiku-4.5\nWARN some warning text\n  github-copilot/gpt-5.4-mini  \nnot a model at all\n")
	if strings.Join(names, ",") != "opencode/big-pickle,github-copilot/claude-haiku-4.5,github-copilot/gpt-5.4-mini" {
		t.Fatalf("want the provider/model lines alone, got %v", names)
	}
	if parseModelList("") != nil {
		t.Fatal("want no names out of no output")
	}
}

// The list is fetched in the background and never on the request path: the
// first read answers empty and starts the fetch, a read inside the ten
// minutes answers the cache without a process, a read past them answers the
// cache and starts the refresh, and a refresh that failed keeps what stood.
func TestTheModelListRefreshesInTheBackgroundAndKeepsWhatStood(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	fail := false
	list := newModelList(func(context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		runs++
		if fail {
			return nil, errors.New("opencode is broken")
		}
		return []string{"opencode/big-pickle"}, nil
	})
	settled := func() {
		t.Helper()
		waitSettled(t, list)
	}
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return runs
	}

	start := time.Now()
	if got := list.names(start); len(got) != 0 {
		t.Fatalf("want the first read to answer nothing while the fetch runs, got %v", got)
	}
	settled()
	if count() != 1 {
		t.Fatalf("want the first read to start one fetch, got %d", count())
	}
	if got := list.names(start.Add(time.Minute)); strings.Join(got, ",") != "opencode/big-pickle" || count() != 1 {
		t.Fatalf("want the cache answered without a process inside the ten minutes, got %v after %d runs", got, count())
	}

	mu.Lock()
	fail = true
	mu.Unlock()
	if got := list.names(list.at.Add(modelListTTL)); strings.Join(got, ",") != "opencode/big-pickle" {
		t.Fatalf("want the stale cache answered while the refresh runs, got %v", got)
	}
	settled()
	if count() != 2 {
		t.Fatalf("want the read past the ten minutes to start a refresh, got %d runs", count())
	}
	if got := list.names(list.at.Add(time.Minute)); strings.Join(got, ",") != "opencode/big-pickle" || count() != 2 {
		t.Fatalf("want a failed refresh to keep what stood and wait, got %v after %d runs", got, count())
	}
}

// waitSettled waits for the refresh a read started to come back, which every
// refresh does: a hung one is ended by its deadline.
func waitSettled(t *testing.T, list *modelList) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list.mu.Lock()
		done := !list.refreshing
		list.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the refresh never came back")
}

// A refresh that hangs is ended by its deadline and read as a failed one: the
// refreshing flag is cleared, what stood is kept, and the read past the TTL
// starts the next try instead of serving the stale list for the life of the
// process. The fetch itself is what the deadline reaches, through its context.
func TestAHungModelListRefreshIsEndedByItsDeadline(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	ended := 0
	list := newModelList(func(ctx context.Context) ([]string, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		<-ctx.Done()
		mu.Lock()
		ended++
		mu.Unlock()
		return nil, ctx.Err()
	})
	list.timeout = 50 * time.Millisecond
	list.cached = []string{"opencode/big-pickle"}
	count := func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return runs, ended
	}

	start := time.Now()
	if got := list.names(start); strings.Join(got, ",") != "opencode/big-pickle" {
		t.Fatalf("want what stood answered while the refresh runs, got %v", got)
	}
	waitSettled(t, list)
	if runs, ended := count(); runs != 1 || ended != 1 {
		t.Fatalf("want the one fetch started and ended by its context, got %d started and %d ended", runs, ended)
	}
	if got := list.names(list.at.Add(time.Minute)); strings.Join(got, ",") != "opencode/big-pickle" {
		t.Fatalf("want a failed refresh to keep what stood, got %v", got)
	}
	if runs, _ := count(); runs != 1 {
		t.Fatalf("want a read inside the TTL to start nothing, got %d runs", runs)
	}
	list.names(list.at.Add(modelListTTL))
	waitSettled(t, list)
	if runs, ended := count(); runs != 2 || ended != 2 {
		t.Fatalf("want the read past the TTL to try again, got %d started and %d ended", runs, ended)
	}
}

// The real fetch runs `opencode models` under the context: a process that
// never ends is ended at the deadline and answers as a failure, never as a
// list, and the refresh that ran it comes back.
func TestListModelsEndsTheProcessAtTheDeadline(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep on this host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	names, err := listModelsWith(ctx, "sleep", "30")
	if err == nil || names != nil {
		t.Fatalf("want a failure and no list from a hung process, got %v and %v", names, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the deadline did not end the process, the call took %s", time.Since(start))
	}
	if !strings.Contains(err.Error(), "did not answer in time") {
		t.Fatalf("want the deadline named, got %v", err)
	}
}

// The warm up at the serve start is the coder's list capability alone: a coder
// with no conversation probe at all starts its first fetch when asked, so the
// New coder dialog of a host whose opencode cannot hold a conversation opens
// filled the same way.
func TestWarmingTheModelsHangsOnNoOtherCapability(t *testing.T) {
	var mu sync.Mutex
	runs := 0
	c := &Coder{modelCache: newModelList(func(context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		runs++
		return []string{"opencode/big-pickle"}, nil
	})}
	c.models = coder.NewModelRepository(nil, "opencode", opencodeModelsNote, c.cliModels)
	coder.WarmModels(c)
	waitSettled(t, c.modelCache)
	mu.Lock()
	defer mu.Unlock()
	if runs != 1 {
		t.Fatalf("want the warm up to start the first fetch, got %d runs", runs)
	}
}

// A model opencode does not know has no refusal of its own to name: captured
// on 1.18.32 with `opencode run --format json -m no/such-model -- hi` and
// again with `-m github-copilot/bogus-model-xyz`, exit code 1 both times, an
// error record naming UnknownError and its message, nothing else, no step end
// and nothing on standard error. So it ends with the cockpit's sentence and
// opencode's own words quoted once behind it, which is the path every error
// record the cockpit has no name for takes.
func TestAnErrorRecordQuotesOpencodeOnce(t *testing.T) {
	fixture := `{"type":"error","timestamp":1790190493382,"sessionID":"` + nativeID + `","error":{"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_52011bce"}}}`
	err := errorOf(runTurn(t, testRunner(t), fixture))
	if err == nil || err.Error() != "The coder could not finish this answer. The coder said: UnknownError: Unexpected server error. Check server logs for details." {
		t.Fatalf("want the cockpit's sentence with opencode quoted once, got %v", err)
	}
	var refusal *assistant.Refusal
	if errors.As(err, &refusal) {
		t.Fatalf("opencode names no model refusal, got %v", err)
	}
	// A record without words leaves the frame alone.
	err = errorOf(runTurn(t, testRunner(t), `{"type":"error","timestamp":1,"sessionID":"`+nativeID+`","error":{}}`))
	if err == nil || err.Error() != "The coder could not finish this answer." {
		t.Fatalf("want the frame alone, got %v", err)
	}
}
