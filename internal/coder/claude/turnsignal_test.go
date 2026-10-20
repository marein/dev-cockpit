package claude

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

// What each hook says about the main thread's turn, by claude's own names.
func TestEveryHookSaysWhatItMeansForTheTurn(t *testing.T) {
	p := &Coder{}
	cases := []struct {
		ev   coder.HookEvent
		want coder.TurnSignal
	}{
		{coder.HookEvent{Name: "UserPromptSubmit"}, coder.TurnSignalOpen},
		{coder.HookEvent{Name: "PreToolUse", ToolName: "Bash"}, coder.TurnSignalTool},
		{coder.HookEvent{Name: "PostToolUse"}, coder.TurnSignalOpen},
		{coder.HookEvent{Name: "PostToolUseFailure"}, coder.TurnSignalOpen},
		{coder.HookEvent{Name: "PostToolBatch"}, coder.TurnSignalOpen},
		{coder.HookEvent{Name: "MessageDisplay"}, coder.TurnSignalAlive},
		{coder.HookEvent{Name: "Stop"}, coder.TurnSignalOver},
		{coder.HookEvent{Name: "StopFailure"}, coder.TurnSignalOver},
		{coder.HookEvent{Name: "SessionEnd"}, coder.TurnSignalOver},
		{coder.HookEvent{Name: "SessionStart", Source: "startup"}, coder.TurnSignalOver},
		{coder.HookEvent{Name: "SessionStart", Source: "resume"}, coder.TurnSignalOver},
		{coder.HookEvent{Name: "SessionStart", Source: "clear"}, coder.TurnSignalOver},
		{coder.HookEvent{Name: "SessionStart", Source: "compact"}, coder.TurnSignalNone},
		{coder.HookEvent{Name: "PreCompact", Trigger: "manual"}, coder.TurnSignalOpen},
		{coder.HookEvent{Name: "PostCompact", Trigger: "manual"}, coder.TurnSignalOver},
		{coder.HookEvent{Name: "PreCompact", Trigger: "auto"}, coder.TurnSignalNone},
		{coder.HookEvent{Name: "PostCompact", Trigger: "auto"}, coder.TurnSignalNone},
		{coder.HookEvent{Name: "PermissionRequest", ToolName: "Bash"}, coder.TurnSignalWaiting},
		{coder.HookEvent{Name: "PermissionRequest", ToolName: "AskUserQuestion"}, coder.TurnSignalQuestion},
		{coder.HookEvent{Name: "PreToolUse", ToolName: "AskUserQuestion"}, coder.TurnSignalQuestion},
		{coder.HookEvent{Name: "PreToolUse", ToolName: "ExitPlanMode"}, coder.TurnSignalQuestion},
		{coder.HookEvent{Name: "PermissionRequest", ToolName: "ExitPlanMode"}, coder.TurnSignalQuestion},
		{coder.HookEvent{Name: "Notification", NotificationType: "permission_prompt"}, coder.TurnSignalWaiting},
		{coder.HookEvent{Name: "Notification", NotificationType: "elicitation_dialog"}, coder.TurnSignalQuestion},
		{coder.HookEvent{Name: "Notification", NotificationType: "idle_prompt"}, coder.TurnSignalNone},
		{coder.HookEvent{Name: "Notification", NotificationType: "auth_success"}, coder.TurnSignalNone},
		{coder.HookEvent{Name: "PreModelSwitch"}, coder.TurnSignalNone},
		{coder.HookEvent{Name: "TurnStart"}, coder.TurnSignalNone},
		{coder.HookEvent{Name: "Stop", AgentID: "a1"}, coder.TurnSignalNone},
		{coder.HookEvent{Name: "PreToolUse", AgentID: "a1"}, coder.TurnSignalNone},
	}
	for _, c := range cases {
		if got := p.TurnSignal(c.ev); got != c.want {
			t.Errorf("%+v: got %v, want %v", c.ev, got, c.want)
		}
	}
}

