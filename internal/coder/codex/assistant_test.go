package codex

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
)

const cockpitID = "11111111-2222-4333-8444-555555555555"

func assistantRunner(t *testing.T) (*runner, string, string) {
	t.Helper()
	sessions, home, project := testRepository(t)
	r := &runner{sessions: sessions, create: func(string, string, string) (string, error) {
		t.Fatal("unexpected session start")
		return "", nil
	}}
	return r, home, project
}

func cockpitRollout(t *testing.T, home, project string, lines ...string) string {
	t.Helper()
	path := writeRollout(t, home, firstID, map[string]any{"cwd": project, "source": "vscode", "originator": "dev-cockpit", "thread_source": cockpitSource + cockpitID}, time.Minute)
	if len(lines) > 0 {
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.WriteString(strings.Join(lines, "\n") + "\n"); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

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
	if err == nil {
		err = parser.Finish()
	}
	drain()
	if err != nil {
		if named := parser.Diagnose(err, ""); named != nil {
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

func toolsOf(events []assistant.Event) []string {
	var tools []string
	for _, ev := range events {
		if ev.Kind == assistant.EventTool {
			tools = append(tools, ev.Text)
		}
	}
	return tools
}

func failureOf(events []assistant.Event) error {
	for _, ev := range events {
		if ev.Kind == assistant.EventError {
			return ev.Err
		}
	}
	return nil
}

// The first turn makes the session through codex, named after the assistant
// and tied to the cockpit's id, and runs against the id codex minted. The
// prompt and that id stand behind the end of options, the model in the
// equals form, and the turn runs without codex's sandbox with the cockpit's
// hook in front of the tools that ask.
func TestTheFirstTurnStartsItsSessionThroughCodex(t *testing.T) {
	r, _, project := assistantRunner(t)
	var started []string
	r.create = func(workdir, name, id string) (string, error) {
		started = append(started, workdir, name, id)
		return firstID, nil
	}
	cmd, err := r.Command(assistant.TurnRequest{SessionID: cockpitID, Title: "  A conversation  ", Workdir: project, Prompt: "--help me", Model: "gpt-fake-sol"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(started, []string{project, "A conversation", cockpitID}) {
		t.Fatalf("started = %q", started)
	}
	want := []string{"exec", "resume", "--json", "--skip-git-repo-check", "--dangerously-bypass-approvals-and-sandbox", "--dangerously-bypass-hook-trust",
		"-c", toolApprovalHook(project), "--model=gpt-fake-sol", "--", firstID, "--help me"}
	if cmd.Name != "codex" || !slices.Equal(cmd.Args, want) {
		t.Fatalf("argv = %s %q", cmd.Name, cmd.Args)
	}
	for _, flag := range assistantFlags {
		if !slices.Contains(cmd.Args, flag) {
			t.Fatalf("the probe asks for %s, the turn does not carry it", flag)
		}
	}

	r.create = func(string, string, string) (string, error) { return "", errors.New("codex is gone") }
	if _, err := r.Command(assistant.TurnRequest{SessionID: cockpitID, Workdir: project, Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "codex is gone") {
		t.Fatalf("err = %v, want the failed start", err)
	}
	r.create = func(string, string, string) (string, error) { return secondID, errors.New("no name") }
	if cmd, err := r.Command(assistant.TurnRequest{SessionID: cockpitID, Workdir: project, Prompt: "x"}); err != nil || cmd.Args[len(cmd.Args)-2] != secondID {
		t.Fatalf("a session without its name still runs: %v, %q", err, cmd.Args)
	}
	r.create = func(string, string, string) (string, error) { return "--last", nil }
	if _, err := r.Command(assistant.TurnRequest{SessionID: cockpitID, Workdir: project, Prompt: "x"}); err == nil {
		t.Fatal("an id codex never mints must not reach the argv")
	}
}

// Every later turn resumes the session codex holds for the cockpit's id, and
// without a model the flag stays away.
func TestALaterTurnResumesCodexsOwnID(t *testing.T) {
	r, home, project := assistantRunner(t)
	cockpitRollout(t, home, project)
	cmd, err := r.Command(assistant.TurnRequest{SessionID: cockpitID, Resume: true, Workdir: project, Prompt: "again"})
	if err != nil {
		t.Fatal(err)
	}
	if got := cmd.Args[len(cmd.Args)-3:]; !slices.Equal(got, []string{"--", firstID, "again"}) {
		t.Fatalf("argv ends %q", got)
	}
	if slices.ContainsFunc(cmd.Args, func(arg string) bool { return strings.HasPrefix(arg, "--model") }) {
		t.Fatalf("argv = %q, want no model flag", cmd.Args)
	}
}

// The hook override is TOML codex reads: one PreToolUse hook over the tools
// of MCP servers, the cockpit's command behind the turn's own wrapper, quoted
// for a shell whatever the workspace is called, with a timeout beyond the
// wait of the question.
func TestTheToolApprovalHookIsValidConfig(t *testing.T) {
	workdir := filepath.Join(t.TempDir(), `it's "odd" $HOME`)
	key, value, ok := strings.Cut(toolApprovalHook(workdir), "=")
	if !ok || key != "hooks.PreToolUse" {
		t.Fatalf("override = %s", toolApprovalHook(workdir))
	}
	var parsed struct {
		Hooks []struct {
			Matcher string `toml:"matcher"`
			Hooks   []struct {
				Type    string `toml:"type"`
				Command string `toml:"command"`
				Timeout int    `toml:"timeout"`
			} `toml:"hooks"`
		} `toml:"v"`
	}
	if err := toml.Unmarshal([]byte("v = "+value), &parsed); err != nil {
		t.Fatalf("%v in %s", err, value)
	}
	if len(parsed.Hooks) != 1 || parsed.Hooks[0].Matcher != "^mcp__" || len(parsed.Hooks[0].Hooks) != 1 {
		t.Fatalf("hooks = %+v", parsed.Hooks)
	}
	hook := parsed.Hooks[0].Hooks[0]
	wantCommand := clirun.ShellQuote(filepath.Join(workdir, "cockpit")) + " " + clirun.ShellQuote("tool-approval-ask")
	if hook.Type != "command" || hook.Command != wantCommand {
		t.Fatalf("hook = %+v, want %s", hook, wantCommand)
	}
	if wait := time.Duration(hook.Timeout) * time.Second; wait <= assistant.ToolApprovalWait+time.Minute {
		t.Fatalf("timeout %s ends before the command answers", wait)
	}
}

// The session of an assistant is listed under the cockpit's id, exists and
// is deleted under it, and everything that reads its record finds codex's
// file.
func TestACockpitSessionIsAddressedByTheCockpitsID(t *testing.T) {
	r, home, project := assistantRunner(t)
	path := cockpitRollout(t, home, project)
	writeRollout(t, home, secondID, map[string]any{"cwd": project}, time.Hour)
	writeIndex(t, home, `{"id":"`+firstID+`","thread_name":"cockpit check: A job"}`)

	got := r.sessions.List()
	if len(got) != 2 || got[0].SessionID != cockpitID || got[0].Name != "cockpit check: A job" || got[1].SessionID != secondID {
		t.Fatalf("sessions = %+v", got)
	}
	if !r.SessionExists(cockpitID) || !r.SessionExists(secondID) || r.SessionExists(firstID) || r.SessionExists(agentID) {
		t.Fatal("a session exists under the id it is listed by, and under no other")
	}
	if found, err := r.sessions.rollout(cockpitID); err != nil || found != path {
		t.Fatalf("rollout = %s, %v", found, err)
	}
	if resume := (runtime{sessions: r.sessions}).ResumeCommand(cockpitID, project, false); !strings.HasSuffix(resume, " -- "+clirun.ShellQuote(firstID)) {
		t.Fatalf("resume = %s", resume)
	}
	var removed, named []string
	r.sessions.remove = func(id string) error { removed = append(removed, id); return nil }
	r.sessions.name = func(id, name string) error { named = append(named, id); return nil }
	if err := r.sessions.NameSession(cockpitID, "x"); err != nil || !slices.Equal(named, []string{firstID}) {
		t.Fatalf("named = %q, %v", named, err)
	}
	if err := r.DeleteSession(cockpitID); err != nil || !slices.Equal(removed, []string{firstID}) {
		t.Fatalf("removed = %q, %v", removed, err)
	}

	gone := filepath.Join(project, "gone")
	writeRollout(t, home, agentID, map[string]any{"cwd": gone, "thread_source": cockpitSource + "99999999-2222-4333-8444-555555555555"}, time.Second)
	if !r.SessionExists("99999999-2222-4333-8444-555555555555") {
		t.Fatal("a session whose workspace is gone still exists, so it can be deleted")
	}
	writeRollout(t, home, "01a20f5d-2e4c-7561-8473-31a7c9f259fc", map[string]any{"cwd": project, "thread_source": cockpitSource + "../../etc"}, time.Second)
	if len(r.sessions.List()) != 2 {
		t.Fatalf("a thread source that carries no id is no session of anybody: %+v", r.sessions.List())
	}
}

// The session start speaks codex's app server protocol: the thread with its
// directory and the cockpit's id as its source, then its name.
func TestStartingASessionSpeaksTheAppServerProtocol(t *testing.T) {
	answer := func(lines ...string) io.Reader { return strings.NewReader(strings.Join(lines, "\n") + "\n") }
	var sent bytes.Buffer
	id, err := startOver(&sent, answer(
		`{"id":0,"result":{}}`,
		`{"method":"thread/started","params":{}}`,
		`{"id":1,"result":{"thread":{"id":"`+firstID+`","path":"/x"}}}`,
		`{"id":2,"result":{}}`,
	), "/work/space", "A name", cockpitID)
	if err != nil || id != firstID {
		t.Fatalf("id = %q, %v", id, err)
	}
	var requests []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(sent.String()), "\n") {
		var request map[string]any
		if err := json.Unmarshal([]byte(line), &request); err != nil {
			t.Fatalf("request is not one JSON line: %q", line)
		}
		requests = append(requests, request)
	}
	if len(requests) != 4 || requests[2]["method"] != "thread/start" || requests[3]["method"] != "thread/name/set" {
		t.Fatalf("requests = %v", requests)
	}
	start := requests[2]["params"].(map[string]any)
	if start["cwd"] != "/work/space" || start["threadSource"] != "dev-cockpit:"+cockpitID {
		t.Fatalf("start = %v", start)
	}
	if name := requests[3]["params"].(map[string]any); name["threadId"] != firstID || name["name"] != "A name" {
		t.Fatalf("name = %v", name)
	}

	if _, err := startOver(io.Discard, answer(`{"id":0,"result":{}}`, `{"id":1,"result":{"thread":{"id":"--last"}}}`), "/w", "n", cockpitID); err == nil {
		t.Fatal("want an error for an id codex never mints")
	}
	if id, err := startOver(io.Discard, answer(`{"id":0,"result":{}}`, `{"id":1,"result":{"thread":{"id":"`+firstID+`"}}}`, `{"id":2,"error":{"message":"no"}}`), "/w", "n", cockpitID); id != firstID || err == nil {
		t.Fatalf("a refused name keeps the session: %q, %v", id, err)
	}
}

// The stream of a real turn (0.172.0): the warnings of the hook trust flag,
// a message, a command that starts and completes, an MCP call, a patch that
// only completes, the closing message and the turn's end. The context
// reading comes out of the session's record, not out of the run's own sum.
func TestATurnStreamsItsBlocksAndTools(t *testing.T) {
	r, home, project := assistantRunner(t)
	cockpitRollout(t, home, project,
		`{"type":"turn_context","payload":{"model":"gpt-6.2-sol","cwd":"/x"}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":17184,"cached_input_tokens":13952},"model_context_window":475000}}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":null}}`,
		`{"type":"event_msg","payload":{"type":"token_count","info":{"total_token_usage":{"input_tokens":34419},"last_token_usage":{"input_tokens":17235,"cached_input_tokens":17024},"model_context_window":475000}}}`,
	)
	events := runTurn(t, r, `
{"type":"thread.started","thread_id":"`+firstID+`"}
{"type":"item.completed","item":{"id":"item_0","type":"error","message":"`+"`--dangerously-bypass-hook-trust`"+` is enabled. Enabled hooks may run without review for this invocation."}}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_2","type":"agent_message","text":"one"}}
{"type":"item.started","item":{"id":"item_3","type":"command_execution","command":"/bin/bash -lc 'echo hi'","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_3","type":"command_execution","command":"/bin/bash -lc 'echo hi'","aggregated_output":"hi\n","exit_code":0,"status":"completed"}}
{"type":"item.started","item":{"id":"item_4","type":"mcp_tool_call","server":"probe","tool":"lookup","arguments":{"word":"apple"},"result":null,"error":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_4","type":"mcp_tool_call","server":"probe","tool":"lookup","arguments":{"word":"apple"},"result":null,"error":{"message":"MCP tool call requires approval, but approval policy is never"},"status":"failed"}}
{"type":"item.completed","item":{"id":"item_5","type":"file_change","changes":[{"path":"/x/a.txt","kind":"update"}],"status":"completed"}}
{"type":"item.completed","item":{"id":"item_6","type":"reasoning","text":"thinking"}}
{"type":"item.completed","item":{"id":"item_7","type":"agent_message","text":"two"}}
{"type":"turn.completed","usage":{"input_tokens":34419,"cached_input_tokens":30976,"output_tokens":33}}
`)
	if err := failureOf(events); err != nil {
		t.Fatal(err)
	}
	if got := textOf(events); got != "one"+assistant.BlockSeparator+"two" {
		t.Fatalf("text = %q", got)
	}
	if got := toolsOf(events); !slices.Equal(got, []string{"shell", "lookup", "apply_patch"}) {
		t.Fatalf("tools = %q", got)
	}
	last := events[len(events)-1]
	if last.Kind != assistant.EventUsage || *last.Usage != (assistant.ContextUsage{Model: "gpt-6.2-sol", Tokens: 17235, Window: 475000}) {
		t.Fatalf("last event = %+v %+v", last, last.Usage)
	}
}

// Assistant turns survive unknown coder output: a line that is no JSON, a
// record of a kind this version does not know and an item of an unknown type
// are read past, and the turn ends the way codex ended it.
func TestATurnSurvivesUnknownOutput(t *testing.T) {
	r, home, project := assistantRunner(t)
	cockpitRollout(t, home, project)
	events := runTurn(t, r, `
not json at all
{"type":"thread.started","thread_id":"`+firstID+`"}
{"type":"turn.started"}
{"type":"some.future.event","payload":[1,2,3]}
{"type":"item.completed","item":{"id":"item_0","type":"future_item","text":"not an answer"}}
{"type":"item.completed","item":"a string where an object stood"}
{"type":"error","message":"Reconnecting... 2/5 (stream disconnected)"}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"done"}}
{"type":"turn.completed"}
`)
	if err := failureOf(events); err != nil {
		t.Fatal(err)
	}
	if got := textOf(events); got != "done" || len(toolsOf(events)) != 0 {
		t.Fatalf("text = %q, tools = %q", got, toolsOf(events))
	}
}

// How a turn that did not end well is named: by its own record where codex
// wrote one, and as a turn that stopped where it wrote none.
func TestAFailedTurnIsNamed(t *testing.T) {
	r, home, project := assistantRunner(t)
	cockpitRollout(t, home, project)
	head := `{"type":"thread.started","thread_id":"` + firstID + `"}` + "\n" + `{"type":"turn.started"}` + "\n"

	err := failureOf(runTurn(t, r, head+`{"type":"error","message":"The 'no-such-model' model is not supported when using Codex with a ChatGPT account."}
{"type":"turn.failed","error":{"message":"The 'no-such-model' model is not supported when using Codex with a ChatGPT account."}}`))
	var refusal *assistant.Refusal
	if !errors.As(err, &refusal) || refusal.Kind != assistant.RefusalUnknownModel || refusal.Model != "no-such-model" {
		t.Fatalf("err = %v, want the unknown model named", err)
	}

	err = failureOf(runTurn(t, r, head+`{"type":"error","message":"Reconnecting... 5/5 (unexpected status 401 Unauthorized: Missing bearer or basic authentication in header)"}
{"type":"turn.failed","error":{"message":"unexpected status 401 Unauthorized: Missing bearer or basic authentication in header, url: https://api.openai.com/v1/responses"}}`))
	if !errors.Is(err, assistant.ErrNotLoggedIn) {
		t.Fatalf("err = %v, want the missing login", err)
	}

	err = failureOf(runTurn(t, r, head+`{"type":"turn.failed","error":{"message":"You've hit your usage limit. Try again at 9:12 PM."}}`))
	var said *assistant.Said
	if !errors.As(err, &said) || !strings.Contains(said.Line, "usage limit") || !strings.HasPrefix(err.Error(), "The coder could not finish this answer.") {
		t.Fatalf("err = %v, want codex's words quoted", err)
	}

	err = failureOf(runTurn(t, r, head+`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"half"}}`))
	if err == nil || err.Error() != "The coder stopped before it finished the answer." {
		t.Fatalf("err = %v, want the stopped turn", err)
	}

	err = failureOf(runTurn(t, r, `{"type":"thread.started","thread_id":"`+secondID+`"}`))
	if err == nil || !strings.Contains(err.Error(), "different conversation") {
		t.Fatalf("err = %v, want the foreign thread refused", err)
	}
}

// A record without a reading reports nothing, and a window the record does
// not name comes from the table or stays unknown.
func TestTheContextReadingNeedsARecord(t *testing.T) {
	if usage, ok := readContextUsage(strings.NewReader(`{"type":"turn_context","payload":{"model":"gpt-6.2-sol"}}` + "\n")); ok {
		t.Fatalf("usage = %+v, want none", usage)
	}
	usage, ok := readContextUsage(strings.NewReader(`{"type":"event_msg","payload":{"type":"token_count","info":{"last_token_usage":{"input_tokens":9}}}}`))
	if !ok || usage.Tokens != 9 || usage.Window != 0 {
		t.Fatalf("usage = %+v, %v", usage, ok)
	}
}

func TestCodexAnswersTurnsBehindItsProbe(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\necho 'Usage: codex exec resume --json --skip-git-repo-check --dangerously-bypass-approvals-and-sandbox'\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("CODEX_HOME", t.TempDir())
	if runner := coder.AssistantRunnerFor(New(nil)); runner != nil {
		t.Fatal("a codex without the hook trust flag answers no turns")
	}
	script = strings.Replace(script, "sandbox'", "sandbox --dangerously-bypass-hook-trust'", 1)
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	c := New(nil)
	runner := coder.AssistantRunnerFor(c)
	if runner == nil {
		t.Fatal("want the runner once the CLI has every flag")
	}
	if _, ok := runner.(assistant.Compactor); !ok {
		t.Fatal("want the compaction beside the turns")
	}
}
