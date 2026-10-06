package opencode

import (
	"errors"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/config"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// opencode's /rename takes no name and opens a dialog, so the cockpit types
// nothing: the entry only points the user to the command inside the coder,
// and a POST that still arrives is refused before anything reaches the pane.
func TestOpenCodeIsRenamedOnlyInside(t *testing.T) {
	var c coder.Coder = &Coder{}
	if mode := coder.RenameMode(c); mode != coder.RenameManual {
		t.Fatalf("mode = %q, want %q", mode, coder.RenameManual)
	}
	t.Setenv("PATH", t.TempDir())
	m := coder.NewManager(config.Config{}, tmux.New(), c, project.NewRepository(t.TempDir(), nil))
	if _, err := m.Rename("abc", "new"); !errors.Is(err, coder.ErrRenameUnsupported) {
		t.Fatalf("want ErrRenameUnsupported, got %v", err)
	}
}
