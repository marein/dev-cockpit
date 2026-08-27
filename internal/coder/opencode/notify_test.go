package opencode

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
)

// The inbox travels into the session through the tmux environment, which is
// the only place the injected plugin reads it from. Without an inbox there
// is nothing to carry.
func TestEnvCarriesTheNotifyInbox(t *testing.T) {
	env := runtime{notifyInbox: "/state/notification-inbox/opencode"}.Env()
	if env[notifyEnv] != "/state/notification-inbox/opencode" {
		t.Errorf("session environment misses the inbox: %v", env)
	}
	if plain := (runtime{}).Env(); plain != nil {
		t.Errorf("without an inbox the environment stays empty: %v", plain)
	}
}

// Every session start puts the plugin in place, start and resume alike, the
// way claude injects its hooks per session. A start without an inbox
// injects nothing.
func TestStartAndResumeInjectTheNotifyPlugin(t *testing.T) {
	calls := 0
	r := runtime{
		notifyInbox:  "/inbox",
		ensurePlugin: func() error { calls++; return nil },
		create:       func(workdir, title, cockpitID string) (string, error) { return "", os.ErrNotExist },
	}
	r.StartCommand(coder.SessionStart{SessionID: "sid", Name: "name", Workdir: "/work", Task: "go"})
	if calls != 1 {
		t.Fatalf("start has to inject the plugin once, got %d calls", calls)
	}
	r.ResumeCommand("ses_fc6480f13ffeS2hWoSoid3ir6k", "/work", false)
	if calls != 2 {
		t.Fatalf("resume has to inject the plugin too, got %d calls", calls)
	}
	silent := runtime{ensurePlugin: func() error { t.Fatal("no inbox, no injection"); return nil }}
	silent.StartCommand(coder.SessionStart{SessionID: "sid", Name: "name", Workdir: "/work", Task: "go"})
}

// A refused plugin write costs the notifications alone, never the session.
func TestAFailedInjectionStillStartsTheSession(t *testing.T) {
	r := runtime{notifyInbox: "/inbox", ensurePlugin: func() error { return os.ErrPermission }}
	command := r.StartCommand(coder.SessionStart{SessionID: "sid", Name: "name", Workdir: "/work", Task: "go"})
	if !strings.Contains(command, "exec opencode") {
		t.Fatalf("the start has to survive a refused plugin write: %s", command)
	}
}

func TestEnsureNotifyPluginWritesTheFile(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := ensureNotifyPlugin(); err != nil {
		t.Fatalf("fresh write failed: %v", err)
	}
	content, err := os.ReadFile(notifyPluginPath())
	if err != nil {
		t.Fatalf("plugin file missing: %v", err)
	}
	if string(content) != notifyPlugin {
		t.Fatal("plugin file differs from the source")
	}
	for _, want := range []string{notifyEnv, "session.idle", "permission.asked", "permission.replied", `"Stop"`, `"Notification"`, ".tmp"} {
		if !strings.Contains(string(content), want) {
			t.Errorf("plugin misses %q", want)
		}
	}
}

