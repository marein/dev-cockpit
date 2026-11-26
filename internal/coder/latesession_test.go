package coder

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/tmux"
)

type lateCoder struct {
	Coder
	sessions lateSessions
}

func (c lateCoder) ID() string                           { return "late" }
func (c lateCoder) SessionRepository() SessionRepository { return c.sessions }

type lateSessions struct {
	SessionRepository
	byPID map[int]string
}

func (s lateSessions) ProcessSession(pid int) (string, bool) {
	id, ok := s.byPID[pid]
	return id, ok
}

// A coder that writes its record with the first message runs under the key
// the cockpit minted until then. Once the record exists the pane is that
// session: listed under the record's id, on the pane it always had, and the
// record is not offered for a resume beside its own running coder.
func TestAPaneIsTiedToTheRecordItsProcessWrites(t *testing.T) {
	stored := []Session{{SessionID: "record-1", Name: "named by the cli", CWD: "/work/app"}, {SessionID: "other", CWD: "/work/app"}}
	pane := tmux.Pane{Name: "minted-key", PID: "4711", Coder: "late", CoderName: "launch name", CoderDir: "/work/app"}

	t.Run("no record yet", func(t *testing.T) {
		running, inactive := scanRunning([]tmux.Pane{pane}, stored, lateCoder{})
		if len(running) != 1 || running[0].Identifier != "minted-key" || running[0].Name != "launch name" {
			t.Fatalf("running = %+v, want the pane under its minted key", running)
		}
		if len(inactive) != 2 {
			t.Fatalf("inactive = %+v, want both stored sessions", inactive)
		}
	})

	t.Run("the process writes a record", func(t *testing.T) {
		c := lateCoder{sessions: lateSessions{byPID: map[int]string{4711: "record-1"}}}
		running, inactive := scanRunning([]tmux.Pane{pane}, stored, c)
		if len(running) != 1 || running[0].Identifier != "record-1" || running[0].TmuxSession != "minted-key" {
			t.Fatalf("running = %+v, want the record's id on the minted pane", running)
		}
		if len(inactive) != 1 || inactive[0].SessionID != "other" {
			t.Fatalf("inactive = %+v, want the running record left out", inactive)
		}
	})

	t.Run("a record the store does not list ties nothing", func(t *testing.T) {
		c := lateCoder{sessions: lateSessions{byPID: map[int]string{4711: "unlisted"}}}
		running, _ := scanRunning([]tmux.Pane{pane}, stored, c)
		if len(running) != 1 || running[0].Identifier != "minted-key" {
			t.Fatalf("running = %+v, want the pane under its minted key", running)
		}
	})

	t.Run("another coder's pane is never asked", func(t *testing.T) {
		c := lateCoder{sessions: lateSessions{byPID: map[int]string{4711: "record-1"}}}
		foreign := pane
		foreign.Coder = "claude"
		if running, _ := scanRunning([]tmux.Pane{foreign}, stored, c); len(running) != 0 {
			t.Fatalf("running = %+v, want nothing", running)
		}
	})
}

// The promote of such a coder takes the record the pane's own process
// writes, so two sessions started in one project at the same moment each
// land on their own, where the working directory alone would be a guess.
func TestALatePromoteTakesTheProcessRecordOnly(t *testing.T) {
	late := lateSessions{byPID: map[int]string{1: "new-1", 2: "old-1", 3: "unlisted"}}
	candidates := []Session{{SessionID: "new-1", CWD: "/work/app"}, {SessionID: "new-2", CWD: "/work/app"}, {SessionID: "old-1", CWD: "/work/app"}}
	seen := map[string]bool{"old-1": true}

	if got, found := lateTarget(candidates, seen, late, 1); !found || got.SessionID != "new-1" {
		t.Fatalf("got %q found=%v, want the process's own record", got.SessionID, found)
	}
	if _, found := lateTarget(candidates, seen, late, 2); found {
		t.Fatal("a record that stood before the start was taken")
	}
	if _, found := lateTarget(candidates, seen, late, 3); found {
		t.Fatal("a record the store does not list was taken")
	}
	if _, found := lateTarget(candidates, seen, late, 9); found {
		t.Fatal("a process without a record was promoted")
	}
}

