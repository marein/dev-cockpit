package detach

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// files are the three paths one run lives in.
func files(t *testing.T) (out, lock, result string) {
	t.Helper()
	dir := t.TempDir()
	return filepath.Join(dir, "run.out"), filepath.Join(dir, "run.lock"), filepath.Join(dir, "run.result")
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func read(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// The whole contract in one run: the output lands in the file, the working
// directory and the environment are the ones the caller asked for, standard
// error joins the same file when no second one is named, and the exit code is
// on disk by the time the lock is free.
func TestStartRunsDetachedAndWritesItsResult(t *testing.T) {
	out, lock, result := files(t)
	workdir := t.TempDir()
	p, err := Start(Options{
		Command: []string{"sh", "-c", "pwd; echo $GREETING; echo to stderr >&2; exit 3"},
		Dir:     workdir,
		Env:     append(os.Environ(), "GREETING=hello"),
		Out:     out,
		Lock:    lock,
		Result:  result,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if p.PID() <= 0 {
		t.Fatal("start answered no process")
	}
	p.Wait()
	// The lock is the truth about a run, and it is free once the hold process
	// is gone.
	waitFor(t, "the lock to be released", func() bool { return !Alive(p.PID(), lock) })

	text := read(t, out)
	for _, want := range []string{workdir, "hello", "to stderr"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %q misses %q", text, want)
		}
	}
	code, ok := Result(result)
	if !ok || code != 3 {
		t.Fatalf("result answered %d, %v", code, ok)
	}
	if TimedOut(code) {
		t.Fatal("an ordinary failure reads as a timeout")
	}
}

// A run that is still going holds its lock, and that is what a later server
// reads it by: it knows nothing but the process number and the lock file.
func TestAnAdoptedRunIsAliveWhileItHoldsTheLock(t *testing.T) {
	out, lock, result := files(t)
	p, err := Start(Options{Command: []string{"sleep", "30"}, Out: out, Lock: lock, Result: result})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	adopted := Adopt(p.PID(), lock)
	if !adopted.Alive() {
		t.Fatal("a running run does not read as alive")
	}
	if _, ok := Result(result); ok {
		t.Fatal("a running run already wrote a result")
	}
	adopted.Kill()
	waitFor(t, "the killed run to end", func() bool { return !adopted.Alive() })
	// Killed outright, so the hold process never got to write anything down.
	// That absence is the answer, not a zero.
	if _, ok := Result(result); ok {
		t.Fatal("a killed run left a result behind")
	}
	p.Wait()
}

// The timeout belongs to the hold process, not to the server that asked for the
// run: the server may be long gone when it passes.
func TestTheHoldProcessEnforcesTheTimeout(t *testing.T) {
	out, lock, result := files(t)
	p, err := Start(Options{
		Command: []string{"sleep", "30"},
		Out:     out,
		Lock:    lock,
		Result:  result,
		Timeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	p.Wait()
	code, ok := Result(result)
	if !ok || !TimedOut(code) {
		t.Fatalf("result answered %d, %v", code, ok)
	}
	if text := read(t, out); !strings.Contains(text, "timed out after") {
		t.Fatalf("the run does not say why it ended: %q", text)
	}
}

// A timeout must end everything the program spawned, not the program alone:
// helpers inherit the run's lock, so a survivor would keep the run reading as
// alive while the result already says it timed out.
func TestTheTimeoutKillsWhatTheProgramSpawned(t *testing.T) {
	out, lock, result := files(t)
	p, err := Start(Options{
		Command: []string{"sh", "-c", "sleep 30 & sleep 30"},
		Out:     out,
		Lock:    lock,
		Result:  result,
		Timeout: 300 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	p.Wait()
	// The background sleep holds the inherited lock; only the group kill
	// frees it within this window.
	waitFor(t, "the whole process tree to be gone", func() bool { return !Alive(p.PID(), lock) })
	code, ok := Result(result)
	if !ok || !TimedOut(code) {
		t.Fatalf("result answered %d, %v", code, ok)
	}
}

// A program that is not there is refused where the caller still hears about it,
// and nothing is left behind that would look like a run.
func TestStartRefusesWhatItCannotRun(t *testing.T) {
	out, lock, result := files(t)
	if _, err := Start(Options{Command: []string{"dev-cockpit-no-such-program"}, Out: out, Lock: lock, Result: result}); err == nil {
		t.Fatal("start accepted a program that does not exist")
	}
	if _, err := Start(Options{Command: nil, Out: out, Lock: lock}); err == nil {
		t.Fatal("start accepted an empty command")
	}
}

func TestParseHoldArgs(t *testing.T) {
	result, timeout, argv, err := parseHoldArgs([]string{"--result", "/tmp/r", "--timeout", "5m", "--", "docker", "compose", "--timeout", "1"})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if result != "/tmp/r" || timeout != 5*time.Minute {
		t.Fatalf("parse answered %q, %s", result, timeout)
	}
	// Everything behind the separator is the program's, including arguments
	// that read like ours.
	if strings.Join(argv, " ") != "docker compose --timeout 1" {
		t.Fatalf("argv answered %v", argv)
	}
	if _, _, argv, err = parseHoldArgs([]string{"echo", "hi"}); err != nil || strings.Join(argv, " ") != "echo hi" {
		t.Fatalf("a bare command answered %v, %v", argv, err)
	}
	if _, _, _, err = parseHoldArgs(nil); err == nil {
		t.Fatal("nothing to run was accepted")
	}
	if _, _, _, err = parseHoldArgs([]string{"--timeout", "soon", "--", "echo"}); err == nil {
		t.Fatal("an unreadable timeout was accepted")
	}
}