// Whatever holds the path is rewritten: the file carries the cockpit's own
// name, so it is the cockpit's, and rewriting unconditionally is what keeps
// it current across releases, an older build's copy and a hand edit alike.
func TestEnsureNotifyPluginRewritesWhateverHoldsThePath(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	path := notifyPluginPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("export const Mine = async () => ({})\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := ensureNotifyPlugin(); err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	content, _ := os.ReadFile(path)
	if string(content) != notifyPlugin {
		t.Fatal("the file has to be brought current")
	}
}

// An unchanged file writes nothing, so a session start does not churn the
// config directory.
func TestEnsureNotifyPluginLeavesAnUnchangedFileAlone(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := ensureNotifyPlugin(); err != nil {
		t.Fatalf("fresh write failed: %v", err)
	}
	marker := time.Now().Add(-time.Hour)
	if err := os.Chtimes(notifyPluginPath(), marker, marker); err != nil {
		t.Fatal(err)
	}
	if err := ensureNotifyPlugin(); err != nil {
		t.Fatalf("second ensure failed: %v", err)
	}
	info, err := os.Stat(notifyPluginPath())
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(marker) {
		t.Error("an unchanged file was written again")
	}
}

func notifyPluginPath() string {
	return filepath.Join(configDir(), "plugin", notifyPluginFile)
}

// The behavior tests run the real plugin under node in a container, the
// image the LSP build files already use, and skip where docker is missing,
// like the voice tests. The harness stands in for opencode's plugin host:
// it loads the module, hands it a client whose session lookup answers a
// plain parent session, and feeds events into the returned hook. The inbox
// is a bind mount, so the Go side reads what the plugin wrote.
func runNotifyHarness(t *testing.T, harness string) string {
	t.Helper()
	dockerPath, err := exec.LookPath("docker")
	if err != nil {
		t.Skip("docker is not available on this host")
	}
	if err := exec.Command(dockerPath, "info").Run(); err != nil {
		t.Skip("the docker daemon does not answer")
	}
	work := t.TempDir()
	if err := os.WriteFile(filepath.Join(work, "plugin.mjs"), []byte(notifyPlugin), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "harness.mjs"), []byte(harness), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(work, "inbox"), 0o755); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(dockerPath, "run", "--rm", "--network", "none",
		"-e", notifyEnv+"=/work/inbox",
		"-v", work+":/work",
		"node:lts-alpine", "node", "/work/harness.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("harness failed: %v\n%s", err, out)
	}
	return filepath.Join(work, "inbox")
}

const notifyHarnessHead = `import { DevCockpitNotify } from "/work/plugin.mjs"
import { readdirSync } from "node:fs"
const client = { session: { get: async () => ({ data: { metadata: {} } }) } }
const hooks = await DevCockpitNotify({ client })
const send = (type, properties) => hooks.event({ event: { type, properties } })
const wait = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
`

// An ask the TUI answers itself, the way every --auto session does, is
// nobody's news: the reply lands inside the grace and no file is written.
// The v1 and the v2 names travel through the same pending map.
func TestAnAnsweredAskInsideTheGraceWritesNothing(t *testing.T) {
	inbox := runNotifyHarness(t, notifyHarnessHead+`await send("permission.asked", { id: "per_1", sessionID: "ses_1" })
await send("permission.replied", { requestID: "per_1", sessionID: "ses_1", reply: "once" })
await send("permission.v2.asked", { id: "per_2", sessionID: "ses_1" })
await send("permission.v2.replied", { requestID: "per_2", sessionID: "ses_1", reply: "once" })
await wait(2600)
`)
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("an answered ask must write nothing, the inbox holds %v", entries)
	}
}

// An ask nobody answers becomes exactly one file, and only after the grace:
// the harness proves the inbox still empty right after the ask.
func TestAnUnansweredAskWritesOneFileAfterTheGrace(t *testing.T) {
	inbox := runNotifyHarness(t, notifyHarnessHead+`await send("permission.asked", { id: "per_1", sessionID: "ses_1" })
await wait(300)
if (readdirSync("/work/inbox").length !== 0) {
  console.error("a file landed inside the grace")
  process.exit(1)
}
await wait(2600)
`)
	entries, err := os.ReadDir(inbox)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("an unanswered ask must write exactly one file, the inbox holds %d", len(entries))
	}
	name := entries[0].Name()
	if !strings.HasSuffix(name, ".json") {
		t.Fatalf("the file has to be renamed to .json, got %q", name)
	}
	content, err := os.ReadFile(filepath.Join(inbox, name))
	if err != nil {
		t.Fatal(err)
	}
	var payload struct {
		SessionID string `json:"session_id"`
		HookEvent string `json:"hook_event_name"`
	}
	if err := json.Unmarshal(content, &payload); err != nil {
		t.Fatalf("payload is no JSON: %v\n%s", err, content)
	}
	if payload.SessionID != "ses_1" || payload.HookEvent != "Notification" {
		t.Fatalf("payload misses the claude hook shape: %s", content)
	}
}
