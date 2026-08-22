package docker

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// A compose run outlives the server that started it. Its process is detached
// and writes its output into a file of its own, so an up that pulls an image
// keeps going while the cockpit restarts, and the next process picks it up
// where it stands. What ties the two together is this register: one entry per
// compose run, on disk, next to the files it describes.
//
// Everything a later process needs to finish a run it never started lives in
// the entry: where it runs, which project it reports under, what it runs, how
// long it may take and which process is doing it. The files are derived from
// the id, the way the assistant's runs are laid out.
//
// A finished run stays in the register instead of going with its output,
// because the output is what somebody reads afterwards: the entry then carries
// how it ended, and only the newest few are kept.

// keptRuns is how many finished runs the register holds on to. The output of
// the last handful is what a person looks at; older ones are disk.
const keptRuns = 20

// ComposeRecord is one compose run as it exists outside this process.
type ComposeRecord struct {
	ID string `json:"id"`
	// Dir is the compose directory, which is also what a second run in the
	// same place is refused against.
	Dir string `json:"dir"`
	// Label is the name the run reports under, the project it belongs to.
	Label string `json:"label"`
	// Action is the label of the configured entry that started it, the word
	// every surface says about the run.
	Action string `json:"action"`
	// Argv is what really ran, kept so the output view can show the line.
	Argv []string `json:"argv"`
	// Timeout is what the hold process enforces, kept so a run that hit it can
	// say after how long.
	Timeout time.Duration `json:"timeout"`
	// Quiet keeps a run that went through silent, see ComposeOptions.
	Quiet bool `json:"quiet,omitempty"`
	// PID is the hold process. Whether the run is still going is decided by
	// its lock file alone.
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"startedAt"`
	// Cancelled marks a run somebody called off, so the end reads as that and
	// not as a run that ended without a result.
	Cancelled bool `json:"cancelled,omitempty"`
	// Finished and everything below it are written once, when the run is over
	// and has been reported. An entry carrying it is never taken up again.
	Finished bool      `json:"finished,omitempty"`
	EndedAt  time.Time `json:"endedAt,omitempty"`
	// Exited says the run wrote an exit code at all; Exit is that code. A run
	// without one did not end by its own decision.
	Exited  bool   `json:"exited,omitempty"`
	Exit    int    `json:"exit,omitempty"`
	Failure string `json:"failure,omitempty"`
}

// runStore persists the register. One file, read through on every call like
// every other state file, so the entry a dying process wrote is the entry the
// next one finds.
type runStore struct {
	path string
	dir  string
	mu   sync.Mutex
}

// runPaths returns the register and the directory of run files for a state
// directory.
func runPaths(stateDir string) (index, dir string) {
	root := filepath.Join(stateDir, "docker")
	return filepath.Join(root, "runs.json"), filepath.Join(root, "runs")
}

func newRunStore(stateDir string) *runStore {
	index, dir := runPaths(stateDir)
	return &runStore{path: index, dir: dir}
}

// List returns every registered run.
func (s *runStore) List() []ComposeRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

// Get answers one entry.
func (s *runStore) Get(id string) (ComposeRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, r := range s.load() {
		if r.ID == id {
			return r, true
		}
	}
	return ComposeRecord{}, false
}

func (s *runStore) load() []ComposeRecord {
	var out []ComposeRecord
	statefile.Load(s.path, &out)
	return out
}

// Save writes one entry, replacing the one with the same id.
func (s *runStore) Save(r ComposeRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.save(r)
}

func (s *runStore) save(r ComposeRecord) {
	list := s.load()
	for i := range list {
		if list[i].ID == r.ID {
			list[i] = r
			statefile.Save(s.path, 0o600, list)
			return
		}
	}
	statefile.Save(s.path, 0o600, append(list, r))
}

// Finish writes the entry of a run that is over and drops what is too old to
// keep. The output file stays, that is what the entry is for now; the lock and
// the result are what the run needed while it ran.
func (s *runStore) Finish(r ComposeRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.save(r)
	_, lock, result := s.paths(r.ID)
	remove(lock)
	remove(result)
	s.trim()
}

// trim keeps the newest finished runs and takes the rest with their files.
// Running entries are never counted or dropped.
func (s *runStore) trim() {
	list := s.load()
	var finished []ComposeRecord
	for _, r := range list {
		if r.Finished {
			finished = append(finished, r)
		}
	}
	if len(finished) <= keptRuns {
		return
	}
	sort.Slice(finished, func(i, j int) bool { return finished[i].EndedAt.After(finished[j].EndedAt) })
	drop := map[string]bool{}
	for _, r := range finished[keptRuns:] {
		drop[r.ID] = true
	}
	kept := list[:0]
	for _, r := range list {
		if drop[r.ID] {
			continue
		}
		kept = append(kept, r)
	}
	statefile.Save(s.path, 0o600, kept)
	for id := range drop {
		out, lock, result := s.paths(id)
		remove(out)
		remove(lock)
		remove(result)
	}
}

// Delete removes one entry and the files it describes.
func (s *runStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	kept := list[:0]
	for _, r := range list {
		if r.ID == id {
			continue
		}
		kept = append(kept, r)
	}
	statefile.Save(s.path, 0o600, kept)
	out, lock, result := s.paths(id)
	remove(out)
	remove(lock)
	remove(result)
}

// Files returns the paths a new run lives in and makes sure they have a place.
// A run whose output cannot be written must not be started at all.
func (s *runStore) Files(id string) (out, lock, result string, err error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", "", "", err
	}
	out, lock, result = s.paths(id)
	return out, lock, result, nil
}

// paths is where one run's files lie. They follow from the id, so the register
// carries the run and not a copy of the layout.
func (s *runStore) paths(id string) (out, lock, result string) {
	base := filepath.Join(s.dir, id)
	return base + ".out", base + ".lock", base + ".result"
}

// Sweep removes files no entry points at any more. A process killed between
// writing a file and registering the run would otherwise leave it behind
// forever.
func (s *runStore) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	known := map[string]bool{}
	for _, r := range s.load() {
		out, lock, result := s.paths(r.ID)
		known[filepath.Base(out)] = true
		known[filepath.Base(lock)] = true
		known[filepath.Base(result)] = true
	}
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() || known[entry.Name()] {
			continue
		}
		remove(filepath.Join(s.dir, entry.Name()))
	}
}

func remove(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("docker: remove %s: %v", path, err)
	}
}
