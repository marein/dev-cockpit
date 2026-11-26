package coder

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

// waitSettled waits for the refresh a read started to come back, which every
// refresh does: a hung one is ended by its deadline.
func waitSettled[T any](t *testing.T, list *ModelList[T]) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		list.mu.Lock()
		done := !list.refreshing
		list.mu.Unlock()
		if done {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the refresh never came back")
}

// The list is fetched in the background and never on the request path: the
// first read answers empty and starts the fetch, a read inside the TTL
// answers the cache without a process, a read past it answers the cache and
// starts the refresh, and a refresh that failed keeps what stood.
func TestTheModelListRefreshesInTheBackgroundAndKeepsWhatStood(t *testing.T) {
	const ttl = 10 * time.Minute
	var mu sync.Mutex
	runs := 0
	fail := false
	list := NewModelList(ttl, func(context.Context) ([]string, error) {
		mu.Lock()
		defer mu.Unlock()
		runs++
		if fail {
			return nil, errors.New("the CLI is broken")
		}
		return []string{"big-pickle"}, nil
	})
	count := func() int {
		mu.Lock()
		defer mu.Unlock()
		return runs
	}

	start := time.Now()
	if got := list.Entries(start); len(got) != 0 {
		t.Fatalf("want the first read to answer nothing while the fetch runs, got %v", got)
	}
	waitSettled(t, list)
	if count() != 1 {
		t.Fatalf("want the first read to start one fetch, got %d", count())
	}
	if got := list.Entries(start.Add(time.Minute)); strings.Join(got, ",") != "big-pickle" || count() != 1 {
		t.Fatalf("want the cache answered without a process inside the TTL, got %v after %d runs", got, count())
	}

	mu.Lock()
	fail = true
	mu.Unlock()
	if got := list.Entries(list.at.Add(ttl)); strings.Join(got, ",") != "big-pickle" {
		t.Fatalf("want the stale cache answered while the refresh runs, got %v", got)
	}
	waitSettled(t, list)
	if count() != 2 {
		t.Fatalf("want the read past the TTL to start a refresh, got %d runs", count())
	}
	if got := list.Entries(list.at.Add(time.Minute)); strings.Join(got, ",") != "big-pickle" || count() != 2 {
		t.Fatalf("want a failed refresh to keep what stood and wait, got %v after %d runs", got, count())
	}
}

// A refresh that hangs is ended by its deadline and read as a failed one: the
// refreshing flag is cleared, what stood is kept, and the read past the TTL
// starts the next try instead of serving the stale list for the life of the
// process. The fetch itself is what the deadline reaches, through its context.
func TestAHungModelListRefreshIsEndedByItsDeadline(t *testing.T) {
	const ttl = 10 * time.Minute
	var mu sync.Mutex
	runs := 0
	ended := 0
	list := NewModelList(ttl, func(ctx context.Context) ([]string, error) {
		mu.Lock()
		runs++
		mu.Unlock()
		<-ctx.Done()
		mu.Lock()
		ended++
		mu.Unlock()
		return nil, ctx.Err()
	})
	list.timeout = 50 * time.Millisecond
	list.cached = []string{"big-pickle"}
	count := func() (int, int) {
		mu.Lock()
		defer mu.Unlock()
		return runs, ended
	}

	if got := list.Entries(time.Now()); strings.Join(got, ",") != "big-pickle" {
		t.Fatalf("want what stood answered while the refresh runs, got %v", got)
	}
	waitSettled(t, list)
	if runs, ended := count(); runs != 1 || ended != 1 {
		t.Fatalf("want the one fetch started and ended by its context, got %d started and %d ended", runs, ended)
	}
	if got := list.Entries(list.at.Add(time.Minute)); strings.Join(got, ",") != "big-pickle" {
		t.Fatalf("want a failed refresh to keep what stood, got %v", got)
	}
	if runs, _ := count(); runs != 1 {
		t.Fatalf("want a read inside the TTL to start nothing, got %d runs", runs)
	}
	list.Entries(list.at.Add(ttl))
	waitSettled(t, list)
	if runs, ended := count(); runs != 2 || ended != 2 {
		t.Fatalf("want the read past the TTL to try again, got %d started and %d ended", runs, ended)
	}
}

// A list command that never ends is ended at the deadline and answers as a
// failure, never as a list, so the refresh that ran it comes back.
func TestRunListCommandEndsTheProcessAtTheDeadline(t *testing.T) {
	if _, err := exec.LookPath("sleep"); err != nil {
		t.Skip("no sleep on this host")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	out, err := RunListCommand(ctx, 1<<20, "sleep", "30")
	if err == nil || out != "" {
		t.Fatalf("want a failure and no list from a hung process, got %q and %v", out, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the deadline did not end the process, the call took %s", time.Since(start))
	}
	if !strings.Contains(err.Error(), "did not answer in time") {
		t.Fatalf("want the deadline named, got %v", err)
	}
}

// What a list command prints past the cap is no list: it is refused instead
// of read as one cut in the middle. A command that fails without printing
// anything answers what it said on standard error.
func TestRunListCommandRefusesAnOutputPastTheCap(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh on this host")
	}
	if _, err := RunListCommand(context.Background(), 8, "sh", "-c", "echo 0123456789"); err == nil || !strings.Contains(err.Error(), "printed more than a list") {
		t.Fatalf("want an output past the cap refused, got %v", err)
	}
	if _, err := RunListCommand(context.Background(), 1<<20, "sh", "-c", "echo not logged in >&2; exit 3"); err == nil || err.Error() != "not logged in" {
		t.Fatalf("want the command's own words of a failure, got %v", err)
	}
	if out, err := RunListCommand(context.Background(), 1<<20, "sh", "-c", "echo one; echo two"); err != nil || out != "one\ntwo\n" {
		t.Fatalf("want what the command printed, got %q and %v", out, err)
	}
}
