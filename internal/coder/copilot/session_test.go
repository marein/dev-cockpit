package copilot

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder"
)

func writeWorkspace(t *testing.T, root, id, cwd, name string, withEvents bool) {
	t.Helper()
	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := "id: " + id + "\ncwd: " + cwd + "\n"
	if name != "" {
		yaml += "name: " + name + "\n"
	}
	if err := os.WriteFile(filepath.Join(dir, "workspace.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	if withEvents {
		if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A session copilot has just started carries neither a name nor events, so the
// lists leave it out. The promote is looking for exactly that record, and
// without it the session keeps running under the key the cockpit minted while
// copilot holds its own id and its own title.
func TestOnlyTheCandidatesCarryAFreshSession(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	r := &sessionRepository{stateRoot: root}
	writeWorkspace(t, root, "11111111-1111-4111-8111-111111111111", cwd, "", false)
	writeWorkspace(t, root, "22222222-2222-4222-8222-222222222222", cwd, "worked on", true)

	if listed := r.List(); len(listed) != 1 || listed[0].Name != "worked on" {
		t.Fatalf("List = %v, want the session with events alone", listed)
	}
	candidates := r.CandidateSessions()
	if len(candidates) != 2 {
		t.Fatalf("CandidateSessions = %v, want both", candidates)
	}
	// The manager reaches them through the interface, never through the type.
	var repository coder.SessionRepository = r
	if _, ok := repository.(coder.SessionCandidates); !ok {
		t.Fatal("the repository does not offer the promote's view")
	}
}
