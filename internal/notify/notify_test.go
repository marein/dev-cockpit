package notify

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// A failed run right after a success is not a follow-up: the resolver marks
// such news urgent, the dedupe window lets it through, and it replaces the
// stale success as the target's one unread entry.
func TestUrgentNewsPassesTheDedupeWindow(t *testing.T) {
	urgent := false
	s := NewService(filepath.Join(t.TempDir(), "notifications.json"), func(targetID string) TargetInfo {
		if urgent {
			return TargetInfo{Name: "Compose", Title: "Compose failed.", Urgent: true}
		}
		return TargetInfo{Name: "Compose", Title: "Compose finished."}
	})
	published := collect(t, s)

	s.Add(DockerTarget("one"))
	urgent = true
	s.Add(DockerTarget("one"))

	list := s.List(0)
	if len(list) != 1 || list[0].Title != "Compose failed." {
		t.Fatalf("want the failure as the target's one entry, got %+v", list)
	}
	if list[0].Read {
		t.Fatalf("want the failure unread, got %+v", list[0])
	}
	if s.UnreadCount() != 1 {
		t.Fatalf("want one unread, got %d", s.UnreadCount())
	}
	events := published()
	if len(events) != 2 || events[1].Added == nil || events[1].Added.Title != "Compose failed." {
		t.Fatalf("want the failure to ring with its own Added, got %+v", events)
	}
}

// A failure is stored and fanned out as one, a success and plain news carry
// no mark, and a read failure keeps it: only the unread dot goes, the color of
// what it reported stays.
func TestAFailureIsStoredAsOneAndKeepsItWhenRead(t *testing.T) {
	s := NewService(filepath.Join(t.TempDir(), "notifications.json"), func(targetID string) TargetInfo {
		if targetID == "failing" {
			return TargetInfo{Name: "Backup", Title: "Backup failed.", Failed: true}
		}
		return TargetInfo{Name: "Backup", Title: "Backup ready."}
	})
	published := collect(t, s)

	s.Add("failing")
	s.Add("passing")

	byTarget := map[string]Notification{}
	for _, n := range s.List(0) {
		byTarget[n.TargetID] = n
	}
	if n := byTarget["failing"]; !n.Failed || n.Title != "Backup failed." {
		t.Fatalf("want the failure marked, got %+v", n)
	}
	if n := byTarget["passing"]; n.Failed || n.Title != "Backup ready." {
		t.Fatalf("want the success unmarked, got %+v", n)
	}
	events := published()
	if len(events) != 2 || events[0].Added == nil || !events[0].Added.Failed || events[1].Added == nil || events[1].Added.Failed {
		t.Fatalf("want the toast trigger to carry the mark of each, got %+v", events)
	}
	failed, _ := json.Marshal(byTarget["failing"])
	passed, _ := json.Marshal(byTarget["passing"])
	if !strings.Contains(string(failed), `"failed":true`) || strings.Contains(string(passed), `"failed"`) {
		t.Fatalf("want the mark on the wire for a failure alone, got %s and %s", failed, passed)
	}

	s.MarkAllRead()
	for _, n := range s.List(0) {
		if n.TargetID == "failing" && (!n.Read || !n.Failed) {
			t.Fatalf("want a read failure that is still one, got %+v", n)
		}
	}
}

// An entry an older build wrote has no mark and reads as plain news, with
// everything else it carried intact.
func TestAnEntryOfAnOlderBuildReadsAsNews(t *testing.T) {
	path := filepath.Join(t.TempDir(), "notifications.json")
	old := `[{"id":"n1","targetId":"docker:shop","targetName":"Compose","title":"Compose failed.","detail":"Compose up","project":"shop","url":"/projects","createdAt":"2026-10-01T10:00:00Z","read":false}]`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s := NewService(path, nil)

	list := s.List(0)
	if len(list) != 1 || list[0].Failed || list[0].Title != "Compose failed." || list[0].Detail != "Compose up" || list[0].Read {
		t.Fatalf("want the old entry as it was, unmarked, got %+v", list)
	}
}

