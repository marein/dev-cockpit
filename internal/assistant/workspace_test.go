package assistant

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// trustingRunner is a runner whose CLI keeps a trust answer per directory, the
// way claude and copilot do, so a test can see which directory was trusted
// before the turn.
type trustingRunner struct {
	*fakeRunner
	mu      sync.Mutex
	trusted []string
}

func (r *trustingRunner) TrustWorkdir(dir string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.trusted = append(r.trusted, dir)
	return nil
}

type trustingCoders struct{ runner *trustingRunner }

func (c trustingCoders) Available() []CoderInfo {
	return []CoderInfo{{ID: "claude", Label: "Claude", Runner: c.runner}}
}

// An assistant comes to its directory the way every directory here comes to
// be, on first use: creating one gives it a workspace with the files folder in
// it, and the path is the same one the store and the media address read.
func TestAnAssistantGetsAWorkspaceOfItsOwn(t *testing.T) {
	dir := t.TempDir()
	svc, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	created, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	_, instances, _ := Paths(dir)
	want := filepath.Join(instances, created.ID, workspaceDirName)
	if workspace.Dir(created.ID) != want || svc.store.WorkspaceDir(created.ID) != want {
		t.Fatalf("the workspace is spelled two ways: %q, %q", workspace.Dir(created.ID), svc.store.WorkspaceDir(created.ID))
	}
	if workdir, err := svc.workdirs.Workdir(created.ID); err != nil || workdir != want {
		t.Fatalf("the turn's workdir is elsewhere: %q (%v)", workdir, err)
	}
	if info, err := os.Stat(filepath.Join(want, FilesDirName)); err != nil || !info.IsDir() {
		t.Fatalf("want the files folder created with the workspace: %v", err)
	}
	uploads, err := svc.UploadDir(created.ID)
	if err != nil || !strings.HasPrefix(uploads, want) {
		t.Fatalf("want the uploads inside the workspace, got %q (%v)", uploads, err)
	}
	if _, err := workspace.Workdir("not an id"); err == nil {
		t.Fatal("a workspace was made for something that is not an assistant")
	}
}

// The check session sweep asks whether a directory is an assistant's
// workspace, whoever that assistant was: a stray check is recognized by where
// it ran, and a deleted assistant's directory still spells like one.
func TestIsWorkdirKnowsEveryInstanceWorkspace(t *testing.T) {
	dir := t.TempDir()
	_, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	_, instances, _ := Paths(dir)
	own := filepath.Join(instances, "11111111-1111-4111-8111-111111111111", workspaceDirName)
	for _, is := range []string{own, own + "/", filepath.Join(instances, "22222222-2222-4222-8222-222222222222", workspaceDirName)} {
		if !workspace.IsWorkdir(is) {
			t.Fatalf("%q is an assistant's workspace", is)
		}
	}
	for _, not := range []string{
		"",
		instances,
		filepath.Join(instances, "11111111-1111-4111-8111-111111111111"),
		filepath.Join(own, FilesDirName),
		filepath.Join(instances, "notes", workspaceDirName),
		filepath.Join(dir, "projects", "demo"),
	} {
		if workspace.IsWorkdir(not) {
			t.Fatalf("%q is not an assistant's workspace", not)
		}
	}
}

