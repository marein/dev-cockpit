package coder

import (
	"errors"
	"testing"

	"github.com/local/dev-cockpit/internal/config"
	"github.com/local/dev-cockpit/internal/tmux"
)

// The fallback is for a coder that keeps no record of its sessions: then the
// screen is the only source, and a picture that did not change is the only
// evidence that nothing is happening.
func TestScreenActivityReadsTheTerminalSeveralTimes(t *testing.T) {
	reads := 0
	still, err := screenActivity(func() (string, error) {
		reads++
		return "waiting for input", nil
	}, 0)
	if err != nil {
		t.Fatalf("screenActivity: %v", err)
	}
	if reads != screenSettleReads {
		t.Fatalf("want the terminal read %d times, got %d", screenSettleReads, reads)
	}
	if !still.Finished {
		t.Fatal("a picture that did not change is a coder that is not working")
	}
	if !still.Screen || still.Text != "waiting for input" {
		t.Fatalf("want the picture marked as a screen, got %+v", still)
	}

	moving, err := screenActivity(func() (string, error) {
		reads++
		return "working " + string(rune('0'+reads)), nil
	}, 0)
	if err != nil {
		t.Fatalf("screenActivity: %v", err)
	}
	if moving.Finished {
		t.Fatal("a picture that changed is a coder that works")
	}

	// The failure this window exists for: a coder inside a long tool call that
	// redraws once. One redraw anywhere in the window means it is working.
	step := 0
	late, err := screenActivity(func() (string, error) {
		step++
		if step < screenSettleReads {
			return "same picture", nil
		}
		return "finally something else", nil
	}, 0)
	if err != nil {
		t.Fatalf("screenActivity: %v", err)
	}
	if late.Finished {
		t.Fatal("a coder that redrew late in the window is working, not idle")
	}
}

// A terminal that cannot be read at all is an error the caller reports. One that
// stops answering between the two reads is not working either, and what it last
// showed still says where the job stands.
func TestScreenActivityHandlesATerminalThatGoesAway(t *testing.T) {
	if _, err := screenActivity(func() (string, error) {
		return "", errors.New("no such session")
	}, 0); err == nil {
		t.Fatal("want the error of an unreadable terminal")
	}

	reads := 0
	activity, err := screenActivity(func() (string, error) {
		reads++
		if reads == 1 {
			return "the last thing it showed", nil
		}
		return "", errors.New("no such session")
	}, 0)
	if err != nil {
		t.Fatalf("screenActivity: %v", err)
	}
	if !activity.Finished || activity.Text != "the last thing it showed" {
		t.Fatalf("want the first picture and a coder that is not working, got %+v", activity)
	}
}

// stubCoder is a coder whose sessions never answer, so the manager has to decide
// what that means. It carries an empty session store, which is what a manager
// needs to build a snapshot.
type stubCoder struct {
	Coder
	err error
}

func (c stubCoder) ID() string                                         { return "stub" }
func (c stubCoder) SessionRepository() SessionRepository               { return emptySessions{} }
func (c stubCoder) SessionActivity(string, int, int) (Activity, error) { return Activity{}, c.err }

type emptySessions struct{ SessionRepository }

func (emptySessions) List() []Session { return nil }

// A coder that cannot answer for a session nobody is running is a coder that is
// gone, and the caller has to hear that. The other way round, a session that is
// running but has recorded nothing yet, must not reach the caller as an error:
// the watcher reads an error as a coder that is gone, and a gone coder counts as
// idle, so a job checked in the first seconds of its coder would be reported as
// standing still while the coder is still coming up.
func TestAManagerPassesOnWhatACoderCannotAnswer(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_TMPDIR", t.TempDir())
	m := NewManager(config.Config{}, tmux.New(), stubCoder{err: errors.New("This session has no transcript to read.")}, nil)
	if _, err := m.Activity("11111111-1111-4111-8111-111111111111", 0, ActivityBudget); err == nil {
		t.Fatal("want the coder's own error for a session nobody is running")
	}
}
