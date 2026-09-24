package copilot

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/assistant"
)

const sessionID = "11111111-2222-4333-8444-555555555555"

// testRunner is the runner over an empty session store. A turn reads the
// session's own event log for its context reading, so the store is real and
// simply holds nothing unless a test writes a log into it.
func testRunner(t *testing.T) *runner {
	t.Helper()
	return &runner{sessions: &sessionRepository{stateRoot: t.TempDir()}}
}

// turnEvents puts an event log where the store looks for this session's one, the
// same writer the activity checks use.
func turnEvents(t *testing.T, r *runner, lines ...string) {
	t.Helper()
	writeEvents(t, r.sessions.stateRoot, sessionID, lines...)
}

func deltaLine(id, text string) string {
	return `{"type":"assistant.message_delta","data":{"messageId":"` + id + `","deltaContent":"` + text + `"}}`
}

func messageLine(id, text string) string {
	return `{"type":"assistant.message","data":{"messageId":"` + id + `","content":"` + text + `","toolRequests":[]}}`
}

func resultLine(id string) string {
	return `{"type":"result","sessionId":"` + id + `","exitCode":0}`
}

// argvOf builds the process one turn runs, which is what the runner decides now:
// the assistant package starts it and reads its file.
func argvOf(t *testing.T, resume bool) string {
	t.Helper()
	r := testRunner(t)
	cmd, err := r.Command(assistant.TurnRequest{
		SessionID: sessionID,
		Resume:    resume,
		Title:     "A conversation title",
		Workdir:   t.TempDir(),
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	return strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
}

// runTurn feeds one output fixture through the runner's parser, line by line,
// the way the assistant reads the file a turn writes. A parser error and a
// missing closing record both end the turn, so both come back as the error
// event the service would record.
func runTurn(t *testing.T, fixture string) []assistant.Event {
	t.Helper()
	return runTurnWith(t, testRunner(t), fixture)
}

// runTurnWith is the same for a runner a test prepared, which is what a check on
// the context reading needs: that one is not on the output at all, it stands in
// the session's event log.
func runTurnWith(t *testing.T, r *runner, fixture string) []assistant.Event {
	t.Helper()
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
	// Finishing is what sends the context reading here, so the channel is drained
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

func TestTurnAssemblesDeltasWithoutDoublingTheFinalMessage(t *testing.T) {
	fixture := strings.Join([]string{
		`{"type":"session.mcp_servers_loaded","data":{"servers":[]},"ephemeral":true}`,
		`{"type":"user.message","data":{"content":"hello"}}`,
		`{"type":"assistant.turn_start","data":{"turnId":"0"}}`,
		deltaLine("m1", "Hel"),
		deltaLine("m1", "lo"),
		messageLine("m1", "Hello"),
		`{"type":"assistant.idle","data":{}}`,
		resultLine(sessionID),
	}, "\n")

	events := runTurn(t, fixture)
	if err := errorOf(events); err != nil {
		t.Fatalf("want a clean turn, got %v", err)
	}
	if got := textOf(events); got != "Hello" {
		t.Fatalf("want the final message record ignored after its deltas, got %q", got)
	}
}

func TestTurnFallsBackToTheFullMessage(t *testing.T) {
	fixture := messageLine("m1", "No deltas here") + "\n" + resultLine(sessionID)

	events := runTurn(t, fixture)
	if got := textOf(events); got != "No deltas here" {
		t.Fatalf("want the complete answer when a version sends no deltas, got %q", got)
	}
}

// Recorded from the GitHub Copilot CLI: a turn with a tool call answers in two
// messages, and the id on the records is what says where one ends and the next
// begins. Appended without that seam the run produced "depth.Fire", two
// markdown blocks welded into one word.
func TestSeparateMessagesKeepTheirBlankLine(t *testing.T) {
	const first = "80052770-f70b-495b-85e2-86c9c0c7d97f"
	const second = "c8d2365e-2789-49df-818d-58c366163a11"
	fixture := strings.Join([]string{
		`{"type":"assistant.message_start","data":{"messageId":"` + first + `"}}`,
		deltaLine(first, "Water carries life and finds its way patiently."),
		deltaLine(first, " Its calm surface can hide great depth."),
		messageLine(first, "Water carries life and finds its way patiently. Its calm surface can hide great depth."),
		`{"type":"tool.execution_start","data":{"toolName":"bash","arguments":{}}}`,
		`{"type":"assistant.message_start","data":{"messageId":"` + second + `"}}`,
		deltaLine(second, "Fire gives warmth, but it demands respect."),
		messageLine(second, "Fire gives warmth, but it demands respect."),
		resultLine(sessionID),
	}, "\n")

	events := runTurn(t, fixture)
	if err := errorOf(events); err != nil {
		t.Fatalf("want a clean turn, got %v", err)
	}
	want := "Water carries life and finds its way patiently. Its calm surface can hide great depth." +
		"\n\nFire gives warmth, but it demands respect."
	if got := textOf(events); got != want {
		t.Fatalf("want the two messages apart, got %q", got)
	}
}

// Inside one message nothing is inserted at all: the deltas are pieces of one
// markdown block. Nothing goes in front of the first message of a turn and
// nothing behind its last either, so the stored answer keeps its own ends.
func TestDeltasOfOneMessageAreJoinedUntouched(t *testing.T) {
	fixture := strings.Join([]string{
		deltaLine("m1", "Water"),
		deltaLine("m1", " carries"),
		deltaLine("m1", " life"),
		deltaLine("m1", "."),
		messageLine("m1", "Water carries life."),
		resultLine(sessionID),
	}, "\n")

	if got := textOf(runTurn(t, fixture)); got != "Water carries life." {
		t.Fatalf("want the deltas of one message joined and the answer untouched at both ends, got %q", got)
	}
}

// The fallback that takes the full message record needs the same seam, or the
// messages stick together whenever a version stops sending deltas.
func TestFullMessagesKeepTheirBlankLine(t *testing.T) {
	fixture := strings.Join([]string{
		messageLine("m1", "I am looking at the coder."),
		`{"type":"tool.execution_start","data":{"toolName":"bash","arguments":{}}}`,
		messageLine("m2", "Project tetris is up"),
		resultLine(sessionID),
	}, "\n")

	if got := textOf(runTurn(t, fixture)); got != "I am looking at the coder.\n\nProject tetris is up" {
		t.Fatalf("want the two full messages apart, got %q", got)
	}
}

func TestTurnArgvFirstAndResume(t *testing.T) {
	argv := argvOf(t, false)
	for _, want := range []string{
		"copilot -p hello",
		"--session-id " + sessionID,
		"--name A conversation title",
		"--output-format json",
		"--log-level none",
	} {
		if !strings.Contains(argv, want) {
			t.Fatalf("want %q in the first turn argv, got %q", want, argv)
		}
	}
	if strings.Contains(argv, "--available-tools") {
		t.Fatalf("want no tool restriction, a conversation has the same tools as a terminal, got %q", argv)
	}

	argv = argvOf(t, true)
	if !strings.Contains(argv, "--resume "+sessionID) {
		t.Fatalf("want a resumed turn, got %q", argv)
	}
	if strings.Contains(argv, "--session-id") || strings.Contains(argv, "--name") {
		t.Fatalf("want no session creation flags on a resumed turn, got %q", argv)
	}
}

// A prompt is text, whatever it starts with. It rides as the value of -p, which
// copilot requires an argument for and therefore takes whatever the next word is
// (measured on GitHub Copilot CLI 1.0.78), so a prompt like
// -dxdebug.idekey=PHPSTORM reaches the turn as text. An end of options separator
// would be consumed as the prompt itself, which is why this argv carries none.
func TestATurnCarriesADashLeadingPromptAsText(t *testing.T) {
	prompts := map[string]string{
		"a php option somebody pasted": "-dxdebug.idekey=PHPSTORM",
		"a long flag":                  "--help",
		"a bare dash":                  "-",
		"an ordinary prompt":           "Fix the login redirect",
	}
	for name, prompt := range prompts {
		for _, resume := range []bool{false, true} {
			r := testRunner(t)
			cmd, err := r.Command(assistant.TurnRequest{
				SessionID: sessionID,
				Resume:    resume,
				Title:     "A conversation title",
				Workdir:   t.TempDir(),
				Prompt:    prompt,
			})
			if err != nil {
				t.Fatalf("%s (resume %v): command: %v", name, resume, err)
			}
			if len(cmd.Args) < 2 || cmd.Args[0] != "-p" || cmd.Args[1] != prompt {
				t.Fatalf("%s (resume %v): want the prompt as the value of -p, got %v", name, resume, cmd.Args)
			}
			for _, arg := range cmd.Args {
				if arg == "--" {
					t.Fatalf("%s (resume %v): a separator here would be the prompt, got %v", name, resume, cmd.Args)
				}
			}
		}
	}
}

// The assistant is not about one project: it reaches the projects, the cockpit
// binary and its own workspace, and copilot refuses every path outside the
// working directory and its trusted folders. Every turn lifts that
// verification, there is no scoped caller.
func TestEveryTurnLiftsThePathVerification(t *testing.T) {
	r := testRunner(t)
	cmd, err := r.Command(assistant.TurnRequest{
		SessionID: sessionID,
		Workdir:   t.TempDir(),
		Prompt:    "hello",
	})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	if argv := strings.Join(cmd.Args, " "); !strings.Contains(argv, "--allow-all-paths") {
		t.Fatalf("want the path verification lifted on every turn, got %q", argv)
	}
}

func TestToolUseSurfacesAsEventsWithTheirNames(t *testing.T) {
	fixture := strings.Join([]string{
		deltaLine("m1", "starting"),
		`{"type":"tool.execution_start","data":{"toolName":"view","arguments":{}}}`,
		`{"type":"tool.execution_start","data":{"toolName":"apply_patch","arguments":{}}}`,
		messageLine("m2", "done"),
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
	if strings.Join(tools, ",") != "view,apply_patch" {
		t.Fatalf("want both tool events with their names, got %v", tools)
	}
	if !strings.Contains(textOf(events), "done") {
		t.Fatalf("want the answer text delivered, got %q", textOf(events))
	}
}

func TestTurnRefusesAForeignSession(t *testing.T) {
	fixture := messageLine("m1", "hi") + "\n" + resultLine("99999999-2222-4333-8444-555555555555")

	events := runTurn(t, fixture)
	if errorOf(events) == nil {
		t.Fatal("want a mismatched session id to fail the turn")
	}
}

func TestTurnFailsWithoutAResult(t *testing.T) {
	events := runTurn(t, messageLine("m1", "half an answer"))
	if errorOf(events) == nil {
		t.Fatal("want a turn that ends without a result to fail")
	}
}

// A line nobody can decode is logged and read past: the run keeps going after
// such a record, so the turn's outcome stays with the records the parser does
// evaluate. Noise alone still fails the turn, through the missing result.
func TestMalformedOutputIsReadPastAndTheResultDecides(t *testing.T) {
	events := runTurn(t, "{not json at all}\n"+messageLine("m1", "hi")+"\n"+resultLine(sessionID))
	if err := errorOf(events); err != nil {
		t.Fatalf("want the noisy line skipped, got %v", err)
	}
	if got := textOf(events); got != "hi" {
		t.Fatalf("want the answer kept around the noise, got %q", got)
	}

	events = runTurn(t, "{not json at all}")
	err := errorOf(events)
	if err == nil {
		t.Fatal("want a turn with nothing readable to fail")
	}
	if !strings.Contains(err.Error(), "stopped before it finished") {
		t.Fatalf("want the missing result named, got %q", err)
	}
}

// A known record type whose payload has an unexpected shape reads the same
// way: logged, skipped, and the turn goes on to its result.
func TestARecordWithAForeignPayloadShapeIsReadPast(t *testing.T) {
	fixture := strings.Join([]string{
		`{"type":"assistant.message","data":"a plain string where an object stands"}`,
		messageLine("m1", "hi"),
		resultLine(sessionID),
	}, "\n")

	events := runTurn(t, fixture)
	if err := errorOf(events); err != nil {
		t.Fatalf("want the foreign shape read past, got %v", err)
	}
	if got := textOf(events); got != "hi" {
		t.Fatalf("want the answer delivered, got %q", got)
	}
}

// Measured on copilot 1.0.76 in a container nobody logged in: exit code 1,
// standard output empty, and the complaint on standard error. That is the only
// place it says it, so the parser reads it from there.
func TestDiagnoseNamesAMissingLogin(t *testing.T) {
	stderr := strings.Join([]string{
		"Error: No authentication information found.",
		"",
		"Copilot can be authenticated with GitHub using an OAuth Token or a Fine-Grained Personal Access Token.",
		"",
		"To authenticate, you can use any of the following methods:",
		"  • Start 'copilot' and run the '/login' command",
		"  • Set the COPILOT_GITHUB_TOKEN, GH_TOKEN, or GITHUB_TOKEN environment variable",
		"  • Run 'gh auth login' to authenticate with the GitHub CLI",
	}, "\n")

	r := testRunner(t)
	parser := r.Parse(sessionID, make(chan assistant.Event, 1))
	generic := errors.New("The coder stopped before it finished the answer.")
	if err := parser.Diagnose(generic, stderr); !errors.Is(err, assistant.ErrNotLoggedIn) {
		t.Fatalf("want the login sentinel, got %v", err)
	}
	if err := parser.Diagnose(generic, "panic: runtime error: index out of range"); err != nil {
		t.Fatalf("want a crash left unnamed, so the generic failure stands, got %v", err)
	}
}

func TestCapabilityCheckNamesTheFlagsItNeeds(t *testing.T) {
	for _, flag := range []string{"--prompt", "--output-format", "--session-id", "--resume", "--allow-all-tools"} {
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

// copilot writes how full its context stands into the session's event log when a
// run shuts down, and nowhere near its output. A turn is one run, so the newest
// record is where the conversation stands: a resumed session carries one per turn
// taken so far.
func TestTurnReportsTheContextOfTheNewestRun(t *testing.T) {
	r := testRunner(t)
	turnEvents(t, r,
		`{"type":"session.start","data":{}}`,
		`{"type":"session.shutdown","data":{"shutdownType":"routine","currentModel":"claude-sonnet-5","currentTokens":30125,"systemTokens":17480}}`,
		`{"type":"session.shutdown","data":{"shutdownType":"routine","currentModel":"claude-sonnet-5","currentTokens":48200,"systemTokens":17480}}`,
	)
	usage := usageOf(runTurnWith(t, r, messageLine("m1", "hi")+"\n"+resultLine(sessionID)))
	if usage.Tokens != 48200 {
		t.Fatalf("want the newest run's tokens, got %d", usage.Tokens)
	}
	if usage.Window != 200000 {
		t.Fatalf("want the window of the model it names, got %d", usage.Window)
	}
	if usage.Percent() != 24 {
		t.Fatalf("want 24 percent of the window, got %d", usage.Percent())
	}
}

// A model nobody has a window for shows no fill at all: a made up figure under a
// confident ring would be worse than an empty one.
func TestTurnLeavesAnUnknownWindowUnmeasured(t *testing.T) {
	r := testRunner(t)
	turnEvents(t, r,
		`{"type":"session.shutdown","data":{"currentModel":"gpt-9-imaginary","currentTokens":16816}}`,
	)
	usage := usageOf(runTurnWith(t, r, messageLine("m1", "hi")+"\n"+resultLine(sessionID)))
	if usage.Tokens != 16816 {
		t.Fatalf("want the tokens read anyway, got %d", usage.Tokens)
	}
	if usage.Window != 0 || usage.Known() || usage.Percent() != 0 {
		t.Fatalf("want no percentage for an unknown window, got %d of %d", usage.Tokens, usage.Window)
	}
}

// The tier stands in the log next to the tokens, and it is what decides the
// window. Reading the tokens without it would measure a session on the wide tier
// against the standard bound and call it three times as full as it is.
func TestTurnMeasuresAgainstTheTierTheSessionIsOn(t *testing.T) {
	standard := testRunner(t)
	turnEvents(t, standard,
		`{"type":"session.model_change","data":{"newModel":"gpt-5.6-terra","contextTier":"default"}}`,
		`{"type":"session.shutdown","data":{"currentModel":"gpt-5.6-terra","currentTokens":68000}}`,
	)
	usage := usageOf(runTurnWith(t, standard, messageLine("m1", "hi")+"\n"+resultLine(sessionID)))
	if usage.Window != 272_000 || usage.Percent() != 25 {
		t.Fatalf("want the standard tier's bound, got %d of %d", usage.Tokens, usage.Window)
	}

	wide := testRunner(t)
	turnEvents(t, wide,
		`{"type":"session.model_change","data":{"newModel":"gpt-5.6-terra","contextTier":"long_context"}}`,
		`{"type":"session.shutdown","data":{"currentModel":"gpt-5.6-terra","currentTokens":68000}}`,
	)
	usage = usageOf(runTurnWith(t, wide, messageLine("m1", "hi")+"\n"+resultLine(sessionID)))
	if usage.Window != 922_000 || usage.Percent() != 7 {
		t.Fatalf("want the wide tier's bound, got %d of %d", usage.Tokens, usage.Window)
	}

	// Moving back off the wide tier takes its bound away again.
	back := testRunner(t)
	turnEvents(t, back,
		`{"type":"session.model_change","data":{"newModel":"gpt-5.6-terra","contextTier":"long_context"}}`,
		`{"type":"session.model_change","data":{"newModel":"gpt-5.6-terra","contextTier":null}}`,
		`{"type":"session.shutdown","data":{"currentModel":"gpt-5.6-terra","currentTokens":68000}}`,
	)
	if got := usageOf(runTurnWith(t, back, messageLine("m1", "hi")+"\n"+resultLine(sessionID))).Window; got != 272_000 {
		t.Fatalf("want the wide tier dropped again, got %d", got)
	}
}

// A turn whose run never wrote the record, it was killed, or an older CLI that
// does not write one, reports nothing. The page then keeps the number it had.
func TestTurnWithoutARecordReportsNothing(t *testing.T) {
	r := testRunner(t)
	turnEvents(t, r, `{"type":"assistant.turn_end","data":{"turnId":"0"}}`)
	for _, ev := range runTurnWith(t, r, messageLine("m1", "hi")+"\n"+resultLine(sessionID)) {
		if ev.Kind == assistant.EventUsage {
			t.Fatalf("want no reading without a record, got %+v", ev.Usage)
		}
	}
}

// A session with no log at all must not fail the turn: the answer is on the
// output, the reading is a bonus.
func TestTurnWithoutAnEventLogStillPasses(t *testing.T) {
	events := runTurn(t, messageLine("m1", "hi")+"\n"+resultLine(sessionID))
	if err := errorOf(events); err != nil {
		t.Fatalf("want a clean turn without an event log, got %v", err)
	}
	if usage := usageOf(events); usage.Tokens != 0 {
		t.Fatalf("want no reading, got %+v", usage)
	}
}

// A model the turn names rides behind --model; a turn that names none carries
// no such flag, so copilot's own default stands as it always did.
func TestATurnCarriesTheModelBehindItsFlag(t *testing.T) {
	cmd, err := testRunner(t).Command(assistant.TurnRequest{SessionID: sessionID, Title: "A conversation title", Workdir: t.TempDir(), Prompt: "hello", Model: "gpt-5.4-mini"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	argv := strings.Join(append([]string{cmd.Name}, cmd.Args...), " ")
	if !strings.Contains(argv, " --model gpt-5.4-mini") {
		t.Fatalf("want the model behind its flag, got %q", argv)
	}
	if strings.Contains(argvOf(t, false), "--model") {
		t.Fatalf("want no model flag on a turn that names none, got %q", argvOf(t, false))
	}
	// A check and a reaction come as a fresh session of their own with the
	// model the service resolved, the ring's chat pick where nothing narrower
	// stands, and that request builds the same flag.
	held, err := testRunner(t).Command(assistant.TurnRequest{SessionID: "99999999-2222-4333-8444-555555555555", Title: "cockpit check: readme-task", Workdir: t.TempDir(), Prompt: "check", Model: "fable"})
	if err != nil {
		t.Fatalf("command: %v", err)
	}
	argv = strings.Join(append([]string{held.Name}, held.Args...), " ")
	if !strings.Contains(argv, " --session-id 99999999-2222-4333-8444-555555555555 ") || !strings.HasSuffix(argv, " --model fable") {
		t.Fatalf("want a check's fresh session started on the resolved model, got %q", argv)
	}
}

// copilot's config.json opens with comment lines before the JSON and keeps
// the models the user ran recently under recentModelIds. The select lists
// auto first and then those; a missing or unreadable file is auto alone.
func TestRecentModelsReadTheConfigBehindItsComments(t *testing.T) {
	data := []byte("// User settings belong in settings.json.\n// This file is managed automatically.\n{\n  \"appTipShown\": true,\n  \"recentModelIds\": [\n    \"gemini-3.8-flash\",\n    \"claude-sonnet-5\",\n    \"auto\",\n    \"claude-sonnet-5\",\n    \"not a model\"\n  ],\n  \"trustedFolders\": [\"/root/projects\"]\n}\n")
	if got := recentModels(data); strings.Join(got, ",") != "gemini-3.8-flash,claude-sonnet-5" {
		t.Fatalf("want the recent models behind the comments, once each and without auto, got %v", got)
	}
	if got := recentModels([]byte("// only a comment\nnot json")); got != nil {
		t.Fatalf("want an unreadable file to read as an empty list, got %v", got)
	}

	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if got := (&Coder{config: path}).cliModels(); strings.Join(got, ",") != "auto,gemini-3.8-flash,claude-sonnet-5" {
		t.Fatalf("want auto first and the recent models after it, got %v", got)
	}
	if !strings.Contains(copilotModelsNote, "what you add") {
		t.Fatalf("want the note to say the added names are listed too, got %q", copilotModelsNote)
	}
	if got := (&Coder{config: filepath.Join(t.TempDir(), "none.json")}).cliModels(); strings.Join(got, ",") != "auto" {
		t.Fatalf("want a missing file to leave auto alone, got %v", got)
	}
}

// A model copilot does not have is a named refusal, captured on GitHub Copilot
// CLI 1.0.88 with `copilot -p hi --output-format json --model no-such-model`:
// exit code 1, two ephemeral records on standard output and one line on
// standard error, `Error: Model "no-such-model" from --model flag is not
// available.`, which names the model between its quotes. A crash still stays
// unnamed.
func TestDiagnoseNamesAModelCopilotRefuses(t *testing.T) {
	r := testRunner(t)
	parser := r.Parse(sessionID, make(chan assistant.Event, 1))
	generic := errors.New("The coder stopped before it finished the answer.")
	var refusal *assistant.Refusal
	err := parser.Diagnose(generic, "Error: Model \"no-such-model\" from --model flag is not available.\n")
	if !errors.As(err, &refusal) || refusal.Kind != assistant.RefusalUnknownModel || refusal.Model != "no-such-model" {
		t.Fatalf("want the unknown model refusal naming no-such-model, got %v", err)
	}
	if err := parser.Diagnose(generic, "panic: runtime error: index out of range"); err != nil {
		t.Fatalf("want a crash left unnamed, got %v", err)
	}
	// The name copilot echoed is read under the value rule before it reaches
	// a sentence: one past the bound arrives cut to it.
	long := strings.Repeat("m1", assistant.MaxModelRunes)
	err = parser.Diagnose(generic, "Error: Model \""+long+"\" from --model flag is not available.\n")
	if !errors.As(err, &refusal) || refusal.Model != strings.Repeat("m1", assistant.MaxModelRunes/2) {
		t.Fatalf("want the echoed name cut to %d runes, got %v", assistant.MaxModelRunes, err)
	}
}
