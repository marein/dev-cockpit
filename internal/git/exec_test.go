package git

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestExecAnswersOutputAndExitCodeOneToOne(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	writeAt(t, dir, "a.txt", "a\n")
	runGit(t, dir, "add", "-A")
	runGit(t, dir, "commit", "-qm", "init")

	res, err := New(dir).Exec(context.Background(), []string{"rev-parse", "--is-inside-work-tree"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode != 0 || strings.TrimSpace(string(res.Stdout)) != "true" {
		t.Fatalf("exit %d, stdout %q", res.ExitCode, res.Stdout)
	}

	res, err = New(dir).Exec(context.Background(), []string{"rev-parse", "--verify", "-q", "zz-nothing"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatal("a revision that does not exist must answer a non-zero exit code")
	}
}

// The proxy promises the command line as it was typed: run() injects
// core.quotepath in front of every reading call, and an injected -c is
// visible to `config --get`, so an unset key answering empty is the proof
// that Exec adds nothing.
func TestExecInjectsNoConfiguration(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)

	res, err := New(dir).Exec(context.Background(), []string{"config", "--get", "core.quotepath"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.ExitCode == 0 || strings.TrimSpace(string(res.Stdout)) != "" {
		t.Fatalf("core.quotepath was injected: exit %d, stdout %q", res.ExitCode, res.Stdout)
	}
}

func TestExecPushMovesTheRemote(t *testing.T) {
	work, remote := remotePair(t)
	writeAt(t, work, "a.txt", "a2\n")
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-qm", "second")

	res, err := New(work).Exec(context.Background(), []string{"push"})
	if err != nil {
		t.Fatalf("exec push: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("push exited %d: %s", res.ExitCode, res.Stderr)
	}
	if headOf(t, remote) != headOf(t, work) {
		t.Fatal("the remote did not follow the push")
	}
}

func TestExecKeepsTheNoAnswerCasesAsErrors(t *testing.T) {
	dir := t.TempDir()
	commitRepo(t, dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(dir).Exec(ctx, []string{"status"}); !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("a dropped call must carry ErrNoAnswer, got %v", err)
	}
}

// The action name is the one line the dialog shows as this server's own
// truth, above the prompt ssh wrote. Reading past an option to find a word
// further back cannot tell an option from an option's value, so a caller could
// name the action after one of their own.
func TestSubcommandReadsTheFirstArgumentAndNothingBehindIt(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"push", "--force-with-lease"}, "push"},
		{[]string{"pull", "--ff-only"}, "pull"},
		{[]string{"ls-remote"}, "ls-remote"},
		{nil, "git"},
		{[]string{"-c", "core.sshCommand=/tmp/evil", "push"}, "git"},
		{[]string{"--exec-path=/tmp/evil", "push"}, "git"},
		{[]string{"Push"}, "git"},
	}
	for _, c := range cases {
		if got := Subcommand(c.args); got != c.want {
			t.Fatalf("Subcommand(%v) = %q, want %q", c.args, got, c.want)
		}
	}
}

// A proxied line may describe a git operation and may never name what this
// server runs. Everything dangerous is an option of git itself and therefore
// only valid in front of the subcommand, so demanding the subcommand first is
// what makes the rule complete instead of a list of option names.
func TestCheckProxyArgsKeepsTheShapeOfAProxiedLine(t *testing.T) {
	fine := [][]string{
		{"push"},
		{"push", "--force-with-lease", "origin", "HEAD"},
		{"fetch", "--all", "--prune"},
		{"ls-remote", "--heads", "origin"},
		{"status"},
	}
	for _, args := range fine {
		if err := CheckProxyArgs(args); err != nil {
			t.Fatalf("CheckProxyArgs(%v) refused an ordinary call: %v", args, err)
		}
	}
	refused := [][]string{
		nil,
		{"-c", "core.sshCommand=/tmp/evil", "push"},
		{"-c", "credential.helper=!/tmp/evil", "fetch"},
		{"-C", "/elsewhere", "push"},
		{"--git-dir=/elsewhere/.git", "push"},
		{"--exec-path=/tmp/evil", "push"},
		{"--version"},
		{"fetch", "--upload-pack=/tmp/evil", "file:///srv/repo"},
		{"push", "--receive-pack=/tmp/evil"},
	}
	for _, args := range refused {
		if err := CheckProxyArgs(args); err == nil {
			t.Fatalf("CheckProxyArgs(%v) let a line through that names a program or another repository", args)
		}
	}
}
