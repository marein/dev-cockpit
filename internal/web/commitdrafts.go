package web

import (
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// commitDrafts keeps the editor's commit panel per project: the message and
// the picked paths, so another device takes the panel over where this one
// left it. One JSON state file holds every project's draft; reads go through
// the disk on every call like every state file, and the lock only orders this
// process's read-modify-writes.
type commitDrafts struct {
	mu   sync.Mutex
	path string
}

// commitDraft is one project's unsent commit: what stands in the message box,
// which changes are picked, and whether an amend is in progress with the
// borrowed message it holds — Message stays the plain draft the amend
// displaced, so unchecking gives it back on any device. A cleared draft keeps
// its timestamp: the other devices decide by it, and a cleared draft without
// one would look older than what they still hold.
type commitDraft struct {
	Message      string    `json:"message,omitempty"`
	Paths        []string  `json:"paths,omitempty"`
	Amend        bool      `json:"amend,omitempty"`
	AmendMessage string    `json:"amendMessage,omitempty"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

func (d commitDraft) empty() bool {
	return d.Message == "" && len(d.Paths) == 0 && !d.Amend && d.AmendMessage == ""
}

func newCommitDrafts(stateDir string) *commitDrafts {
	return &commitDrafts{path: filepath.Join(stateDir, "editor-commit-drafts.json")}
}

func (d *commitDrafts) load() map[string]commitDraft {
	m := map[string]commitDraft{}
	statefile.Load(d.path, &m)
	return m
}

func (d *commitDrafts) store(m map[string]commitDraft) {
	statefile.Save(d.path, 0o644, m)
}

// Get answers the stored draft; a project that never had one answers empty
// with a zero time.
func (d *commitDrafts) Get(project string) commitDraft {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.load()[project]
}

// Save stores what the panel holds and says whether that changed anything, so
// a repeated save writes nothing and wakes nobody. The paths are kept sorted:
// two devices then agree on a draft regardless of the order their lists
// render in. A borrowed message without its amend is nothing, so it is
// dropped rather than stored.
func (d *commitDrafts) Save(project string, next commitDraft) (commitDraft, bool) {
	paths := slices.Clone(next.Paths)
	slices.Sort(paths)
	paths = slices.Compact(paths)
	if len(paths) == 0 {
		paths = nil
	}
	next.Paths = paths
	if !next.Amend {
		next.AmendMessage = ""
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.load()
	cur := m[project]
	if cur.Message == next.Message && cur.Amend == next.Amend &&
		cur.AmendMessage == next.AmendMessage && slices.Equal(cur.Paths, paths) {
		return cur, false
	}
	next.UpdatedAt = time.Now().UTC()
	m[project] = next
	d.store(m)
	return next, true
}

// Clear empties a project's draft after a commit spent it, and says whether
// there was anything to clear.
func (d *commitDrafts) Clear(project string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.load()
	if cur, ok := m[project]; !ok || cur.empty() {
		return false
	}
	m[project] = commitDraft{UpdatedAt: time.Now().UTC()}
	d.store(m)
	return true
}

// Delete drops a project's entry entirely: the project is gone and nobody is
// left to pull a cleared draft of it.
func (d *commitDrafts) Delete(project string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.load()
	if _, ok := m[project]; !ok {
		return
	}
	delete(m, project)
	d.store(m)
}