// A pane whose session moved onto its record is announced once, a session
// that only started or stayed is not.
func TestAPaneWhoseSessionMovedIsAnnouncedOnce(t *testing.T) {
	first := map[string]Running{"minted-key": {Identifier: "minted-key", TmuxSession: "minted-key"}, "stays": {Identifier: "stays", TmuxSession: "stays"}}
	moved, panes := movedPanes(map[string]string{}, first)
	if len(moved) != 0 {
		t.Fatalf("moved = %+v, want nothing for sessions seen for the first time", moved)
	}
	second := map[string]Running{"record-1": {Identifier: "record-1", TmuxSession: "minted-key", Name: "named"}, "stays": first["stays"]}
	moved, panes = movedPanes(panes, second)
	if len(moved) != 1 || moved[0].Identifier != "record-1" {
		t.Fatalf("moved = %+v, want the session that landed on its record", moved)
	}
	if moved, _ = movedPanes(panes, second); len(moved) != 0 {
		t.Fatalf("moved = %+v, want it announced once", moved)
	}
}

type plainCoder struct{ Coder }

func (plainCoder) SessionRepository() SessionRepository { return nil }

// A session that took its record's id is announced once per process, from
// the key its pane keeps to that id, so a job steered under the key follows
// it. A coder whose records never come late announces nothing, whatever its
// panes are called.
func TestAMoveOntoALateRecordIsAnnouncedOnce(t *testing.T) {
	var got []string
	m := NewManager(config.Config{}, tmux.New(), lateCoder{}, nil)
	m.OnMoved(func(from, to string) { got = append(got, from+">"+to) })
	early := []Running{{Identifier: "minted-key", TmuxSession: "minted-key"}, {Identifier: "stays", TmuxSession: "stays"}}
	m.announceMoves(early)
	if len(got) != 0 {
		t.Fatalf("announced %v before any record", got)
	}
	tied := []Running{{Identifier: "record-1", TmuxSession: "minted-key"}, early[1]}
	m.announceMoves(tied)
	m.announceMoves(tied)
	if strings.Join(got, ",") != "minted-key>record-1" {
		t.Fatalf("announced %v, want the key moving to the record once", got)
	}

	restarted := NewManager(config.Config{}, tmux.New(), lateCoder{}, nil)
	var again []string
	restarted.OnMoved(func(from, to string) { again = append(again, from+">"+to) })
	restarted.announceMoves(tied)
	if strings.Join(again, ",") != "minted-key>record-1" {
		t.Fatalf("a fresh process announced %v, want the tie it found", again)
	}

	plain := NewManager(config.Config{}, tmux.New(), plainCoder{}, nil)
	plain.OnMoved(func(from, to string) { t.Fatalf("a coder without late records announced %s>%s", from, to) })
	plain.announceMoves([]Running{{Identifier: "session", TmuxSession: "legacy-pane"}})
}

// A page opened before a coder took its record's id holds the key, and the
// news it is looking at comes under the id: only a coder that moved and has
// news names its key.
func TestTheStartKeyOfAMovedCoderIsNamedForItsNews(t *testing.T) {
	running := []Running{
		{Identifier: "record-1", TmuxSession: "minted-key"},
		{Identifier: "record-2", TmuxSession: "other-key"},
		{Identifier: "stays", TmuxSession: "stays"},
	}
	got := startKeys(running, []string{"record-1", "stays", "gone"})
	if len(got) != 1 || got["minted-key"] != "record-1" {
		t.Fatalf("start keys = %v, want minted-key for record-1 alone", got)
	}
	if got := startKeys(running, nil); len(got) != 0 {
		t.Fatalf("start keys without news = %v, want none", got)
	}
}