// Right before a turn starts, its assistant's workspace is readied: the
// instruction files stand in it, written for that assistant, and the coder's
// CLI has been told to trust the directory, so a non interactive turn never
// sits on a trust dialog.
func TestATurnFindsItsInstructionsAndATrustedWorkspace(t *testing.T) {
	dir := t.TempDir()
	runner := &trustingRunner{fakeRunner: &fakeRunner{dir: t.TempDir(), events: []Event{{Kind: EventDelta, Text: "hi"}}}}
	over := make(chan struct{})
	t.Cleanup(func() { runner.end(over) })
	runner.over = over
	svc, workspace, err := New(dir, trustingCoders{runner: runner}, Cockpit{Executable: "/opt/dev-cockpit/dev-cockpit"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	t.Cleanup(func() { quiesce(t, svc, nil) })
	created, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := svc.Rename(created.ID, "Release work"); err != nil {
		t.Fatalf("rename: %v", err)
	}
	if _, err := svc.Send(created.ID, "hello", nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	waitIdle(t, svc, created.ID)

	turns := runner.turns()
	if len(turns) != 1 || turns[0].Instance != created.ID || turns[0].Workdir != workspace.Dir(created.ID) {
		t.Fatalf("unexpected turn: %+v", turns)
	}
	if turns[0].Prompt != "hello" {
		t.Fatalf("want the prompt alone, nothing said in front of it, got %q", turns[0].Prompt)
	}
	for _, name := range instructionFiles {
		text := mustRead(t, filepath.Join(workspace.Dir(created.ID), name))
		for _, want := range []string{"You are `" + created.ID + "`", "\n./cockpit status", workspace.Wrapper(created.ID)} {
			if !strings.Contains(text, want) {
				t.Fatalf("%s is missing %q", name, want)
			}
		}
	}
	runner.mu.Lock()
	trusted := append([]string(nil), runner.trusted...)
	runner.mu.Unlock()
	if len(trusted) != 1 || trusted[0] != workspace.Dir(created.ID) {
		t.Fatalf("want the workspace trusted before the turn, got %v", trusted)
	}
}

// The wrapper is what a turn runs, so it has to run: with the binary, the
// directories and the id on the line and every argument handed on as it was,
// spaces and all, from any directory. It stands in the workspace with the
// instruction files, written by the same rebuild before every turn.
func TestTheWrapperRunsTheCockpitAsTheAssistant(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(t.TempDir(), "dev cockpit")
	if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatalf("write the stand-in binary: %v", err)
	}
	svc, workspace, err := New(dir, fakeCoders{runner: &fakeRunner{}}, Cockpit{
		Executable:  exe,
		StateDir:    "/var/lib/dc state",
		ProjectsDir: "/srv/projects",
	})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	created, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := workspace.Prepare(created.ID); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	wrapper := workspace.Wrapper(created.ID)
	if info, err := os.Stat(wrapper); err != nil || info.Mode().Perm()&0o100 == 0 {
		t.Fatalf("want an executable wrapper in the workspace: %v %v", info, err)
	}
	cmd := exec.Command(wrapper, "coder-send-prompt", "term-1", "keep going, two words")
	cmd.Dir = t.TempDir()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("run the wrapper: %v", err)
	}
	want := "assistant\n--state-dir\n/var/lib/dc state\n--projects-dir\n/srv/projects\n--as\n" + created.ID + "\ncoder-send-prompt\nterm-1\nkeep going, two words\n"
	if string(out) != want {
		t.Fatalf("the wrapper ran the cockpit as:\n%s\nwant:\n%s", out, want)
	}
}

// A check runs in the workspace of the assistant whose job it is and reads the
// same instructions as a chat turn: every ability a conversation has, the
// files it can hand over, what it knows about the user, is one a check may
// need for its report, which goes to the user like an answer does.
func TestACheckRunsInTheAssistantsWorkspace(t *testing.T) {
	dir := t.TempDir()
	runner := &trustingRunner{fakeRunner: &fakeRunner{dir: t.TempDir(), answer: answering("NOTHING")}}
	over := make(chan struct{})
	t.Cleanup(func() { runner.end(over) })
	runner.over = over
	svc, workspace, err := New(dir, trustingCoders{runner: runner}, Cockpit{Executable: "/opt/dev-cockpit/dev-cockpit"})
	if err != nil {
		t.Fatalf("new: %v", err)
	}
	watcher := NewWatcher(svc, NewJobs(svc.store), &fakeSessions{activity: Activity{Text: "the coder said something"}}, nil)
	t.Cleanup(func() { quiesce(t, svc, watcher) })
	created, err := svc.Create("claude")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := watcher.Steer(Job{Owner: created.ID, Terminal: "term-1", CoderID: "claude", DoneWhen: "x"}); err != nil {
		t.Fatalf("steer: %v", err)
	}
	watcher.Handle("term-1")
	waitFor(t, "the check to start", func() bool { return len(runner.turns()) == 1 })

	turn := runner.turns()[0]
	if turn.Workdir != workspace.Dir(created.ID) {
		t.Fatalf("the check did not run in the assistant's workspace: %+v", turn)
	}
	if strings.Contains(turn.Prompt, "--as ") || !strings.Contains(turn.Prompt, "`./cockpit terminal-screen term-1`") || !strings.Contains(turn.Prompt, "`"+workspace.Wrapper(created.ID)+"`") {
		t.Fatalf("the check prompt does not spell the workspace wrapper:\n%s", turn.Prompt)
	}
	text := mustRead(t, filepath.Join(workspace.Dir(created.ID), "CLAUDE.md"))
	for _, want := range []string{"# Cockpit assistant", "You are `" + created.ID + "`", "## Remembering", "assistant-files"} {
		if !strings.Contains(text, want) {
			t.Fatalf("the instructions a check reads are missing %q", want)
		}
	}
}
