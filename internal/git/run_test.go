package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeGit puts an executable named git first on the PATH for this test, so
// run() starts it instead of the real binary.
func fakeGit(t *testing.T, script string) {
	t.Helper()
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("sh is not installed")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "git"), []byte("#!/bin/sh\n"+script), 0o755); err != nil {
		t.Fatalf("write fake git: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// processWithArg reports whether any process on the machine carries the
// marker in its command line, which is how the tests see an orphan.
func processWithArg(marker string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		if bytes.Contains(cmdline, []byte(marker)) {
			return true
		}
	}
	return false
}

// The bug this pins down: stdout and stderr are buffers, os/exec builds pipes
// for them, and Run waits until every writer has closed its end. The timeout
// kill used to hit the git process alone, so a helper it had started — ssh
// waiting for a passphrase, a credential helper, pinentry — kept the
// inherited pipes open and Run blocked far past the timeout, which is a push
// button in the editor that never releases.
func TestRunReturnsByTheTimeoutWhenAChildKeepsThePipes(t *testing.T) {
	marker := fmt.Sprintf("dc-git-pipe-holder-%d", os.Getpid())
	fakeGit(t, fmt.Sprintf("sh -c 'sleep 30' %s &\nsleep 30\n", marker))
	repo := &Repo{dir: t.TempDir(), timeout: time.Second}

	start := time.Now()
	_, err := repo.run(context.Background(), []string{"status"}, nil)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a call that was killed must report an error")
	}
	if !strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("the error must name the timeout, not the kill: %v", err)
	}
	if limit := repo.timeout + waitDelay + 3*time.Second; elapsed > limit {
		t.Fatalf("run returned after %v, the timeout plus the grace is %v", elapsed, limit)
	}
	if runtime.GOOS == "linux" {
		deadline := time.Now().Add(3 * time.Second)
		for processWithArg(marker) {
			if time.Now().After(deadline) {
				t.Fatal("the pipe holding child survived the kill as an orphan")
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
}

// A call whose process exited cleanly is not turned into a failure by a
// survivor that still holds the pipes: the grace closes them and the output
// that made it through stays the answer.
func TestRunKeepsACleanExitDespiteAPipeHolder(t *testing.T) {
	marker := fmt.Sprintf("dc-git-clean-holder-%d", os.Getpid())
	fakeGit(t, fmt.Sprintf("printf 'answer'\nsh -c 'sleep 30' %s &\nexit 0\n", marker))
	repo := &Repo{dir: t.TempDir(), timeout: 30 * time.Second}

	start := time.Now()
	out, err := repo.run(context.Background(), []string{"status"}, nil)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("a clean exit must stay one: %v", err)
	}
	if string(out) != "answer" {
		t.Fatalf("output: %q", out)
	}
	if limit := waitDelay + 3*time.Second; elapsed > limit {
		t.Fatalf("run returned after %v, the grace is %v", elapsed, limit)
	}
}

// Every prompt has to fail in seconds, not at the timeout: git's own through
// GIT_TERMINAL_PROMPT=0, ssh's passphrase question through the askpass forced
// to /bin/false. The host's choice of ssh is deliberately not touched, so the
// call carries no GIT_SSH_COMMAND of ours.
func TestRunForcesEveryPromptToFailFast(t *testing.T) {
	fakeGit(t, `printf '%s %s %s %s' "$GIT_TERMINAL_PROMPT" "$SSH_ASKPASS" "$SSH_ASKPASS_REQUIRE" "${GIT_SSH_COMMAND-unset}"`+"\n")
	t.Setenv("SSH_ASKPASS", "/usr/bin/some-desktop-askpass")
	t.Setenv("SSH_ASKPASS_REQUIRE", "")
	repo := &Repo{dir: t.TempDir(), timeout: DefaultTimeout}

	out, err := repo.run(context.Background(), []string{"status"}, nil)

	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got := string(out); got != "0 /bin/false force unset" {
		t.Fatalf("the prompt guards are not on the call: %q", got)
	}
}

// An exit code is git deciding something, everything else is git saying
// nothing at all. Only the second kind carries ErrNoAnswer, and it is what
// lets a caller tell "this is no repository" from "git could not be asked".
func TestRunMarksOnlyTheCallsThatNeverAnswered(t *testing.T) {
	fakeGit(t, "echo 'fatal: not a git repository' >&2\nexit 128\n")
	_, err := (&Repo{dir: t.TempDir(), timeout: DefaultTimeout}).run(context.Background(), []string{"status"}, nil)
	if errors.Is(err, ErrNoAnswer) {
		t.Fatalf("git deciding something is an answer: %v", err)
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("git's own words must travel: %v", err)
	}

	// The deadline.
	fakeGit(t, "sleep 30\n")
	_, err = (&Repo{dir: t.TempDir(), timeout: time.Second}).run(context.Background(), []string{"status"}, nil)
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("a call the deadline ended answered nothing: %v", err)
	}
	if !strings.Contains(err.Error(), "no answer within") {
		t.Fatalf("the error must name the timeout: %v", err)
	}

	// A caller that dropped the call says so and never names the timeout,
	// which stood the whole time.
	fakeGit(t, "sleep 30\n")
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(200 * time.Millisecond)
		cancel()
	}()
	_, err = (&Repo{dir: t.TempDir(), timeout: time.Minute}).run(ctx, []string{"status"}, nil)
	cancel()
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("a cancelled call answered nothing: %v", err)
	}
	if !strings.Contains(err.Error(), "cancelled") || strings.Contains(err.Error(), "within") {
		t.Fatalf("a cancelled call must not report a timeout it never hit: %v", err)
	}
}

