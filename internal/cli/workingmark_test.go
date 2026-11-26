package cli

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/coder"
	coderclaude "github.com/marein/dev-cockpit/internal/coder/claude"
	"github.com/marein/dev-cockpit/internal/notify"
)

type inboxFile struct {
	name, body string
	at         time.Time
}

// drainOnce drops the files into a fresh inbox at once, so one poll drains
// them all, and waits until the poller took them. The poller runs until the
// test binary ends.
func drainOnce(t *testing.T, notifier *notify.Service, coderID string, files ...inboxFile) {
	t.Helper()
	dir := t.TempDir()
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		if err := os.WriteFile(path, []byte(f.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, f.at, f.at); err != nil {
			t.Fatal(err)
		}
	}
	go notifier.RunInbox(dir, coderID, 10*time.Millisecond)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if entries, _ := os.ReadDir(dir); len(entries) == 0 {
			time.Sleep(20 * time.Millisecond)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the inbox was never drained")
}

func wiredTracker(t *testing.T) (*notify.Service, *activity.Tracker) {
	t.Helper()
	notifier := notify.NewService(filepath.Join(t.TempDir(), "notifications.json"), nil)
	tracker := activity.NewTracker()
	wireWorkingMark(notifier, tracker, map[string]coder.TurnSignaler{"claude": &coderclaude.Coder{}})
	return notifier, tracker
}

// opencode's plugin drops TurnStart and Stop a few milliseconds apart when a
// permission is rejected past its grace, and one poll drains both. Every word
// is dated by its file, so the Stop is the newer one and closes the turn, and
// a file name in milliseconds is no time at all. Its ask closes the turn as
// it always did.
func TestAnOpencodeStopRightAfterItsTurnStartClosesTheTurn(t *testing.T) {
	notifier, tracker := wiredTracker(t)
	started := time.Now().Add(-time.Second)
	drainOnce(t, notifier, "opencode",
		inboxFile{"1792083000100-7-0.json", `{"session_id":"ses_1","hook_event_name":"TurnStart","message":""}`, started},
		inboxFile{"1792083000124-7-1.json", `{"session_id":"ses_1","hook_event_name":"Stop","message":""}`, started.Add(24 * time.Millisecond)},
		inboxFile{"1792083000100-8-0.json", `{"session_id":"ses_2","hook_event_name":"TurnStart","message":""}`, started},
		inboxFile{"1792083000124-8-1.json", `{"session_id":"ses_2","hook_event_name":"Notification","message":"Permission requested"}`, started.Add(time.Second)},
	)
	if tracker.Working()["ses_1"] != 0 {
		t.Fatal("the Stop written 24 ms after the TurnStart is the newer word, the turn is over")
	}
	if tracker.Working()["ses_2"] != 0 {
		t.Fatal("an opencode ask closes the turn")
	}
}

// claude does not wait for the hook it fires while text streams, so the last
// one can land a moment after the Stop of its turn. It must not reopen what
// the Stop closed.
func TestALateMessageDisplayDoesNotReopenAFinishedTurn(t *testing.T) {
	notifier, tracker := wiredTracker(t)
	start := time.Now().Add(-2 * time.Second)
	wrap := func(event string) string { return `{"terminal":"term-1","event":` + event + `}` }
	drainOnce(t, notifier, "claude",
		inboxFile{"1-a.json", wrap(`{"session_id":"term-1","hook_event_name":"UserPromptSubmit"}`), start},
		inboxFile{"2-b.json", wrap(`{"session_id":"term-1","hook_event_name":"Stop"}`), start.Add(time.Second)},
		inboxFile{"3-c.json", wrap(`{"session_id":"term-1","hook_event_name":"MessageDisplay","final":true}`), start.Add(time.Second + 2*time.Millisecond)},
	)
	if tracker.Working()["term-1"] != 0 {
		t.Fatal("a MessageDisplay after the Stop reopened the turn")
	}
}

// /clear starts a new conversation under a new session id in the same
// terminal. The wrapped hooks still name the terminal, so the mark follows
// it, and the record watcher reads the new conversation.
func TestTheMarkFollowsTheTerminalAcrossClear(t *testing.T) {
	notifier, tracker := wiredTracker(t)
	start := time.Now().Add(-2 * time.Second)
	wrap := func(event string) string { return `{"terminal":"term-1","event":` + event + `}` }
	drainOnce(t, notifier, "claude",
		inboxFile{"1-a.json", wrap(`{"session_id":"term-1","hook_event_name":"SessionEnd","reason":"clear"}`), start},
		inboxFile{"2-b.json", wrap(`{"session_id":"conv-2","hook_event_name":"SessionStart","source":"clear"}`), start.Add(10 * time.Millisecond)},
		inboxFile{"3-c.json", wrap(`{"session_id":"conv-2","hook_event_name":"UserPromptSubmit"}`), start.Add(time.Second)},
		inboxFile{"4-d.json", wrap(`{"session_id":"term-1","agent_id":"helper","hook_event_name":"PostToolUse"}`), start.Add(2 * time.Second)},
	)
	if tracker.Working()["term-1"] == 0 {
		t.Fatal("the prompt after /clear is the terminal's turn")
	}
	if got := tracker.Record("term-1"); got != "conv-2" {
		t.Fatalf("the record watcher reads %q, want the new conversation", got)
	}
	if tracker.Working()["conv-2"] != 0 {
		t.Fatal("the new conversation's id is no terminal of its own")
	}
}

// A coder without a reading of its own is read by the inbox's own words, as
// before: TurnStart opens, Stop closes.
func TestAnInboxWithoutAReaderSpeaksTheInboxWords(t *testing.T) {
	notifier, tracker := wiredTracker(t)
	drainOnce(t, notifier, "copilot",
		inboxFile{"1-a.json", `{"session_id":"cop-1","hook_event_name":"TurnStart"}`, time.Now().Add(-time.Second)},
	)
	if tracker.Working()["cop-1"] == 0 {
		t.Fatal("TurnStart opens the turn of a coder without a reader")
	}
}

type bellReading struct {
	asked  string
	signal coder.TurnSignal
}

func (b *bellReading) BellSignal(sessionID string) coder.TurnSignal {
	b.asked = sessionID
	return b.signal
}

// A bell is news under the name its coder reads into it: a turn that ended
// closes the working mark, a question sets the turn waiting and goes by the
// name the hooks use for one, which the assistants' triggers hear as a coder
// that asks.
func TestABellIsTheNewsItsCoderReadsIntoIt(t *testing.T) {
	tracker := activity.NewTracker()
	var heard []string
	reader := &bellReading{signal: coder.TurnSignalOver}
	ring := bellNews(reader, tracker, func(targetID, kind string) { heard = append(heard, targetID+" "+kind) })

	tracker.SetTurn("t1", true, false, time.Now().Add(-time.Second))
	if _, working := tracker.Working()["t1"]; !working {
		t.Fatal("an open turn is not working")
	}
	ring("t1")
	if _, working := tracker.Working()["t1"]; working {
		t.Fatal("the bell of a turn that ended left the mark up")
	}
	if reader.asked != "t1" {
		t.Fatalf("the coder was asked about %q", reader.asked)
	}

	tracker.SetTurn("t1", true, true, time.Now())
	reader.signal = coder.TurnSignalWaiting
	ring("t1")
	if _, working := tracker.Working()["t1"]; working {
		t.Fatal("a coder standing at a question is marked as working")
	}
	if len(heard) != 2 || heard[0] != "t1 Bell" || heard[1] != "t1 Notification" {
		t.Fatalf("the news went out as %q", heard)
	}
}
