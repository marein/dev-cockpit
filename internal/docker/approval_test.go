package docker

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/detach"
)

// An assistant's run carries its owner from the request to the word at the
// end, through the register, so the process that reports it knows whose
// thread it goes into. It never becomes the user's own news: the newest run a
// project's notification names is the user's, whatever an assistant ran since.
func TestAnOwnedRunCarriesItsOwnerToTheEnd(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	dir := t.TempDir()
	service, done := newTestService(t, t.TempDir())
	id, err := service.RunCompose(ComposeOptions{Dir: dir, Label: "proj", Action: upAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, done)
	if got.run.Owner != "bot-1" || got.run.ID != id || got.run.Failed || got.run.Declined {
		t.Fatalf("run answered %+v", got.run)
	}
	if !got.run.Exited || got.run.Exit != 0 {
		t.Fatalf("the exit code did not travel: %+v", got.run)
	}
	view, ok := service.ComposeRunByID(id)
	if !ok || view.Owner != "bot-1" || view.Pending || view.Declined {
		t.Fatalf("view answered %+v, %v", view, ok)
	}
	if _, ok := service.LastComposeRun("proj"); ok {
		t.Fatal("an assistant's run became the project's own news")
	}
}

// A parked run is registered and resolved but starts nothing: no process, no
// directory held, and a second run in the same place is still allowed. The
// approval starts exactly what was parked.
func TestAParkedRunStartsOnApproval(t *testing.T) {
	record := fakeDockerCLI(t, 0, "")
	dir := t.TempDir()
	service, done := newTestService(t, t.TempDir())
	id, err := service.ParkCompose(ComposeOptions{Dir: dir, Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	view, ok := service.ComposeRunByID(id)
	if !ok || !view.Pending || view.Running || view.Owner != "bot-1" || view.Action != "Compose down" {
		t.Fatalf("the parked run reads as %+v, %v", view, ok)
	}
	if service.ComposeBusy(dir) {
		t.Fatal("a parked run holds its directory")
	}
	if _, err := os.Stat(record); err == nil {
		t.Fatal("a parked run ran its command")
	}
	if err := service.ApproveCompose(id); err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, done)
	if got.err != nil || got.run.ID != id || got.run.Owner != "bot-1" || got.run.Declined {
		t.Fatalf("the approved run answered %+v, %v", got.run, got.err)
	}
	raw, err := os.ReadFile(record)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "compose down") {
		t.Fatalf("the approved run ran %q", strings.TrimSpace(string(raw)))
	}
	if view, ok := service.ComposeRunByID(id); !ok || view.Pending || view.Running || !view.Exited {
		t.Fatalf("the finished run reads as %+v, %v", view, ok)
	}
	if err := service.ApproveCompose(id); err == nil {
		t.Fatal("a run that already ran was approved a second time")
	}
}

// A denied run never starts and ends declined with the reason it was given;
// the owner hears about it through the same completion path a failed run
// takes, without an exit code and without output.
func TestAParkedRunEndsDeclined(t *testing.T) {
	record := fakeDockerCLI(t, 0, "")
	service, done := newTestService(t, t.TempDir())
	id, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeclineCompose(id, "declined by the user", true); err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, done)
	if !got.run.Declined || !got.run.Failed || got.run.Owner != "bot-1" || got.run.Exited || got.output != "" {
		t.Fatalf("the declined run answered %+v, %q", got.run, got.output)
	}
	if got.err == nil || got.err.Error() != "declined by the user" {
		t.Fatalf("the reason did not travel: %v", got.err)
	}
	if !got.run.ByUser {
		t.Fatal("the user's own decline does not say so")
	}
	if _, err := os.Stat(record); err == nil {
		t.Fatal("a declined run ran its command")
	}
	view, ok := service.ComposeRunByID(id)
	if !ok || !view.Declined || view.Pending || view.Running || view.Failure != "declined by the user" {
		t.Fatalf("the declined run reads as %+v, %v", view, ok)
	}
	if err := service.DeclineCompose(id, "again", true); err == nil {
		t.Fatal("a run that is over was declined a second time")
	}
	if err := service.ApproveCompose(id); err == nil {
		t.Fatal("a declined run was approved")
	}
}