// git missing from the path is the same kind of nothing as a timeout: the
// process never ran, so it said nothing about the directory either.
func TestRunWithoutGitOnThePathIsNoAnswer(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := (&Repo{dir: t.TempDir(), timeout: DefaultTimeout}).run(context.Background(), []string{"status"}, nil)
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("a git that never ran must read as no answer: %v", err)
	}
}

// The poller hangs on this: a directory that is no repository is an answer
// and stays one between rounds, while a round that could not ask git knows
// nothing and must not be taken as an empty fingerprint, which would publish
// a moved base twice, once on the failure and once on the recovery.
func TestFingerprintTellsNoRepositoryFromNoAnswer(t *testing.T) {
	fakeGit(t, "echo 'fatal: not a git repository' >&2\nexit 128\n")
	if _, ok := (&Repo{dir: t.TempDir(), timeout: DefaultTimeout}).Fingerprint(context.Background()); !ok {
		t.Fatal("a directory that is no repository is an answer like any other")
	}

	fakeGit(t, "sleep 30\n")
	if _, ok := (&Repo{dir: t.TempDir(), timeout: time.Second}).Fingerprint(context.Background()); ok {
		t.Fatal("a round that could not ask git must not read as an empty repository")
	}
}

// With a prompt attached the deadline breathes: a call that outlives the
// base budget survives as long as answers keep arriving, and the same call
// without them dies at the budget.
func TestPromptAnswersStretchTheDeadline(t *testing.T) {
	fakeGit(t, "sleep 3\nprintf 'done'\n")
	answered := make(chan struct{}, 8)
	repo := (&Repo{dir: t.TempDir(), timeout: time.Second}).WithPrompt(&Prompt{
		Env:      []string{"DC_ASKPASS_TOKEN=t"},
		Asked:    make(chan struct{}),
		Answered: answered,
	})
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case answered <- struct{}{}:
				time.Sleep(300 * time.Millisecond)
			case <-stop:
				return
			}
		}
	}()

	out, err := repo.run(context.Background(), []string{"status"}, nil)
	if err != nil || string(out) != "done" {
		t.Fatalf("the answered call must live past its base budget: %q %v", out, err)
	}

	silent := (&Repo{dir: t.TempDir(), timeout: time.Second}).WithPrompt(&Prompt{
		Env:      []string{"DC_ASKPASS_TOKEN=t"},
		Asked:    make(chan struct{}),
		Answered: make(chan struct{}),
	})
	start := time.Now()
	_, err = silent.run(context.Background(), []string{"status"}, nil)
	if err == nil {
		t.Fatal("without answers the budget must stand")
	}
	// The breathing deadline still has to name itself. A write runs on a
	// context without cancellation, so nobody can have dropped it, and calling
	// its own timeout a cancellation is the one lie these messages exist to
	// avoid.
	if !errors.Is(err, ErrNoAnswer) {
		t.Fatalf("a call the watchdog ended answered nothing: %v", err)
	}
	if !strings.Contains(err.Error(), "no answer within 1s") {
		t.Fatalf("the watchdog must name the budget that ran out: %v", err)
	}
	if strings.Contains(err.Error(), "cancelled") {
		t.Fatalf("a deadline must not report itself as a dropped call: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2500*time.Millisecond {
		t.Fatalf("the silent call ran %v past its second", elapsed)
	}
}

// A question that goes out and is never answered ends in the person's own
// window, and the message names that window and not the action's budget: the
// two are minutes apart and only one of them ran out.
func TestPromptTimeoutNamesTheWindowThatRanOut(t *testing.T) {
	fakeGit(t, "sleep 30\n")
	asked := make(chan struct{}, 8)
	repo := (&Repo{dir: t.TempDir(), timeout: time.Hour}).WithPrompt(&Prompt{
		Env:      []string{"DC_ASKPASS_TOKEN=t"},
		Asked:    asked,
		Answered: make(chan struct{}),
	})
	// The question goes out right after the call starts, which arms the
	// watchdog with the person's window instead of the hour above.
	old := promptWait
	promptWait = time.Second
	defer func() { promptWait = old }()
	asked <- struct{}{}

	_, err := repo.run(context.Background(), []string{"push"}, nil)

	if err == nil || !strings.Contains(err.Error(), "no answer within 1s") {
		t.Fatalf("the message must name the window the question got: %v", err)
	}
}
