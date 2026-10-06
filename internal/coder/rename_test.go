package coder

import (
	"errors"
	"testing"

	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// The name is typed into the coder as one command. A line break in it would
// submit the command early and hand the rest to the model as a prompt.
func TestASessionNameStaysOneLine(t *testing.T) {
	cases := map[string]string{
		"  plain  ":            "plain",
		"two\nlines":           "two lines",
		"tab\there\r\nand\x1b": "tab here and",
		"\n\t ":                "",
	}
	for raw, want := range cases {
		if got := cleanSessionName(raw); got != want {
			t.Errorf("cleanSessionName(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestACoderWithoutTheCapabilityRefusesARename(t *testing.T) {
	withoutTmux(t)
	m := NewManager(config.Config{}, tmux.New(), trustCoder{}, project.NewRepository(t.TempDir(), nil))
	if mode := RenameMode(m.Coder()); mode != "" {
		t.Fatalf("a coder without a rename capability answers mode %q", mode)
	}
	if _, err := m.Rename("abc", "new"); !errors.Is(err, ErrRenameUnsupported) {
		t.Fatalf("want ErrRenameUnsupported, got %v", err)
	}
}