// The question a parked run waits for lives in the process that parked it. A
// process that starts over such an entry cannot answer it, so the run ends
// declined, and its owner reads why.
func TestAParkedRunIsDeclinedAfterARestart(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	stateDir := t.TempDir()
	gone, _ := newTestService(t, stateDir)
	id, err := gone.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	restarted, done := newTestService(t, stateDir)
	restarted.Recover()
	got := waitDone(t, done)
	if got.run.ID != id || !got.run.Declined || got.run.Owner != "bot-1" {
		t.Fatalf("the restart reported %+v", got.run)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "lost in a restart") {
		t.Fatalf("the reason reads %v", got.err)
	}
	if got.run.ByUser {
		t.Fatal("a decline nobody gave reads as the user's")
	}
	if view, ok := restarted.ComposeRunByID(id); !ok || !view.Declined || view.Pending {
		t.Fatalf("the run reads as %+v, %v", view, ok)
	}
}

// approvedAndGone parks a run, approves it the way ApproveCompose does up to
// the given step, and leaves the server there: "decided" stops after the
// start was written down and before anything started, "started" after the
// process exists and before the register heard of it. What it answers is the
// register as the next process finds it.
func approvedAndGone(t *testing.T, stateDir, dir, step string) ComposeRecord {
	t.Helper()
	gone, _ := newTestService(t, stateDir)
	id, err := gone.ParkCompose(ComposeOptions{Dir: dir, Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := gone.parked(id)
	if err != nil {
		t.Fatal(err)
	}
	rec = gone.markLaunching(rec)
	if step == "started" {
		if _, err := gone.spawn(rec); err != nil {
			t.Fatal(err)
		}
	}
	stored, ok := gone.runs.Get(id)
	if !ok || !stored.Pending || !stored.Launching || stored.PID != 0 {
		t.Fatalf("the register the restart finds reads %+v, %v", stored, ok)
	}
	return stored
}

// A restart after the approval decided the start and before any process
// existed has nothing to adopt. The run ends failed with the restart as the
// reason, never declined: the user did approve it.
func TestARestartBetweenApprovalAndLaunchFailsTheRun(t *testing.T) {
	record := fakeDockerCLI(t, 0, "")
	stateDir := t.TempDir()
	rec := approvedAndGone(t, stateDir, t.TempDir(), "decided")

	restarted, done := newTestService(t, stateDir)
	restarted.Recover()
	got := waitDone(t, done)
	if got.run.ID != rec.ID || got.run.Declined || !got.run.Failed || got.run.Owner != "bot-1" || got.run.Exited {
		t.Fatalf("the restart reported %+v", got.run)
	}
	if got.err == nil || !strings.Contains(got.err.Error(), "restarted before the run could start") {
		t.Fatalf("the reason reads %v", got.err)
	}
	if _, err := os.Stat(record); err == nil {
		t.Fatal("the restart started the run")
	}
	view, ok := restarted.ComposeRunByID(rec.ID)
	if !ok || view.Declined || view.Pending || view.Running {
		t.Fatalf("the run reads as %+v, %v", view, ok)
	}
}

// A restart after the approval started the process and before the register
// heard of it finds a run that is going. It is adopted, holds its directory,
// and its owner hears the real end, not a decline.
func TestARestartBetweenLaunchAndSaveAdoptsTheRun(t *testing.T) {
	record := fakeDockerCLI(t, 0, "1")
	stateDir := t.TempDir()
	dir := t.TempDir()
	rec := approvedAndGone(t, stateDir, dir, "started")

	restarted, done := newTestService(t, stateDir)
	restarted.Recover()
	if !restarted.ComposeBusy(dir) {
		t.Fatal("the adopted run does not hold its directory")
	}
	view, ok := restarted.ComposeRunByID(rec.ID)
	if !ok || view.Pending || !view.Running {
		t.Fatalf("the adopted run reads as %+v, %v", view, ok)
	}
	if stored, _ := restarted.runs.Get(rec.ID); stored.PID <= 0 || stored.Launching {
		t.Fatalf("the adopted run was not written down with its process: %+v", stored)
	}
	got := waitDone(t, done)
	if got.err != nil || got.run.ID != rec.ID || got.run.Declined || got.run.Failed || !got.run.Exited || got.run.Exit != 0 {
		t.Fatalf("the adopted run answered %+v, %v", got.run, got.err)
	}
	if !strings.Contains(got.output, "compose says hi") {
		t.Fatalf("the output of the adopted run is gone: %q", got.output)
	}
	raw, err := os.ReadFile(record)
	if err != nil || !strings.Contains(string(raw), "compose down") {
		t.Fatalf("the approved command did not run: %q, %v", raw, err)
	}
	if restarted.ComposeBusy(dir) {
		t.Fatal("the directory stayed held after the adopted run")
	}
}

// The same restart, with a run that ended before the next process came up:
// it left its result behind and is reported with its real exit code.
func TestARunThatEndedBeforeItsSaveIsReportedWithItsExitCode(t *testing.T) {
	fakeDockerCLI(t, 1, "")
	stateDir := t.TempDir()
	rec := approvedAndGone(t, stateDir, t.TempDir(), "started")
	probe, _ := newTestService(t, stateDir)
	_, lock, result := probe.runs.paths(rec.ID)
	waitFor(t, "the run to end", func() bool {
		_, wrote := detach.Result(result)
		return wrote && !detach.Alive(0, lock)
	})

	restarted, done := newTestService(t, stateDir)
	restarted.Recover()
	got := waitDone(t, done)
	if got.run.ID != rec.ID || got.run.Declined || !got.run.Failed || !got.run.Exited || got.run.Exit != 1 {
		t.Fatalf("the ended run reported %+v", got.run)
	}
	if got.err == nil || got.err.Error() != "exit status 1" {
		t.Fatalf("the reason reads %v", got.err)
	}
}

// Calling a parked run off is declining it: nothing runs, so there is
// nothing to kill, and it reads as cancelled like a running one would.
func TestCancellingAParkedRunDeclinesIt(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	service, done := newTestService(t, t.TempDir())
	id, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.CancelCompose(id, true); err != nil {
		t.Fatal(err)
	}
	got := waitDone(t, done)
	if !got.run.Declined || !got.run.ByUser || got.err == nil || got.err.Error() != "the run was cancelled" {
		t.Fatalf("the cancelled run answered %+v, %v", got.run, got.err)
	}
	if view, _ := service.ComposeRunByID(id); !view.Cancelled || !view.Declined {
		t.Fatalf("the run reads as %+v", view)
	}
}

// A command nobody can read is refused when the run is parked, not when it is
// approved: the approval shows what will run, so what is parked has to be a
// run that can start.
func TestAnUnreadableCommandIsNotParked(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	service, _ := newTestService(t, t.TempDir())
	action := downAction()
	action.Command = "docker compose 'down"
	if _, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: action, Owner: "bot-1"}); err == nil {
		t.Fatal("an unreadable command was parked")
	}
	if list := service.runs.List(); len(list) != 0 {
		t.Fatalf("the refusal left %+v behind", list)
	}
}

