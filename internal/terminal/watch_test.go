package terminal

import (
	"sync"
	"testing"
	"time"
)

// A session stopped and resumed between two ticks keeps its name in the live
// set while the watcher that read it ended with the old session. It is
// started again, or the session would stay unwatched for the rest of its
// life, and a watcher that still runs is left alone.
func TestAWatcherThatEndedIsStartedAgainWhileItsSessionLives(t *testing.T) {
	var mu sync.Mutex
	starts := map[string]int{}
	first := &Watch{stop: make(chan struct{}), ended: make(chan struct{})}
	go RunWatch(5*time.Millisecond, nil, func() map[string]string {
		return map[string]string{"resumed": "r", "steady": "s"}
	}, func(tmuxName, id string) (*Watch, error) {
		mu.Lock()
		defer mu.Unlock()
		starts[tmuxName]++
		if tmuxName == "resumed" && starts[tmuxName] == 1 {
			return first, nil
		}
		return &Watch{stop: make(chan struct{}), ended: make(chan struct{})}, nil
	})
	count := func(name string) int {
		mu.Lock()
		defer mu.Unlock()
		return starts[name]
	}
	deadline := time.Now().Add(2 * time.Second)
	for count("resumed") == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	if count("resumed") != 1 || count("steady") != 1 {
		t.Fatalf("running watchers were started again: %d and %d", count("resumed"), count("steady"))
	}
	close(first.ended)
	for count("resumed") < 2 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(30 * time.Millisecond)
	if count("resumed") != 2 || count("steady") != 1 {
		t.Fatalf("after one watcher ended the starts are %d and %d, want 2 and 1", count("resumed"), count("steady"))
	}
}

// A session the caller announces is watched at once, long before the tick.
func TestAnAnnouncedChangeIsReconciledAtOnce(t *testing.T) {
	changed := make(chan struct{}, 1)
	started := make(chan string, 1)
	go RunWatch(time.Hour, changed, func() map[string]string {
		return map[string]string{"fresh": "f"}
	}, func(tmuxName, id string) (*Watch, error) {
		started <- tmuxName
		return &Watch{stop: make(chan struct{}), ended: make(chan struct{})}, nil
	})
	changed <- struct{}{}
	select {
	case name := <-started:
		if name != "fresh" {
			t.Fatalf("started %q", name)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the announced session waits for the tick")
	}
}