// The mark is written with the words and never apart from them, so a failure
// and a success of one target inside the dedupe window leave an entry whose
// color says what its title says, whichever of them stands at the end. A
// failure behind news that did not fail is never swallowed, it replaces that
// entry and rings with its own Added, which is what push and webhooks deliver.
func TestAFailureAndASuccessInsideTheWindowNeverMix(t *testing.T) {
	cases := []struct {
		what        string
		first, then TargetInfo
		want        TargetInfo
		rings       bool
	}{
		{"compose failure after a success",
			TargetInfo{Title: "Compose finished."},
			TargetInfo{Title: "Compose failed.", Failed: true, Urgent: true},
			TargetInfo{Title: "Compose failed.", Failed: true}, true},
		{"compose success after a failure",
			TargetInfo{Title: "Compose failed.", Failed: true, Urgent: true},
			TargetInfo{Title: "Compose finished."},
			TargetInfo{Title: "Compose failed.", Failed: true}, false},
		{"backup failure after a success",
			TargetInfo{Title: "Backup ready."},
			TargetInfo{Title: "Backup failed.", Failed: true},
			TargetInfo{Title: "Backup failed.", Failed: true}, true},
		{"backup success after a failure",
			TargetInfo{Title: "Backup failed.", Failed: true},
			TargetInfo{Title: "Backup ready."},
			TargetInfo{Title: "Backup failed.", Failed: true}, false},
		{"backup failure after a failure",
			TargetInfo{Title: "Backup failed.", Detail: "first", Failed: true},
			TargetInfo{Title: "Backup failed.", Detail: "second", Failed: true},
			TargetInfo{Title: "Backup failed.", Detail: "first", Failed: true}, false},
		{"backup success after a success",
			TargetInfo{Title: "Backup ready.", Detail: "first"},
			TargetInfo{Title: "Backup ready.", Detail: "second"},
			TargetInfo{Title: "Backup ready.", Detail: "first"}, false},
	}
	for _, c := range cases {
		s := testService(t)
		s.AddResolved("target", c.first)
		published := collect(t, s)
		s.AddResolved("target", c.then)
		list := s.List(0)
		if len(list) != 1 || list[0].Read || list[0].Title != c.want.Title || list[0].Detail != c.want.Detail || list[0].Failed != c.want.Failed {
			t.Fatalf("%s: want %q %q failed=%v alone and unread, got %+v", c.what, c.want.Title, c.want.Detail, c.want.Failed, list)
		}
		events := published()
		if !c.rings {
			if len(events) != 0 {
				t.Fatalf("%s: want it swallowed without a word, got %+v", c.what, events)
			}
			continue
		}
		if len(events) != 1 || events[0].Added == nil || events[0].Added.ID != list[0].ID || !events[0].Added.Failed || events[0].Unread != 1 || len(events[0].Targets) != 1 || events[0].Targets[0] != "target" {
			t.Fatalf("%s: want the failure to ring as the target's one unread entry, got %+v", c.what, events)
		}
		if !s.UnreadTargets()["target"] {
			t.Fatalf("%s: want the target unread for the push re-check", c.what)
		}
	}

	// A failure that was read is history: the success after it is a line of
	// its own, and neither takes the other's color.
	s := testService(t)
	s.AddResolved("target", TargetInfo{Title: "Compose failed.", Failed: true})
	s.MarkAllRead()
	s.AddResolved("target", TargetInfo{Title: "Compose finished."})
	list := s.List(0)
	if len(list) != 2 || list[0].Failed || list[0].Read || !list[1].Failed || !list[1].Read {
		t.Fatalf("want an unread success over a read failure, got %+v", list)
	}
}

// A compose target names a project, and a git question's does too. The
// restore pass puts the targets of every existing project into the keep set,
// so the prune is a plain membership check: what belongs to a project that
// still exists stays, what belongs to a deleted one goes with it, like a dead
// terminal's entries do.
func TestPruneDropsTheTargetsOfDeletedProjects(t *testing.T) {
	s := testService(t)
	s.Add(DockerTarget("alive"))
	s.Add(GitPromptTarget("alive"))
	s.Add(DockerTarget("deleted"))
	s.Add(GitPromptTarget("deleted"))
	keep := map[string]bool{
		DockerTarget("alive"):    true,
		GitPromptTarget("alive"): true,
	}
	if removed := s.PruneTargets(keep); removed != 2 {
		t.Fatalf("the prune removed %d entries", removed)
	}
	list := s.List(0)
	if len(list) != 2 {
		t.Fatalf("the prune left %+v", list)
	}
	for _, n := range list {
		if n.TargetID != DockerTarget("alive") && n.TargetID != GitPromptTarget("alive") {
			t.Fatalf("the prune kept %q", n.TargetID)
		}
	}
}

