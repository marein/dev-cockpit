package assistant

import (
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/local/dev-cockpit/internal/statefile"
)

// A turn outlives the server that started it. Its process is detached and
// writes the provider's raw output into a file of its own, so the answer keeps
// being written while the cockpit restarts, and the next process picks the file
// up where it stands. What ties the two together is this register: one entry per
// running turn, on disk, next to the file it describes.
//
// Everything a later process needs to finish a turn it never started lives in
// the entry: which conversation and message it belongs to, which coder has to
// parse its output, where the output is, which process is writing it, and what
// the turn is allowed to do while it runs.

// RunKind separates the two turns that exist. They differ in what finishing
// means: a chat turn writes into the message it already has, a check hands its
// verdict to the watcher, which decides whether the user hears about it at all.
type RunKind string

const (
	// RunChat is a turn the user asked for.
	RunChat RunKind = "chat"
	// RunCheck is a turn a steered job asked for.
	RunCheck RunKind = "check"
)

// RunRecord is one turn as it exists outside this process.
type RunRecord struct {
	ID   string  `json:"id"`
	Kind RunKind `json:"kind"`
	// Conversation and MessageID name what the turn writes into. A chat turn
	// has its placeholder message already and names the conversation it belongs
	// to; a check carries no conversation, only the id its report will get, so
	// writing that report twice is impossible and it lands wherever the user is
	// when it comes back.
	Conversation string `json:"conversation,omitempty"`
	MessageID    string `json:"messageId"`
	// CoderID picks the runner that can read this output, SessionID the
	// provider session the turn drives. Both are needed to attach: the parser
	// belongs to the coder, and a check keeps its session reserved until it is
	// over.
	CoderID   string `json:"coderId"`
	SessionID string `json:"sessionId"`
	// Terminal and Context are what a check needs to be concluded by whoever
	// finds it: the terminal its job steers, and what the watcher saw before it
	// started.
	Terminal string       `json:"terminal,omitempty"`
	Context  checkContext `json:"context,omitempty"`
	// Output is the provider's raw output, Errors its standard error.
	Output string `json:"output"`
	Errors string `json:"errors"`
	// PID is the turn process, the one that holds the turn's lock while the
	// provider runs. It is what a kill signals and what a self update reaps,
	// but whether the turn still runs is decided by Lock alone.
	PID int `json:"pid"`
	// Lock is the file whose exclusive lock the turn process inherited before it
	// started. As long as something holds it, the turn is running.
	Lock string `json:"lock,omitempty"`
	// Processed is how far the output file was turned into events. A file that
	// is shorter than this was replaced under a running turn, and then it is not
	// the answer to this prompt any more.
	Processed int64     `json:"processed"`
	StartedAt time.Time `json:"startedAt"`
	// Deadline is when this turn is killed. A check has one, a chat turn runs
	// as long as the user lets it.
	Deadline time.Time `json:"deadline,omitempty"`
	// Cancelled marks a turn the user stopped. It is written before the process
	// is killed, so a stop that races a restart is still read as a stop and not
	// as a coder that died.
	Cancelled bool `json:"cancelled,omitempty"`
}

// RunStore persists the register. One file, read through on every call like
// every other state file, so the entry a dying process wrote is the entry the
// next one finds.
type RunStore struct {
	path string
	dir  string
	mu   sync.Mutex
}

// RunPaths returns the register and the directory of raw output files for a
// state directory.
func RunPaths(stateDir string) (index, dir string) {
	root := filepath.Join(stateDir, "assistant")
	return filepath.Join(root, "runs.json"), filepath.Join(root, "runs")
}

// NewRunStore returns the register for a state directory.
func NewRunStore(stateDir string) *RunStore {
	index, dir := RunPaths(stateDir)
	return NewRunStoreAt(index, dir)
}

// NewRunStoreAt returns a register over explicit paths, so the layout stays in
// one place.
func NewRunStoreAt(index, dir string) *RunStore {
	return &RunStore{path: index, dir: dir}
}

// List returns every registered turn.
func (s *RunStore) List() []RunRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *RunStore) load() []RunRecord {
	var out []RunRecord
	statefile.Load(s.path, &out)
	return out
}

// Save writes one entry, replacing the one with the same id.
func (s *RunStore) Save(r RunRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

// Update changes one entry in place, on the copy that is on disk right now. Two
// writers touch a running turn, the reader that records its progress and a stop
// that marks it cancelled, and neither may put the other's field back.
func (s *RunStore) Update(id string, change func(*RunRecord)) (RunRecord, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	for i := range list {
		if list[i].ID != id {
			continue
		}
		change(&list[i])
		statefile.Save(s.path, 0o600, list)
		return list[i], true
	}
	return RunRecord{}, false
}

// Delete removes one entry and the raw output it describes. The transcript
// holds the answer by then, and a file nobody reads any more is only disk.
func (s *RunStore) Delete(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	kept := list[:0]
	for _, r := range list {
		if r.ID == id {
			remove(r.Output)
			remove(r.Errors)
			remove(r.Lock)
			continue
		}
		kept = append(kept, r)
	}
	statefile.Save(s.path, 0o600, kept)
}

// Files returns the paths a new turn lives in: its output, its standard
// error, and its lock. The directory is created here, because a turn that
// cannot write its output must not be started at all.
func (s *RunStore) Files(id string) (output, errors, lock string, err error) {
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", "", "", err
	}
	return filepath.Join(s.dir, id+".out"), filepath.Join(s.dir, id+".err"), filepath.Join(s.dir, id+".lock"), nil
}

// Sweep removes raw output files no entry points at any more. A process killed
// between writing the file and registering it would otherwise leave it behind
// forever.
func (s *RunStore) Sweep() {
	s.mu.Lock()
	defer s.mu.Unlock()
	known := map[string]bool{}
	for _, r := range s.load() {
		known[filepath.Base(r.Output)] = true
		known[filepath.Base(r.Errors)] = true
		known[filepath.Base(r.Lock)] = true
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
		log.Printf("assistant: remove %s: %v", path, err)
	}
}

// errTruncatedOutput is what a shorter than expected output file means: the
// answer this turn was producing is not in that file any more.
var errTruncatedOutput = errors.New("The answer of this turn was lost while the cockpit restarted.")
