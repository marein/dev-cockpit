package cli

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// answerGit replies the way handleGitProxy does: both streams base64, the
// exit code as git decided it.
func answerGit(w http.ResponseWriter, stdout, stderr string, code int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"exitCode": code,
		"stdout":   base64.StdEncoding.EncodeToString([]byte(stdout)),
		"stderr":   base64.StdEncoding.EncodeToString([]byte(stderr)),
	})
}

// The whole promise of the proxy: the arguments travel unchanged, and output
// and exit code come back as git's own. What the command sends about the
// project is its raw working directory and nothing else: which project holds
// it is the server's answer, so this command carries no projects root of its
// own to disagree with the server's.
func TestGitProxyPassesTheArgumentsAndAnswersLikeGit(t *testing.T) {
	var gotPath, gotCWD string
	var gotArgs []string
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var payload struct {
			CWD  string   `json:"cwd"`
			Args []string `json:"args"`
		}
		_ = json.Unmarshal(body, &payload)
		gotPath, gotCWD, gotArgs = r.URL.Path, payload.CWD, payload.Args
		answerGit(w, "Everything up-to-date\n", "", 0)
	})

	here := t.TempDir()
	t.Chdir(here)

	var out, errOut strings.Builder
	code, err := runGitProxy(&out, &errOut, dir, []string{"push", "--force-with-lease", "origin", "HEAD"})
	if err != nil {
		t.Fatalf("git proxy: %v", err)
	}
	if code != 0 {
		t.Fatalf("want exit 0, got %d", code)
	}
	if gotPath != "/git" {
		t.Fatalf("want the one git route, got %q", gotPath)
	}
	// The directory travels as this process sees it, unresolved and
	// uninterpreted; t.Chdir's own path may be a symlink, so both spellings
	// count as the same answer.
	resolved, _ := filepath.EvalSymlinks(here)
	if gotCWD != here && gotCWD != resolved {
		t.Fatalf("the working directory did not travel: got %q, want %q", gotCWD, here)
	}
	want := []string{"push", "--force-with-lease", "origin", "HEAD"}
	if strings.Join(gotArgs, " ") != strings.Join(want, " ") {
		t.Fatalf("the arguments were not passed unchanged: %v", gotArgs)
	}
	if out.String() != "Everything up-to-date\n" {
		t.Fatalf("stdout %q", out.String())
	}
}

// A git that refused something is not a failure of this command: the caller
// gets git's own stderr and git's own exit code, no wrapper sentence.
func TestGitProxyHandsBackTheFailingExitCode(t *testing.T) {
	dir := cockpit(t, func(w http.ResponseWriter, r *http.Request) {
		answerGit(w, "", "fatal: could not read from remote repository\n", 128)
	})

	var out, errOut strings.Builder
	code, err := runGitProxy(&out, &errOut, dir, []string{"push"})
	if err != nil {
		t.Fatalf("a refusing git is an answer, not an error: %v", err)
	}
	if code != 128 {
		t.Fatalf("want git's own exit code, got %d", code)
	}
	if !strings.Contains(errOut.String(), "could not read from remote") {
		t.Fatalf("git's words did not reach stderr: %q", errOut.String())
	}
}

// A cancelled or timed out question fails the operation honestly: the
// cockpit's sentence reaches the caller as an error, so nothing hangs and
// nothing pretends to have run.
func TestGitProxyReportsARefusedQuestion(t *testing.T) {
	dir := refusing(t, "git push: no answer within 2m0s — the question was cancelled.")

	var out, errOut strings.Builder
	if _, err := runGitProxy(&out, &errOut, dir, []string{"push"}); err == nil {
		t.Fatal("a refused question has to fail the command")
	} else if !strings.Contains(err.Error(), "the question was cancelled") {
		t.Fatalf("the failure has to carry the cockpit's own words, got %v", err)
	}
}

func TestGitProxyNeedsARunningCockpit(t *testing.T) {
	t.Setenv("DEV_COCKPIT_DIAL_BUDGET", "0")
	var out, errOut strings.Builder
	if _, err := runGitProxy(&out, &errOut, t.TempDir(), []string{"push"}); err == nil {
		t.Fatal("without a cockpit the command has to say so")
	}
}

// An exit code no process can carry (a killed git answers -1) must not wrap
// around into a success.
func TestClampExitKeepsAKilledGitAFailure(t *testing.T) {
	if got := clampExit(-1); got != 1 {
		t.Fatalf("clampExit(-1) = %d, want 1", got)
	}
	if got := clampExit(128); got != 128 {
		t.Fatalf("clampExit(128) = %d, want 128", got)
	}
}

// The command is a proxy, so only its own flag is read off the front and
// everything else reaches the server untouched. Cobra's own parsing is off for
// exactly this: it would end the command with "unknown shorthand flag" on
// `-c`, which is the case that matters, because that is what the server
// refuses with a sentence saying why.
func TestTakeStateDirReadsOnlyItsOwnFlag(t *testing.T) {
	cases := []struct {
		args []string
		dir  string
		rest []string
	}{
		{[]string{"push"}, "fallback", []string{"push"}},
		{[]string{"--state-dir", "/s", "push"}, "/s", []string{"push"}},
		{[]string{"--state-dir=/s", "push", "--force"}, "/s", []string{"push", "--force"}},
		// git's own options are not this command's to judge: they travel, and
		// the server is what refuses them in words that explain.
		{[]string{"-c", "core.sshCommand=/tmp/x", "push"}, "fallback", []string{"-c", "core.sshCommand=/tmp/x", "push"}},
		{[]string{"-C", "/elsewhere", "status"}, "fallback", []string{"-C", "/elsewhere", "status"}},
		// Behind a subcommand the flag is git's too, not ours.
		{[]string{"push", "--state-dir", "/s"}, "fallback", []string{"push", "--state-dir", "/s"}},
	}
	for _, c := range cases {
		dir, rest, err := takeStateDir("fallback", c.args)
		if err != nil {
			t.Fatalf("takeStateDir(%v): %v", c.args, err)
		}
		if dir != c.dir || strings.Join(rest, " ") != strings.Join(c.rest, " ") {
			t.Fatalf("takeStateDir(%v) = %q, %v; want %q, %v", c.args, dir, rest, c.dir, c.rest)
		}
	}
	if _, _, err := takeStateDir("fallback", []string{"--state-dir"}); err == nil {
		t.Fatal("a flag without its value has to be refused")
	}
}