// Every hook a coder drops into the inbox reaches the hook listener as the
// raw fact, under the coder's name and dated by the file's modification time,
// not by its name: claude's hook puts nanoseconds there, opencode's plugin
// milliseconds. A wrapped hook names the terminal it belongs to, which is the
// target even after claude's own session id moved; a bare one is targeted by
// its session id. Only Stop and Notification are news.
func TestEveryInboxHookReachesTheListenerWithItsWriteTime(t *testing.T) {
	s := testService(t)
	heard := map[string]HookEvent{}
	targets := map[string]string{}
	s.SetHook(func(targetID string, ev HookEvent) {
		heard[ev.Name] = ev
		targets[ev.Name] = targetID
	})
	dir := t.TempDir()
	written := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	write := func(name, body string, at time.Time) {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, at, at); err != nil {
			t.Fatal(err)
		}
	}
	write("1792071315017168190-11.json", `{"terminal":"term-1","event":{"session_id":"cleared-2","hook_event_name":"UserPromptSubmit","prompt":"hi"}}`, written)
	write("1792071315017168191-11.json", `{"terminal":"term-1","event":{"session_id":"cleared-2","hook_event_name":"PreToolUse","agent_id":"helper","tool_name":"Bash"}}`, written.Add(time.Second))
	write("1792071315017168192-11.json", `{"terminal":"term-1","event":{"session_id":"cleared-2","hook_event_name":"PreCompact","trigger":"manual"}}`, written.Add(2*time.Second))
	write("1792071315017168193-11.json", `{"terminal":"term-1","event":{"session_id":"cleared-2","hook_event_name":"SessionStart","source":"resume"}}`, written.Add(3*time.Second))
	write("1792071315017168194-11.json", `{"terminal":"term-1","event":{"session_id":"cleared-2","hook_event_name":"Notification","notification_type":"permission_prompt","message":"Claude needs your permission"}}`, written.Add(8*time.Second))
	write("1792071315017-22-0.json", `{"session_id":"term-2","hook_event_name":"Stop","message":""}`, written.Add(20*time.Second))
	s.drainInbox(dir, "claude")

	if len(heard) != 6 {
		t.Fatalf("want every hook heard, got %+v", heard)
	}
	if ev := heard["UserPromptSubmit"]; !ev.At.Equal(written) || ev.Coder != "claude" || ev.Session != "cleared-2" || targets["UserPromptSubmit"] != "term-1" {
		t.Fatalf("a wrapped hook is dated by its file and targets its terminal, got %+v at %s", ev, targets["UserPromptSubmit"])
	}
	if ev := heard["PreToolUse"]; ev.AgentID != "helper" || ev.ToolName != "Bash" {
		t.Fatalf("a subagent's hook names the agent and the tool, got %+v", ev)
	}
	if heard["PreCompact"].Trigger != "manual" || heard["SessionStart"].Source != "resume" {
		t.Fatalf("a compaction carries its trigger and a start its source, got %+v %+v", heard["PreCompact"], heard["SessionStart"])
	}
	if heard["Notification"].NotificationType != "permission_prompt" {
		t.Fatalf("a notification carries its kind, got %+v", heard["Notification"])
	}
	if ev := heard["Stop"]; !ev.At.Equal(written.Add(20*time.Second)) || targets["Stop"] != "term-2" {
		t.Fatalf("a bare hook is targeted by its session and a millisecond name is no time, got %+v at %s", ev, targets["Stop"])
	}
	targetsWithNews := map[string]bool{}
	for _, n := range s.List(0) {
		targetsWithNews[n.TargetID] = true
	}
	if len(targetsWithNews) != 2 || !targetsWithNews["term-1"] || !targetsWithNews["term-2"] {
		t.Fatalf("only Stop and Notification are news, each under its terminal, got %v", targetsWithNews)
	}
}