// An approval whose start fails, the directory taken meanwhile, ends the run
// with that reason instead of leaving a parked entry nobody can answer.
func TestAnApprovalThatCannotStartEndsTheRun(t *testing.T) {
	fakeDockerCLI(t, 0, "1")
	dir := t.TempDir()
	service, done := newTestService(t, t.TempDir())
	parked, err := service.ParkCompose(ComposeOptions{Dir: dir, Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RunCompose(ComposeOptions{Dir: dir, Label: "proj", Action: upAction()}); err != nil {
		t.Fatal(err)
	}
	if err := service.ApproveCompose(parked); err == nil {
		t.Fatal("the approval started a second run in a busy directory")
	}
	got := waitDone(t, done)
	if got.run.ID != parked || !got.run.Failed || got.run.Declined || got.err == nil {
		t.Fatalf("the refused start reported %+v, %v", got.run, got.err)
	}
	if view, _ := service.ComposeRunByID(parked); view.Pending || !strings.Contains(view.Failure, "already under way") {
		t.Fatalf("the run reads as %+v", view)
	}
	waitDone(t, done)
}

// An approval and a cancel that land together settle on exactly one outcome:
// either the cancel came first and the run ends declined without ever
// running, or the approval came first and the cancel calls the running run
// off. Never both, and never a run that reads declined while it runs.
func TestApproveAndCancelTogetherEndInOneOutcome(t *testing.T) {
	record := fakeDockerCLI(t, 0, "30")
	for i := 0; i < 40; i++ {
		os.Remove(record)
		service, done := newTestService(t, t.TempDir())
		id, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-1"})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		approved, cancelled := make(chan error, 1), make(chan error, 1)
		// The rounds walk the cancel from well ahead of the approval,
		// through the approval's read and launch, to one that finds the run
		// going, in steps shorter than either move takes.
		lead := time.Duration(i-10) * 100 * time.Microsecond
		go func() {
			<-start
			if lead < 0 {
				time.Sleep(-lead)
			}
			approved <- service.ApproveCompose(id)
		}()
		go func() {
			<-start
			if lead > 0 {
				time.Sleep(lead)
			}
			cancelled <- service.CancelCompose(id, true)
		}()
		close(start)
		approveErr, cancelErr := <-approved, <-cancelled
		if cancelErr != nil {
			t.Fatalf("round %d: the cancel was refused: %v", i, cancelErr)
		}
		got := waitDone(t, done)
		select {
		case second := <-done:
			t.Fatalf("round %d: one run reported twice: %+v then %+v", i, got.run, second.run)
		case <-time.After(100 * time.Millisecond):
		}
		view, _ := service.ComposeRunByID(id)
		if approveErr != nil {
			if !got.run.Declined || !view.Declined || !view.Cancelled {
				t.Fatalf("round %d: the cancel won but the run reads %+v, reported %+v", i, view, got.run)
			}
			if _, err := os.Stat(record); err == nil {
				t.Fatalf("round %d: a declined run ran its command", i)
			}
			continue
		}
		if got.run.Declined || view.Declined || !view.Cancelled || view.Running || view.Pending {
			t.Fatalf("round %d: the approval won but the run reads %+v, reported %+v", i, view, got.run)
		}
	}
}

// The daemon is asked again when the approval comes, which may be half an
// hour after the park: a run approved while nothing answers ends failed with
// the sentence a fresh start would get, and never starts.
func TestAnApprovalAsksTheDaemonAgain(t *testing.T) {
	record := fakeDockerCLI(t, 0, "")
	service, done := newTestService(t, t.TempDir())
	id, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	service.setState(State{})
	if err := service.ApproveCompose(id); err == nil || err.Error() != "no reachable Docker host" {
		t.Fatalf("the approval answered %v", err)
	}
	got := waitDone(t, done)
	if got.run.ID != id || !got.run.Failed || got.run.Declined {
		t.Fatalf("the refused start reported %+v", got.run)
	}
	if view, _ := service.ComposeRunByID(id); view.Pending || view.Running || view.Failure != "no reachable Docker host" {
		t.Fatalf("the run reads as %+v", view)
	}
	if _, err := os.Stat(record); err == nil {
		t.Fatal("the run started without a daemon")
	}
}

// A parked run whose process cannot be started stays in the register and is
// closed once, with the reason, instead of being deleted and written back.
func TestAParkedRunThatCannotStartStaysListed(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	dir := t.TempDir()
	service, done := newTestService(t, t.TempDir())
	id, err := service.ParkCompose(ComposeOptions{Dir: dir, Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := service.ApproveCompose(id); err == nil {
		t.Fatal("a run in a directory that is gone was started")
	}
	got := waitDone(t, done)
	if got.run.ID != id || !got.run.Failed || got.run.Declined {
		t.Fatalf("the failed start reported %+v", got.run)
	}
	view, ok := service.ComposeRunByID(id)
	if !ok || view.Pending || view.Running || view.Failure == "" {
		t.Fatalf("the run reads as %+v, %v", view, ok)
	}
	if service.ComposeBusy(dir) {
		t.Fatal("the failed start kept the directory")
	}
}

// An owner parks one run per directory and a bounded number in all; another
// owner in the same directory is somebody else's question.
func TestParkingIsBoundedPerOwner(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	service, _ := newTestService(t, t.TempDir())
	dir := t.TempDir()
	if _, err := service.ParkCompose(ComposeOptions{Dir: dir, Label: "proj", Action: downAction(), Owner: "bot-1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ParkCompose(ComposeOptions{Dir: dir, Label: "proj", Action: upAction(), Owner: "bot-1"}); err == nil {
		t.Fatal("a second run of one owner was parked in the same directory")
	}
	if _, err := service.ParkCompose(ComposeOptions{Dir: dir, Label: "proj", Action: downAction(), Owner: "bot-2"}); err != nil {
		t.Fatalf("another owner was refused: %v", err)
	}
	for i := 1; i < MaxPendingPerOwner; i++ {
		if _, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-1"}); err != nil {
			t.Fatalf("park %d was refused: %v", i+1, err)
		}
	}
	if _, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-1"}); err == nil {
		t.Fatal("an owner parked past the bound")
	}
}

// DeclinePending ends the parked runs it is told to and nothing else, and
// answers their ids.
func TestDeclinePendingEndsTheMatchingRuns(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	service, done := newTestService(t, t.TempDir())
	mine, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-2"})
	if err != nil {
		t.Fatal(err)
	}
	ids := service.DeclinePending(func(run RunView) bool { return run.Owner == "bot-1" }, "the assistant was deleted")
	if len(ids) != 1 || ids[0] != mine {
		t.Fatalf("declined %v, want %s", ids, mine)
	}
	got := waitDone(t, done)
	if got.run.ID != mine || !got.run.Declined || got.err.Error() != "the assistant was deleted" {
		t.Fatalf("the declined run reported %+v, %v", got.run, got.err)
	}
	if view, _ := service.ComposeRunByID(theirs); !view.Pending {
		t.Fatalf("another owner's run was touched: %+v", view)
	}
}

// A cancel that lands while a direct start is launching is never lost: it
// finds no run yet and is refused, or it finds the run with its process and
// ends it. What it may not do is answer yes and leave the run going, which a
// cancel between the entry and its process number once did.
func TestACancelDuringADirectStartIsNeverLost(t *testing.T) {
	fakeDockerCLI(t, 0, "30")
	for i := 0; i < 20; i++ {
		dir := t.TempDir()
		service, done := newTestService(t, t.TempDir())
		stop := make(chan struct{})
		accepted := make(chan string, 1)
		go func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, run := range service.ComposeRunsForDir(dir) {
					if service.CancelCompose(run.ID, true) == nil {
						accepted <- run.ID
						return
					}
				}
			}
		}()
		id, err := service.RunCompose(ComposeOptions{Dir: dir, Label: "proj", Action: upAction()})
		if err != nil {
			close(stop)
			t.Fatal(err)
		}
		var cancelled string
		select {
		case cancelled = <-accepted:
		case <-time.After(5 * time.Second):
			close(stop)
			t.Fatalf("round %d: no cancel was ever accepted", i)
		}
		close(stop)
		if cancelled != id {
			t.Fatalf("round %d: the cancel reached %s, want %s", i, cancelled, id)
		}
		got := waitDone(t, done)
		if got.err == nil || !strings.Contains(got.err.Error(), "cancelled") {
			t.Fatalf("round %d: an accepted cancel ended as %v", i, got.err)
		}
		if view, _ := service.ComposeRunByID(id); !view.Cancelled || view.Running {
			t.Fatalf("round %d: the run reads %+v", i, view)
		}
	}
}

// A run whose owner was gone when it ended is handed to the user after the
// fact: the entry loses its owner and becomes the project's own news. A run
// that is still going, one nobody owns and one nobody knows are left alone.
func TestDisownRunHandsAFinishedOwnedRunToTheUser(t *testing.T) {
	fakeDockerCLI(t, 0, "")
	service, done := newTestService(t, t.TempDir())
	id, err := service.RunCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: upAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
	if _, ok := service.LastComposeRun("proj"); ok {
		t.Fatal("an owned run is the project's news before it was handed over")
	}
	if !service.DisownRun(id) {
		t.Fatal("the finished owned run was not handed over")
	}
	if view, _ := service.ComposeRunByID(id); view.Owner != "" {
		t.Fatalf("the run still reads as owned: %+v", view)
	}
	if last, ok := service.LastComposeRun("proj"); !ok || last.ID != id {
		t.Fatalf("the project's news names %+v, %v", last, ok)
	}
	if service.DisownRun(id) || service.DisownRun("nobody") {
		t.Fatal("a run nobody owns or nobody knows was handed over")
	}
	parked, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err != nil {
		t.Fatal(err)
	}
	if service.DisownRun(parked) {
		t.Fatal("a run that is not over was handed over")
	}
}

// A confirm action on a stack that is already running a command is refused
// the way a direct start is, before anything is parked: an approval there
// could only end in that refusal, so the user is never asked for it.
func TestAParkOnABusyStackIsRefused(t *testing.T) {
	fakeDockerCLI(t, 0, "30")
	dir := t.TempDir()
	service, done := newTestService(t, t.TempDir())
	running, err := service.RunCompose(ComposeOptions{Dir: dir, Label: "proj", Action: upAction()})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.ParkCompose(ComposeOptions{Dir: dir, Label: "proj", Action: downAction(), Owner: "bot-1"})
	if err == nil || err.Error() != "a compose run is already under way here" {
		t.Fatalf("the park on a busy stack answered %v", err)
	}
	for _, rec := range service.runs.List() {
		if rec.Pending {
			t.Fatalf("the refusal left a parked run behind: %+v", rec)
		}
	}
	if _, err := service.ParkCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: downAction(), Owner: "bot-1"}); err != nil {
		t.Fatalf("a park on another stack was refused: %v", err)
	}
	if err := service.CancelCompose(running, false); err != nil {
		t.Fatal(err)
	}
	waitDone(t, done)
}

// A cancel of a running run says whose it was at the end: the user's own
// click reads as ByUser, a cancel an assistant gave does not.
func TestACancelOfARunningRunSaysWhoGaveIt(t *testing.T) {
	fakeDockerCLI(t, 0, "30")
	for _, byUser := range []bool{true, false} {
		service, done := newTestService(t, t.TempDir())
		id, err := service.RunCompose(ComposeOptions{Dir: t.TempDir(), Label: "proj", Action: upAction(), Owner: "bot-1"})
		if err != nil {
			t.Fatal(err)
		}
		if err := service.CancelCompose(id, byUser); err != nil {
			t.Fatal(err)
		}
		got := waitDone(t, done)
		if got.run.ID != id || got.run.Declined || got.err == nil || got.err.Error() != "the run was cancelled" {
			t.Fatalf("the cancelled run answered %+v, %v", got.run, got.err)
		}
		if got.run.ByUser != byUser {
			t.Fatalf("a cancel given by the user=%v reads ByUser=%v", byUser, got.run.ByUser)
		}
	}
}
