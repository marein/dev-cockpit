package notify

import (
	"path/filepath"
	"testing"
)

func testService(t *testing.T) *Service {
	t.Helper()
	return NewService(filepath.Join(t.TempDir(), "notifications.json"), nil)
}

// collect subscribes and returns what the service published, so a test can read
// what the browser would have been told.
func collect(t *testing.T, s *Service) func() []Event {
	t.Helper()
	events, cancel := s.Subscribe()
	t.Cleanup(cancel)
	return func() []Event {
		var out []Event
		for {
			select {
			case ev := <-events:
				out = append(out, ev)
			default:
				return out
			}
		}
	}
}

// News from a target somebody else is already looking at is written down and
// stays quiet: it is in the list for the history, and nothing about it rings.
func TestASilentEntryIsWrittenReadAndRingsNowhere(t *testing.T) {
	s := testService(t)
	s.SetSilent(func(targetID string) bool { return targetID == "steered" })
	published := collect(t, s)

	s.Add("steered")

	list := s.List(0)
	if len(list) != 1 {
		t.Fatalf("want the entry in the list, got %d", len(list))
	}
	if !list[0].Read || !list[0].Silent {
		t.Fatalf("want a silent entry that is already read, got %+v", list[0])
	}
	if s.UnreadCount() != 0 {
		t.Fatalf("want no unread, got %d", s.UnreadCount())
	}
	if s.UnreadTargets()["steered"] {
		t.Fatal("want no news mark on a steered target")
	}
	events := published()
	if len(events) != 1 {
		t.Fatalf("want one event, got %d", len(events))
	}
	if events[0].Added != nil {
		t.Fatalf("want no Added, that is the toast and the push, got %+v", events[0].Added)
	}
}

// A steered coder that reports ten times leaves one line, the newest. The
// silent entries are history, and ten of them would push the real history off
// the end of the stored list.
func TestSilentEntriesOfOneTargetCollapse(t *testing.T) {
	s := testService(t)
	s.SetSilent(func(targetID string) bool { return targetID == "steered" })

	s.Add("other")
	for i := 0; i < 10; i++ {
		s.Add("steered")
	}

	list := s.List(0)
	silent := 0
	for _, n := range list {
		if n.TargetID == "steered" {
			silent++
		}
	}
	if silent != 1 {
		t.Fatalf("want one silent line for the target, got %d", silent)
	}
	if list[0].TargetID != "steered" {
		t.Fatalf("want the newest silent line first, got %+v", list[0])
	}
	if len(list) != 2 {
		t.Fatalf("want the other target's entry kept, got %+v", list)
	}
}

// A silent entry never touches what the user still has to read: the unread
// entry of the same target stays unread and keeps counting.
func TestASilentEntryLeavesAnUnreadEntryAlone(t *testing.T) {
	s := testService(t)
	steered := false
	s.SetSilent(func(targetID string) bool { return steered })

	s.Add("term-1")
	steered = true
	s.Add("term-1")

	if s.UnreadCount() != 1 {
		t.Fatalf("want the earlier news still unread, got %d", s.UnreadCount())
	}
	if !s.UnreadTargets()["term-1"] {
		t.Fatal("want the target still marked")
	}
	if len(s.List(0)) != 2 {
		t.Fatalf("want both entries stored, got %+v", s.List(0))
	}
}

// A target nobody steers behaves exactly as it always did: unread, with the
// Added the toast, the jingle and the push channels act on, and a target holds
// at most one unread entry.
func TestALoudEntryIsUnchanged(t *testing.T) {
	s := testService(t)
	s.SetSilent(func(targetID string) bool { return false })
	published := collect(t, s)

	s.Add("term-1")
	s.Add("term-1")

	list := s.List(0)
	if len(list) != 1 {
		t.Fatalf("want the follow-up swallowed, got %d entries", len(list))
	}
	if list[0].Read || list[0].Silent {
		t.Fatalf("want a normal unread entry, got %+v", list[0])
	}
	if s.UnreadCount() != 1 {
		t.Fatalf("want one unread, got %d", s.UnreadCount())
	}
	events := published()
	if len(events) != 1 || events[0].Added == nil {
		t.Fatalf("want one event carrying Added, got %+v", events)
	}
}

// Both lines travel from the resolver into the entry and into the fan-out,
// which is what the list, the toast and the push body read.
func TestBothLinesReachTheEntryAndTheEvent(t *testing.T) {
	s := NewService(filepath.Join(t.TempDir(), "notifications.json"), func(targetID string) TargetInfo {
		return TargetInfo{Name: "Assistant", Title: "Assistant answered.", Detail: "The tests pass.", URL: "/projects"}
	})
	published := collect(t, s)

	s.Add("conversation-1")

	list := s.List(0)
	if len(list) != 1 || list[0].Title != "Assistant answered." || list[0].Detail != "The tests pass." {
		t.Fatalf("want both lines stored, got %+v", list)
	}
	events := published()
	if len(events) != 1 || events[0].Added == nil || events[0].Added.Detail != "The tests pass." {
		t.Fatalf("want the lower line in the fan-out, got %+v", events)
	}
}

// The dedupe window is per target, and a compose run's target is its project:
// two projects finishing in the same moment are two entries, while the same
// project's second run inside the window collapses into the one that stands.
func TestComposeNewsIsDedupedPerProject(t *testing.T) {
	s := testService(t)
	s.Add(DockerTarget("one"))
	s.Add(DockerTarget("two"))
	list := s.List(0)
	if len(list) != 2 {
		t.Fatalf("two projects wrote %d entries: %+v", len(list), list)
	}
	if list[0].TargetID != DockerTarget("two") || list[1].TargetID != DockerTarget("one") {
		t.Fatalf("the entries name %q and %q", list[0].TargetID, list[1].TargetID)
	}

	s.Add(DockerTarget("one"))
	if list = s.List(0); len(list) != 2 {
		t.Fatalf("a second run of one project wrote %d entries: %+v", len(list), list)
	}
	if s.UnreadCount() != 2 {
		t.Fatalf("unread answered %d", s.UnreadCount())
	}
}

// A compose target names a project and a run, not a terminal, so the terminal
// restore's prune cannot know it and must not take it.
func TestPruneKeepsComposeNews(t *testing.T) {
	s := testService(t)
	s.Add(DockerTarget("one"))
	s.Add("11111111-1111-4111-8111-111111111111")
	if removed := s.PruneTargets(map[string]bool{}); removed != 1 {
		t.Fatalf("the prune removed %d entries", removed)
	}
	list := s.List(0)
	if len(list) != 1 || !IsDockerTarget(list[0].TargetID) {
		t.Fatalf("the prune left %+v", list)
	}
	if DockerTargetProject(list[0].TargetID) != "one" {
		t.Fatalf("the target lost its project: %q", list[0].TargetID)
	}
}