// Every hook of the turn's account is registered for a session, and each one
// runs the same command, tagged with the session's terminal.
func TestASessionRegistersEveryTurnHookForItsTerminal(t *testing.T) {
	r := runtime{notifyInbox: "/tmp/inbox"}
	var values struct {
		Hooks map[string][]struct {
			Hooks []struct{ Command string } `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal([]byte(r.sessionSettings("sid-1")), &values); err != nil {
		t.Fatalf("settings are not valid JSON: %v", err)
	}
	for _, name := range turnHooks {
		hook := values.Hooks[name]
		if len(hook) != 1 || len(hook[0].Hooks) != 1 || !strings.Contains(hook[0].Hooks[0].Command, `"terminal":"sid-1"`) {
			t.Errorf("%s is not wired to the terminal's command: %+v", name, hook)
		}
	}
	if len(values.Hooks) != len(turnHooks) {
		t.Errorf("want exactly the turn hooks, got %d", len(values.Hooks))
	}
}

// The hook command drops the hook's stdin wrapped with its terminal, and it
// never fails: an inbox that cannot be written costs the signal, never a line
// on the coder's screen, and no half written file ever becomes a .json.
func TestTheHookCommandNeverFails(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	inbox := filepath.Join(t.TempDir(), "inbox")
	run := func(dir string) (string, error) {
		cmd := exec.Command("sh", "-c", hookCommand(dir, "sid-1"))
		cmd.Stdin = strings.NewReader(`{"session_id":"claude-2","hook_event_name":"Stop"}`)
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	if out, err := run(inbox); err != nil || out != "" {
		t.Fatalf("the hook failed: %v %q", err, out)
	}
	files, _ := filepath.Glob(filepath.Join(inbox, "*.json"))
	if len(files) != 1 {
		t.Fatalf("want one event file, got %v", files)
	}
	data, _ := os.ReadFile(files[0])
	var wrapped struct {
		Terminal string `json:"terminal"`
		Event    struct {
			SessionID string `json:"session_id"`
		} `json:"event"`
	}
	if err := json.Unmarshal(data, &wrapped); err != nil || wrapped.Terminal != "sid-1" || wrapped.Event.SessionID != "claude-2" {
		t.Fatalf("the event is not wrapped with its terminal: %s (%v)", data, err)
	}

	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := run(filepath.Join(blocked, "inbox")); err != nil || out != "" {
		t.Fatalf("an inbox that cannot be made must not fail the hook: %v %q", err, out)
	}
	readOnly := filepath.Join(t.TempDir(), "ro")
	if err := os.MkdirAll(readOnly, 0o500); err != nil {
		t.Fatal(err)
	}
	if out, err := run(readOnly); err != nil || out != "" {
		t.Fatalf("an inbox that cannot be written must not fail the hook: %v %q", err, out)
	}
	if left, _ := os.ReadDir(readOnly); len(left) != 0 && os.Geteuid() != 0 {
		t.Fatalf("a failed write left %v behind", left)
	}
}

// For the working mark the record speaks only where no hook does: a person's
// prompt, which starts a turn queued while claude worked, and the abort
// marker, the only trace of an abort. Everything else is aside: what claude
// writes on its own, a background task's notification and a subagent's
// hand-back, the summary a compaction leaves, a slash command, the `!`
// escape, a note, and claude's own answers and tool calls, which its hooks
// report.
func TestTheRecordSpeaksOnlyForAPromptAndAnAbort(t *testing.T) {
	const at = `"timestamp":"2026-10-20T10:00:09Z"`
	user := func(extra, content string) string {
		return `{"type":"user",` + extra + at + `,"sessionId":"s1","message":{"role":"user","content":` + content + `}}`
	}
	cases := []struct {
		what  string
		last  string
		aside bool
	}{
		{"a typed prompt", user(`"origin":{"kind":"human"},"promptSource":"typed",`, `"and now the tests"`), false},
		{"a queued slash command, recorded bare", user(``, `"/compact"`), true},
		{"the abort marker", user(``, `[{"type":"text","text":"[Request interrupted by user]"}]`), false},
		{"a queued prompt", user(`"origin":{"kind":"human"},"promptSource":"queued",`, `"and then the docs"`), false},
		{"a task notification", user(`"origin":{"kind":"task-notification"},`, `"<task-notification><status>completed</status></task-notification>"`), true},
		{"a hand-back", user(`"isMeta":true,"origin":{"kind":"peer"},`, `"[Subagent hand-back] done"`), true},
		{"the compaction summary", user(`"isCompactSummary":true,"isVisibleInTranscriptOnly":true,`, `"This session is being continued"`), true},
		{"a command's output", user(``, `"<local-command-stdout>Compacted</local-command-stdout>"`), true},
		{"a command line", user(`"origin":{"kind":"human"},`, `"<command-name>/model</command-name>"`), true},
		{"the shell escape's line", user(``, `"<bash-input>sleep 5</bash-input>"`), true},
		{"the shell escape's echo", user(``, `"<bash-stdout></bash-stdout><bash-stderr></bash-stderr>"`), true},
		{"a session note", user(`"isMeta":true,`, `"<system-reminder>The user named this session</system-reminder>"`), true},
		{"a skill's expansion", user(`"isMeta":true,`, `"Base directory for this skill: /x"`), true},
		{"a tool's result", toolResultLn, true},
		{"claude's answer", answerLine, true},
		{"claude's tool call", toolUseLine, true},
	}
	for _, c := range cases {
		activity, err := readActivity(strings.NewReader(userLine+"\n"+c.last+"\n"), 1, 200)
		if err != nil {
			t.Fatalf("%s: %v", c.what, err)
		}
		if activity.Aside != c.aside {
			t.Errorf("%s: Aside = %v, want %v", c.what, activity.Aside, c.aside)
		}
	}
}
