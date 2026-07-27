package terminalstate

import (
	"errors"
	"testing"

	"github.com/local/dev-cockpit/internal/coder"
)

// fakeCoder is one coder manager as the classifier sees it: a set of live
// sessions and a set of stored ones.
type fakeCoder struct {
	running   map[string]bool
	resumable map[string]bool
}

func (f fakeCoder) Resolve(id string) error {
	if f.running[id] {
		return nil
	}
	return errors.New("No active coder")
}

func (f fakeCoder) ResolveResumable(id string) (coder.Session, error) {
	if f.resumable[id] {
		return coder.Session{SessionID: id}, nil
	}
	return coder.Session{}, errors.New("no inactive coder")
}

type fakeShells struct{ running map[string]bool }

func (f fakeShells) Resolve(id string) error {
	if f.running[id] {
		return nil
	}
	return errors.New("No active shell")
}

func sources() ([]CoderLookup, ShellLookup) {
	claude := fakeCoder{
		running:   map[string]bool{"live-coder": true},
		resumable: map[string]bool{"live-coder": true, "stopped-coder": true},
	}
	copilot := fakeCoder{running: map[string]bool{}, resumable: map[string]bool{"other-stopped": true}}
	return []CoderLookup{claude, copilot}, fakeShells{running: map[string]bool{"live-shell": true}}
}

func TestClassifyAnswersEveryCaseAnIdCanBeIn(t *testing.T) {
	coders, shells := sources()
	for _, tc := range []struct {
		id   string
		want State
	}{
		{"live-coder", Running},
		{"live-shell", Running},
		{"stopped-coder", Resumable},
		// A second coder's stored session counts too, whichever coder holds it.
		{"other-stopped", Resumable},
		{"nothing", Unknown},
	} {
		if got := Classify(tc.id, coders, shells); got != tc.want {
			t.Fatalf("Classify(%q) = %v, want %v", tc.id, got, tc.want)
		}
	}
}

func TestClassifyShellNamesTheGoneShellInsteadOfAnUnknownId(t *testing.T) {
	coders, shells := sources()
	if got := ClassifyShell("nothing", coders, shells); got != ShellGone {
		t.Fatalf("ClassifyShell of an id no shell runs under = %v, want ShellGone", got)
	}
	// What the caller knows only fills the gap. Everything the id itself
	// answers still wins, a stopped coder is not a gone shell.
	if got := ClassifyShell("stopped-coder", coders, shells); got != Resumable {
		t.Fatalf("ClassifyShell of a stopped coder = %v, want Resumable", got)
	}
	if got := ClassifyShell("live-shell", coders, shells); got != Running {
		t.Fatalf("ClassifyShell of a live shell = %v, want Running", got)
	}
}

func TestClassifyWithoutShellsLooksAtTheCodersOnly(t *testing.T) {
	coders, _ := sources()
	if got := Classify("live-coder", coders, nil); got != Running {
		t.Fatalf("Classify without shells = %v, want Running", got)
	}
	if got := Classify("live-shell", coders, nil); got != Unknown {
		t.Fatalf("Classify of a shell id without shells = %v, want Unknown", got)
	}
}
