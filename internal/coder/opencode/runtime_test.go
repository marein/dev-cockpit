package opencode

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
)

// The explicit width switch is unconditional: it stands even when there is no
// inbox and the config write fails, because it is what keeps umlauts on the
// screen and nothing else in the environment has a say in it.
func TestEnvAlwaysSwitchesExplicitWidthOff(t *testing.T) {
	for name, r := range map[string]runtime{
		"a bare runtime": {},
		"no inbox and a refused config write": {
			ensureConfig: func() (string, string, error) { return "", "", os.ErrPermission },
		},
	} {
		if env := r.Env(); env[explicitWidthEnv] != "0" {
			t.Errorf("%s: explicit width is not switched off: %v", name, env)
		}
	}
}

// A task reaches opencode through --prompt, which the TUI's home screen
// submits on mount. Typing it into the pane afterwards is what would lose it.
func TestStartCommandCarriesTheTask(t *testing.T) {
	r := runtime{create: func(workdir, title, cockpitID string) (string, error) {
		t.Fatal("a start with a task must not create a session ahead, --prompt only fires on a fresh one")
		return "", nil
	}}
	command := r.StartCommand(coder.SessionStart{
		SessionID: "sid", Name: "name", Workdir: "/work", Task: "Fix the login redirect",
	})
	if !strings.HasSuffix(command, "--prompt='Fix the login redirect'") {
		t.Errorf("task is not passed to opencode: %s", command)
	}
	if strings.Contains(command, "--session") {
		t.Errorf("a start with a task must not resume anything: %s", command)
	}
}

// A task is text, whatever it starts with. It rides in the equals form of
// --prompt, because yargs reads a separate `--prompt -dfoo` as the flag
// without a value followed by an unknown option, while the equals form is
// unambiguous (verified on opencode 1.18.23, the words arrive verbatim).
func TestStartCommandCarriesADashLeadingTaskAsText(t *testing.T) {
	tasks := map[string]string{
		"a php option somebody pasted": "-dxdebug.idekey=PHPSTORM",
		"a long flag":                  "--help",
		"a bare dash":                  "-",
		"an ordinary task":             "Fix the login redirect",
	}
	for name, task := range tasks {
		command := runtime{}.StartCommand(coder.SessionStart{
			SessionID: "sid", Name: "name", Workdir: "/work", Task: task,
		})
		want := "--prompt=" + clirun.ShellQuote(task)
		if !strings.HasSuffix(command, want) {
			t.Errorf("%s: want %q at the end, got %s", name, want, command)
		}
		if strings.Contains(command, " -- ") {
			t.Errorf("%s: run's separator has no place on the TUI command: %s", name, command)
		}
	}
}

// Without a task nothing would create the session record until the first
// typed message, long after the promote window. So the start creates the
// session ahead through opencode's own API, named like the coder, and the
// TUI resumes it.
func TestStartCommandWithoutATaskCreatesTheSessionAhead(t *testing.T) {
	var gotWorkdir, gotTitle, gotCockpit string
	r := runtime{create: func(workdir, title, cockpitID string) (string, error) {
		gotWorkdir, gotTitle, gotCockpit = workdir, title, cockpitID
		return "ses_fc64235edffebZfFWEw6CdU1Sv", nil
	}}
	command := r.StartCommand(coder.SessionStart{SessionID: "sid", Name: "my coder", Workdir: "/work"})
	if gotWorkdir != "/work" || gotTitle != "my coder" {
		t.Fatalf("the session is created in the project under the coder's name, got %q in %q", gotTitle, gotWorkdir)
	}
	if gotCockpit != "" {
		t.Fatalf("a terminal session needs no cockpit id in its metadata, got %q", gotCockpit)
	}
	if !strings.Contains(command, "--session 'ses_fc64235edffebZfFWEw6CdU1Sv'") {
		t.Fatalf("the TUI has to resume the created session: %s", command)
	}
	if strings.Contains(command, "--prompt") {
		t.Fatalf("without a task there is no prompt: %s", command)
	}
}

// A creation that fails must not fail the start: the session then works, only
// its stored record appears late.
func TestStartCommandFallsBackWhenTheCreationFails(t *testing.T) {
	r := runtime{create: func(workdir, title, cockpitID string) (string, error) {
		return "", errors.New("no server")
	}}
	command := r.StartCommand(coder.SessionStart{SessionID: "sid", Name: "name", Workdir: "/work"})
	if strings.Contains(command, "--session") {
		t.Fatalf("a failed creation must fall back to a plain start: %s", command)
	}
	if !strings.Contains(command, "exec opencode") {
		t.Fatalf("the plain start is still a start: %s", command)
	}
}

func TestStartCommandCarriesApprovalAndAgent(t *testing.T) {
	command := runtime{}.StartCommand(coder.SessionStart{
		SessionID: "sid", Name: "name", Workdir: "/work", AgentID: "helper",
		AutomaticApproval: true, Task: "go",
	})
	for _, want := range []string{" --auto", " --agent 'helper'"} {
		if !strings.Contains(command, want) {
			t.Errorf("start command misses %q: %s", want, command)
		}
	}
	plain := runtime{}.StartCommand(coder.SessionStart{SessionID: "sid", Name: "name", Workdir: "/work", Task: "go"})
	if strings.Contains(plain, "--auto") || strings.Contains(plain, "--agent") {
		t.Errorf("a plain session carries neither flag: %s", plain)
	}
}

func TestResumeCommandResumesTheStoredSession(t *testing.T) {
	r := runtime{sessions: fixtureRepository(t, sessionFixtures)}
	command := r.ResumeCommand("ses_fc6480f13ffeS2hWoSoid3ir6k", "/work", true)
	for _, want := range []string{"cd '/work'", "exec opencode", " --auto", "--session 'ses_fc6480f13ffeS2hWoSoid3ir6k'"} {
		if !strings.Contains(command, want) {
			t.Errorf("resume command misses %q: %s", want, command)
		}
	}
}

// A conversation handed over to a terminal is stored under the cockpit's id;
// the resume has to reach opencode under opencode's own.
func TestResumeCommandResolvesTheCockpitId(t *testing.T) {
	r := runtime{sessions: fixtureRepository(t, sessionFixtures)}
	command := r.ResumeCommand("11111111-2222-4333-8444-555555555555", "/work", true)
	if !strings.Contains(command, "--session 'ses_fc6374637ffeU6S5j7RziNAGjI'") {
		t.Fatalf("the resume has to carry opencode's own id: %s", command)
	}
}

// The CLI mints its own session ids and takes no name, so the runtime says
// both: the manager then matches the fresh session on the working directory
// instead of waiting for a name match that can never happen.
func TestTheRuntimeSaysItsSessionsAreUnnamed(t *testing.T) {
	var r any = runtime{}
	naming, ok := r.(coder.SessionNaming)
	if !ok {
		t.Fatal("the opencode runtime has to answer coder.SessionNaming")
	}
	if naming.NamesSessions() {
		t.Fatal("opencode has no name flag, the runtime must not claim one")
	}
	if (runtime{}).UsesProvidedSessionID() {
		t.Fatal("opencode's --session only resumes ids that exist")
	}
}

// opencode records every session in its database, so it answers activity from
// that record: the capability is what keeps the manager off the screen, whose
// input line carries the TUI's own draft. The reading itself is tested in
// activity_test.go.
func TestOpenCodeReportsSessionActivity(t *testing.T) {
	var c any = New("")
	if _, ok := c.(coder.ActivityReporter); !ok {
		t.Fatal("opencode keeps a record, it has to answer from it")
	}
}
