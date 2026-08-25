package opencode

import (
	"os"
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
	for _, want := range []string{notifyEnv, "session.idle", "permission.asked", `"Stop"`, `"Notification"`, ".tmp"} {
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
