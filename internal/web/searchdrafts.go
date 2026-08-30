package web

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// searchDrafts keeps the editor palette's inputs per project: what was searched
// for, what it would be replaced with, and what the search stands on. Somebody
// jumps into a hit, reads, comes back and jumps into the next one, and the
// palette is where they left it; a phone and a desktop share the same one. It
// is the commit panel's draft with a different subject, and it is built the
// same way: one JSON state file for every project, read through on every call,
// the lock only ordering this process's read-modify-writes.
type searchDrafts struct {
	mu   sync.Mutex
	path string
}

// searchDraft is one project's palette. A cleared draft keeps its timestamp:
// the other devices decide by it, and a cleared draft without one would look
// older than what they still hold. The mode is deliberately not in here, it
// belongs to the way the palette was opened.
type searchDraft struct {
	Query     string    `json:"query,omitempty"`
	Replace   string    `json:"replace,omitempty"`
	Folder    string    `json:"folder,omitempty"`
	Mask      string    `json:"mask,omitempty"`
	Regex     bool      `json:"regex,omitempty"`
	Case      bool      `json:"case,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func newSearchDrafts(stateDir string) *searchDrafts {
	return &searchDrafts{path: filepath.Join(stateDir, "editor-search-drafts.json")}
}

func (d *searchDrafts) load() map[string]searchDraft {
	m := map[string]searchDraft{}
	statefile.Load(d.path, &m)
	return m
}

func (d *searchDrafts) store(m map[string]searchDraft) {
	statefile.Save(d.path, 0o644, m)
}

// Get answers the stored draft; a project that never had one answers empty
// with a zero time, which is what tells a client to leave its own state alone.
func (d *searchDrafts) Get(project string) searchDraft {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.load()[project]
}

// Save stores what the palette holds and says whether that changed anything,
// so the pause after a burst of typing writes nothing when nothing moved.
func (d *searchDrafts) Save(project string, next searchDraft) (searchDraft, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.load()
	cur := m[project]
	if cur.Query == next.Query && cur.Replace == next.Replace &&
		cur.Folder == next.Folder && cur.Mask == next.Mask &&
		cur.Regex == next.Regex && cur.Case == next.Case {
		return cur, false
	}
	next.UpdatedAt = time.Now().UTC()
	m[project] = next
	d.store(m)
	return next, true
}

// Delete drops a project's entry entirely: the project is gone and nobody is
// left to pull an emptied palette of it.
func (d *searchDrafts) Delete(project string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	m := d.load()
	if _, ok := m[project]; !ok {
		return
	}
	delete(m, project)
	d.store(m)
}
