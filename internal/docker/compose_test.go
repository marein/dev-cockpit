package docker

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/detach"
)

func TestComposeFileFindsTheCLIOrder(t *testing.T) {
	dir := t.TempDir()
	if _, ok := ComposeFile(dir); ok {
		t.Fatal("empty dir claims a compose file")
	}
	for _, name := range []string{"docker-compose.yml", "compose.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("services: {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	path, ok := ComposeFile(dir)
	if !ok || filepath.Base(path) != "compose.yaml" {
		t.Fatalf("ComposeFile answered %q, %v", path, ok)
	}
}

func TestStacksForDirGroupsByWorkingDirPlusRoot(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "ops")
	if err := os.Mkdir(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "compose.yaml"), []byte("services: {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := State{Containers: []Container{
		{Name: "a", State: "running", WorkingDir: sub},
		{Name: "b", State: "exited", WorkingDir: sub},
	}}
	stacks := state.StacksForDir(root)
	if len(stacks) != 2 {
		t.Fatalf("stacks answered %+v", stacks)
	}
	if stacks[0].Dir != filepath.Clean(root) || stacks[0].Label != "" || stacks[0].Total != 0 {
		t.Fatalf("root stack answered %+v", stacks[0])
	}
	if stacks[1].Dir != sub || stacks[1].Label != "ops" || stacks[1].Running != 1 || stacks[1].Total != 2 {
		t.Fatalf("sub stack answered %+v", stacks[1])
	}
}

func TestExecAndLogsCommands(t *testing.T) {
	exec := ExecCommand(defaultSocketHost, "app-web-1")
	if !strings.HasPrefix(exec, "docker exec -it app-web-1 sh -c ") || strings.Contains(exec, "DOCKER_HOST") {
		t.Fatalf("exec answered %q", exec)
	}
	logs := LogsCommand("tcp://box:2375", "app-web-1")
	if !strings.HasPrefix(logs, "DOCKER_HOST=tcp://box:2375 docker logs -f --tail 200 app-web-1") {
		t.Fatalf("logs answered %q", logs)
	}
	if quoted := shellQuote("unix:///a dir/s.sock"); quoted != "'unix:///a dir/s.sock'" {
		t.Fatalf("quote answered %q", quoted)
	}
}

