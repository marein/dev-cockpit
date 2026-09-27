package docker

import (
	"strings"
	"testing"
	"time"
)

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
	if got.run.Owner != "bot-1" || got.run.ID != id || got.run.Failed {
		t.Fatalf("run answered %+v", got.run)
	}
	if !got.run.Exited || got.run.Exit != 0 {
		t.Fatalf("the exit code did not travel: %+v", got.run)
	}
	view, ok := service.ComposeRunByID(id)
	if !ok || view.Owner != "bot-1" {
		t.Fatalf("view answered %+v, %v", view, ok)
	}
	if _, ok := service.LastComposeRun("proj"); ok {
		t.Fatal("an assistant's run became the project's own news")
	}
}

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
}

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
		if got.run.ID != id || got.err == nil || got.err.Error() != "the run was cancelled" {
			t.Fatalf("the cancelled run answered %+v, %v", got.run, got.err)
		}
		if got.run.ByUser != byUser {
			t.Fatalf("a cancel given by the user=%v reads ByUser=%v", byUser, got.run.ByUser)
		}
	}
}
