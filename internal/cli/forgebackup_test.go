package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/marein/dev-cockpit/internal/backup"
	"github.com/marein/dev-cockpit/internal/forge"
	forgegithub "github.com/marein/dev-cockpit/internal/forge/github"
	forgegitlab "github.com/marein/dev-cockpit/internal/forge/gitlab"
	"github.com/marein/dev-cockpit/internal/project"
)

type forgeHost struct {
	backups *backup.Service
	forges  *forge.Service
	state   string
	root    string
}

func newForgeHost(t *testing.T, projects ...string) forgeHost {
	t.Helper()
	h := forgeHost{state: t.TempDir(), root: t.TempDir()}
	for _, name := range projects {
		if err := os.MkdirAll(filepath.Join(h.root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	h.backups = backup.New(h.state, h.root, "test")
	h.forges = forge.New(h.state, []forge.Provider{forgegithub.Provider{}, forgegitlab.Provider{}}, forgegithub.Builtin(), forgegitlab.Builtin())
	h.backups.SetRestored(keepForgeOverrides(h.forges, project.NewRepository(h.root, nil)))
	return h
}

func (h forgeHost) restore(t *testing.T, archive []byte, sections ...string) {
	t.Helper()
	id, err := h.backups.SavePending(bytes.NewReader(archive), "test-pw")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if _, err := h.backups.Apply(id, sections); err != nil {
		t.Fatalf("apply: %v", err)
	}
}

func (h forgeHost) overrides(t *testing.T, names ...string) map[string]forge.How {
	t.Helper()
	all, err := h.forges.Bindings()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]forge.How{}
	for _, name := range names {
		out[name] = all.Override(name).How
	}
	return out
}

// A restored override of a project this host does not have is dropped, so a
// later project of the same name does not inherit it. A project the same
// import brings along, or one the host already has, keeps its own.
func TestARestoredForgeBindingNeedsItsProject(t *testing.T) {
	src := newForgeHost(t, "app", "lab")
	for name, o := range map[string]forge.Override{
		"app":  {How: forge.ByHand, Host: "gitlab.com", Path: "team/app"},
		"lab":  {How: forge.Off},
		"gone": {How: forge.Off},
	} {
		if err := src.forges.SetOverride(name, o); err != nil {
			t.Fatal(err)
		}
	}
	var archive bytes.Buffer
	enc, err := backup.NewEncryptWriter(&archive, "test-pw")
	if err != nil {
		t.Fatal(err)
	}
	if err := src.backups.Export(enc, []string{"forges", "projects"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}

	with := newForgeHost(t)
	with.restore(t, archive.Bytes(), "forges", "projects")
	if got := with.overrides(t, "app", "lab", "gone"); got["app"] != forge.ByHand || got["lab"] != forge.Off || got["gone"] != forge.ByOrigin {
		t.Fatalf("with the projects: %v", got)
	}

	without := newForgeHost(t, "lab")
	without.restore(t, archive.Bytes(), "forges")
	if got := without.overrides(t, "app", "lab", "gone"); got["app"] != forge.ByOrigin || got["lab"] != forge.Off || got["gone"] != forge.ByOrigin {
		t.Fatalf("without the projects: %v", got)
	}
	if err := os.MkdirAll(filepath.Join(without.root, "app"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := without.overrides(t, "app"); got["app"] != forge.ByOrigin {
		t.Fatalf("a later project of the same name inherited %v", got)
	}
}