// fakeDockerCLI puts a docker stand-in first on the PATH that records its
// argv and cwd, sleeps for the given time so busy states are observable,
// then answers with the given exit code.
func fakeDockerCLI(t *testing.T, exitCode int, sleep string) string {
	t.Helper()
	dir := t.TempDir()
	record := filepath.Join(dir, "record")
	script := "#!/bin/sh\necho \"$PWD $DOCKER_HOST $@\" > " + shellQuote(record) + "\necho compose says hi\n"
	if sleep != "" {
		script += "sleep " + sleep + "\n"
	}
	script += "exit " + map[int]string{0: "0", 1: "1"}[exitCode] + "\n"
	if err := os.WriteFile(filepath.Join(dir, "docker"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return record
}

// outcome is one finished run as the completion callback reports it.
type outcome struct {
	run    ComposeRun
	err    error
	output string
}

// newTestService is a service over a state directory of its own, with the one
// completion callback wired to a channel. A second one over the same directory
// is what a restart looks like.
func newTestService(t *testing.T, stateDir string) (*Service, chan outcome) {
	t.Helper()
	service := NewService(stateDir, func() string { return "" })
	service.setState(State{Available: true, Host: "tcp://fake:1"})
	done := make(chan outcome, 4)
	service.OnComposeDone(func(run ComposeRun, err error, output string) {
		done <- outcome{run: run, err: err, output: output}
	})
	return service, done
}

func waitDone(t *testing.T, done chan outcome) outcome {
	t.Helper()
	select {
	case got := <-done:
		return got
	case <-time.After(15 * time.Second):
		t.Fatal("compose run never finished")
		return outcome{}
	}
}

func TestRunComposeUpRunsInDirWithHost(t *testing.T) {
	record := fakeDockerCLI(t, 0, "")
	dir := t.TempDir()
	service, done := newTestService(t, t.TempDir())
	if _, err := service.RunCompose(ComposeOptions{Dir: dir, Label: "proj", Action: upAction()}); err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, done)
	if got.err != nil {
		t.Fatalf("run answered %v", got.err)
	}
	if !strings.Contains(got.output, "compose says hi") {
		t.Fatalf("output answered %q", got.output)
	}
	if got.run.Label != "proj" || got.run.Action != "Compose up" || got.run.Failed || got.run.Dir != dir {
		t.Fatalf("run answered %+v", got.run)
	}
	if run, ok := service.LastComposeRun("proj"); !ok || run.Action != "Compose up" || run.Failure != "" {
		t.Fatalf("LastComposeRun answered %+v, %v", run, ok)
	}
	if _, ok := service.LastComposeRun("somebody else"); ok {
		t.Fatal("another project got this run as its news")
	}
	// The run is over, and its entry stays: the output is what somebody reads
	// afterwards, and the view answers out of it.
	view, ok := service.ComposeRunByID(got.run.ID)
	if !ok || view.Running || !view.Exited || view.Exit != 0 || view.Failure != "" {
		t.Fatalf("the finished run reads as %+v, %v", view, ok)
	}
	if view.Command != "docker compose up -d" {
		t.Fatalf("the run does not carry its command line: %q", view.Command)
	}
	if !strings.Contains(service.ComposeRunOutput(got.run.ID), "compose says hi") {
		t.Fatal("the output of the finished run cannot be read any more")
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(line, dir+" tcp://fake:1 compose up -d") {
		t.Fatalf("cli saw %q", line)
	}
}

func TestRunComposeSurfacesFailureAndBusy(t *testing.T) {
	fakeDockerCLI(t, 1, "0.3")
	dir := t.TempDir()
	service, done := newTestService(t, t.TempDir())
	if _, err := service.RunCompose(ComposeOptions{Dir: dir, Label: "proj", Action: downAction()}); err != nil {
		t.Fatal(err)
	}
	if !service.ComposeBusy(dir) {
		t.Fatal("run not marked busy")
	}
	if _, err := service.RunCompose(ComposeOptions{Dir: dir, Label: "proj", Action: downAction()}); err == nil {
		t.Fatal("second run in the same dir accepted")
	}
	got := waitDone(t, done)
	if got.err == nil {
		t.Fatal("failure not surfaced")
	}
	if service.ComposeBusy(dir) {
		t.Fatal("busy flag not cleared")
	}
	if run, ok := service.LastComposeRun("proj"); !ok || run.Failure == "" || run.Action != "Compose down" {
		t.Fatalf("LastComposeRun answered %+v, %v", run, ok)
	}
}

// A deletion asks about the whole project, not about one compose directory: a
// run below it counts, and it counts until it is really over, which is past
// the moment its containers stop being listed.
func TestComposeBusyUnderCoversTheWholeProject(t *testing.T) {
	fakeDockerCLI(t, 0, "0.5")
	project := t.TempDir()
	stack := filepath.Join(project, "ops")
	if err := os.Mkdir(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	service, done := newTestService(t, t.TempDir())
	if service.ComposeBusyUnder(project) {
		t.Fatal("an idle project reads as busy")
	}
	if _, err := service.RunCompose(ComposeOptions{Dir: stack, Label: "proj", Action: upAction()}); err != nil {
		t.Fatal(err)
	}
	if !service.ComposeBusyUnder(project) {
		t.Fatal("a run in a subdirectory does not count for the project")
	}
	if service.ComposeBusyUnder(t.TempDir()) {
		t.Fatal("an unrelated project reads as busy")
	}
	if service.ComposeBusyUnder("") {
		t.Fatal("no directory at all reads as busy")
	}
	waitDone(t, done)
	if service.ComposeBusyUnder(project) {
		t.Fatal("the project stays busy after the run ended")
	}
}

func TestRunComposeRefusesWithoutDaemon(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	service := NewService(t.TempDir(), func() string { return "" })
	if _, err := service.RunCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: upAction()}); err == nil {
		t.Fatal("run without a daemon accepted")
	}
}

// A run that is still going when the cockpit restarts is picked up again: the
// directory is claimed, the busy mark is back, and the word at the end reaches
// the new process. startCompose without a waiter is what the old server left
// behind when it went away mid-run.
func TestAComposeRunSurvivesARestart(t *testing.T) {
	fakeDockerCLI(t, 0, "1")
	dir := t.TempDir()
	stateDir := t.TempDir()
	gone, _ := newTestService(t, stateDir)
	rec, _, err := gone.startCompose(ComposeOptions{Dir: dir, Label: "proj", Action: upAction()})
	if err != nil {
		t.Fatal(err)
	}

	restarted, done := newTestService(t, stateDir)
	restarted.Recover()
	if !restarted.ComposeBusy(dir) {
		t.Fatal("the adopted run is not marked busy")
	}
	if _, err := restarted.RunCompose(ComposeOptions{Dir: dir, Label: "proj", Action: upAction()}); err == nil {
		t.Fatal("a second run in the adopted directory was accepted")
	}
	got := waitDone(t, done)
	if got.err != nil {
		t.Fatalf("the adopted run answered %v", got.err)
	}
	if got.run.ID != rec.ID || got.run.Label != "proj" {
		t.Fatalf("the adopted run reported as %+v", got.run)
	}
	if restarted.ComposeBusy(dir) {
		t.Fatal("busy flag not cleared after the adopted run")
	}
	if view, ok := restarted.ComposeRunByID(rec.ID); !ok || view.Running {
		t.Fatalf("the adopted run still reads as going: %+v, %v", view, ok)
	}
}

// A run that finished while nobody was there to hear it says so at the next
// start, which is the notification the restart would otherwise have swallowed.
func TestAFinishedRunIsReportedAfterARestart(t *testing.T) {
	fakeDockerCLI(t, 1, "")
	dir := t.TempDir()
	stateDir := t.TempDir()
	gone, _ := newTestService(t, stateDir)
	rec, proc, err := gone.startCompose(ComposeOptions{Dir: dir, Label: "proj", Action: downAction(), Quiet: true})
	if err != nil {
		t.Fatal(err)
	}
	proc.Wait()
	waitFor(t, "the run to release its lock", func() bool {
		_, lock, _ := gone.runs.paths(rec.ID)
		return !detach.Alive(rec.PID, lock)
	})

	restarted, done := newTestService(t, stateDir)
	restarted.Recover()
	got := waitDone(t, done)
	if got.err == nil {
		t.Fatal("the failure of the finished run was not reported")
	}
	if !got.run.Quiet || got.run.Action != "Compose down" {
		t.Fatalf("the run lost what it was: %+v", got.run)
	}
	if !strings.Contains(got.output, "compose says hi") {
		t.Fatalf("the output of the finished run is gone: %q", got.output)
	}
	// Reported and written down: what the run needed while it ran is gone, the
	// output it left stays, that is what the view reads.
	entries, err := os.ReadDir(filepath.Join(stateDir, "docker", "runs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || !strings.HasSuffix(entries[0].Name(), ".out") {
		t.Fatalf("the run left %v behind", entries)
	}
}

// A run whose hold process was killed outright wrote no exit code. That says
// the run did not finish, and it must not read as success.
func TestAKilledRunReportsAsFailed(t *testing.T) {
	fakeDockerCLI(t, 0, "30")
	dir := t.TempDir()
	stateDir := t.TempDir()
	gone, _ := newTestService(t, stateDir)
	rec, proc, err := gone.startCompose(ComposeOptions{Dir: dir, Label: "proj", Action: upAction()})
	if err != nil {
		t.Fatal(err)
	}
	proc.Kill()
	waitFor(t, "the killed run to end", func() bool {
		_, lock, _ := gone.runs.paths(rec.ID)
		return !detach.Alive(rec.PID, lock)
	})

	restarted, done := newTestService(t, stateDir)
	restarted.Recover()
	got := waitDone(t, done)
	if got.err == nil || !got.run.Failed {
		t.Fatalf("a killed run answered %+v, %v", got.run, got.err)
	}
}

// upAction and downAction are the two default entries the tests drive, the
// same ones an install that never touched the setting has.
func upAction() Action   { return DefaultActions()[0] }
func downAction() Action { return DefaultActions()[1] }

// A run somebody calls off ends as cancelled, not as a run that simply
// stopped saying anything: the entry says so and the view reads it back.
func TestACancelledRunSaysSo(t *testing.T) {
	fakeDockerCLI(t, 0, "30")
	dir := t.TempDir()
	service, done := newTestService(t, t.TempDir())
	id, err := service.RunCompose(ComposeOptions{Dir: dir, Label: "proj", Action: upAction()})
	if err != nil {
		t.Fatal(err)
	}
	if view, ok := service.ComposeRunByID(id); !ok || !view.Running {
		t.Fatalf("the fresh run reads as %+v, %v", view, ok)
	}
	if err := service.CancelCompose(id); err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, done)
	if got.err == nil || !strings.Contains(got.err.Error(), "cancelled") {
		t.Fatalf("the cancelled run answered %v", got.err)
	}
	view, ok := service.ComposeRunByID(id)
	if !ok || view.Running || !view.Cancelled {
		t.Fatalf("the cancelled run reads as %+v, %v", view, ok)
	}
	if err := service.CancelCompose(id); err == nil {
		t.Fatal("a run that is over was cancelled again")
	}
	if runs := service.ComposeRunsForDir(dir); len(runs) != 1 || runs[0].ID != id {
		t.Fatalf("the directory answered %+v", runs)
	}
}

// A command that cannot be read never becomes a run: nothing is claimed, no
// files are laid out, and the caller hears why.
func TestAnUnreadableCommandNeverStarts(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	dir := t.TempDir()
	service, _ := newTestService(t, t.TempDir())
	_, err := service.RunCompose(ComposeOptions{Dir: dir, Label: "proj", Action: Action{Label: "Broken", Command: `docker compose up "`}})
	if err == nil {
		t.Fatal("a command with an unclosed quote started")
	}
	if service.ComposeBusy(dir) {
		t.Fatal("the refused command claimed the directory")
	}
	if len(service.runs.List()) != 0 {
		t.Fatal("the refused command left an entry behind")
	}
}

// A program of the project is what the run starts, absolute, because the
// hold process resolves it before it stands in the stack directory.
func TestAProjectScriptRunsFromTheStackDirectory(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	root := t.TempDir()
	stack := filepath.Join(root, "ops")
	if err := os.Mkdir(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	record := filepath.Join(root, "record")
	script := "#!/bin/sh\necho \"$PWD $1\" > " + shellQuote(record) + "\n"
	if err := os.WriteFile(filepath.Join(root, "deploy.sh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	service, done := newTestService(t, t.TempDir())
	action := Action{ID: "deploy", Label: "Deploy", Command: "./deploy.sh now", Timeout: "1m"}
	if _, err := service.RunCompose(ComposeOptions{Dir: stack, Root: root, Label: "proj", Action: action}); err != nil {
		t.Fatal(err)
	}
	if got := waitDone(t, done); got.err != nil {
		t.Fatalf("the script answered %v", got.err)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if line := strings.TrimSpace(string(raw)); line != stack+" now" {
		t.Fatalf("the script saw %q", line)
	}
}

// A waiter's bound is the run's own timeout, not a flat number: the deadline
// follows the running entry, covers a run in a subdirectory, and clears when
// the run is over.
func TestComposeDeadlineFollowsTheRunningEntry(t *testing.T) {
	fakeDockerCLI(t, 0, "0.4")
	project := t.TempDir()
	stack := filepath.Join(project, "ops")
	if err := os.Mkdir(stack, 0o755); err != nil {
		t.Fatal(err)
	}
	service, done := newTestService(t, t.TempDir())
	if _, ok := service.ComposeDeadline(project); ok {
		t.Fatal("an idle project answered a deadline")
	}
	// upAction carries the default 10m timeout.
	if _, err := service.RunCompose(ComposeOptions{Dir: stack, Label: "proj", Action: upAction()}); err != nil {
		t.Fatal(err)
	}
	deadline, ok := service.ComposeDeadline(project)
	if !ok {
		t.Fatal("a running run answered no deadline")
	}
	if deadline.Before(time.Now().Add(9*time.Minute)) || deadline.After(time.Now().Add(12*time.Minute)) {
		t.Fatalf("the deadline does not follow the run's timeout: %s", deadline)
	}
	if _, ok := service.ComposeDeadline(t.TempDir()); ok {
		t.Fatal("an unrelated directory answered a deadline")
	}
	waitDone(t, done)
	if _, ok := service.ComposeDeadline(project); ok {
		t.Fatal("a finished run kept its deadline")
	}
}

// Two projects finishing a run in the same moment are two pieces of news, and
// each one has to find its own: a single "the last run" would hand both of
// them whichever finished last.
func TestLastComposeRunAnswersPerProject(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	stateDir := t.TempDir()
	service, done := newTestService(t, stateDir)
	first, second := t.TempDir(), t.TempDir()
	if _, err := service.RunCompose(ComposeOptions{Dir: first, Label: "one", Action: upAction()}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	if _, err := service.RunCompose(ComposeOptions{Dir: second, Label: "two", Action: downAction()}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)

	one, ok := service.LastComposeRun("one")
	if !ok || one.Action != "Compose up" || one.Dir != first {
		t.Fatalf("project one answered %+v, %v", one, ok)
	}
	two, ok := service.LastComposeRun("two")
	if !ok || two.Action != "Compose down" || two.Dir != second {
		t.Fatalf("project two answered %+v, %v", two, ok)
	}
	// The newest of a project's own runs is the one it is about.
	if _, err := service.RunCompose(ComposeOptions{Dir: first, Label: "one", Action: downAction()}); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	if again, ok := service.LastComposeRun("one"); !ok || again.Action != "Compose down" {
		t.Fatalf("project one answered %+v, %v after its second run", again, ok)
	}
}
