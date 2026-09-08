package coder

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// The name is optional: a coder started without one has its CLI title the
// session. So a blank name must reach the session start like any other, which
// is what the tmux failure proves, and it must not be refused before it.
func TestASessionStartsWithoutAName(t *testing.T) {
	withoutTmux(t)
	root := t.TempDir()
	workdir := filepath.Join(root, "unnamed-project")
	if err := os.MkdirAll(workdir, 0o755); err != nil {
		t.Fatal(err)
	}
	rt := &recordingRuntime{}
	m := NewManager(config.Config{}, tmux.New(), trustCoder{runtime: rt}, project.NewRepository(root, nil))

	_, err := m.Start("   ", workdir, "", StartOptions{})
	if err == nil {
		t.Fatal("want the session start to fail with no tmux on PATH")
	}
	if strings.Contains(strings.ToLower(err.Error()), "name") {
		t.Fatalf("a missing name must not refuse the start: %v", err)
	}
	if len(rt.trusted) != 1 || rt.trusted[0] != workdir {
		t.Fatalf("trusted = %v, want the start reached with [%s]", rt.trusted, workdir)
	}
}
