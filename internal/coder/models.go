package coder

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/settings"
)

// Model is one name a coder's repository lists, and where it comes from: the
// CLI's own names are what the coder knows to offer, the added ones are what
// somebody typed under Other… on a select and the repository remembered.
type Model struct {
	Name  string
	Added bool
}

// Source is the word a listing marks the name with, cli or added.
func (m Model) Source() string {
	if m.Added {
		return "added"
	}
	return "cli"
}

// ModelRepository is a coder's list of models and the names it was told to
// remember: one list every select and the assistant's `model-list` read, one
// Add every typed name goes through, so a name typed once stands in every
// later list of that coder. It sits on the coder itself and not on its
// conversation runner, because the same list serves a coder session's start
// and an assistant's turn, and a coder without the conversation capability
// still starts sessions.
type ModelRepository interface {
	// List answers the CLI's own names first, then the added ones, each
	// marked with its source. A name that is both is listed once, as the
	// CLI's.
	List() []Model
	// Note is the one line saying where the CLI's own names come from, shown
	// under a select so nobody has to guess whether a name missing from the
	// list exists. Empty where there is nothing to say.
	Note() string
	// Add remembers a name, checked by assistant.CleanModel, and changes
	// nothing for one that is listed already, added or the CLI's own.
	Add(name string) error
	// Delete forgets an added name. A CLI name is refused, it is not the
	// repository's to remove, and a name nobody added is nothing to do.
	Delete(name string) error
	// Exists answers whether List holds the name.
	Exists(name string) bool
}

// ModelKeeper is the optional capability of a coder that keeps a model
// repository, the way it keeps its skills or its agents.
type ModelKeeper interface {
	ModelRepository() ModelRepository
}

// ModelRepositoryFor answers a coder's repository, and an empty one for a
// coder without: it lists nothing, notes nothing and remembers nothing without
// refusing, so a select over it holds the empty entry and the typed way past
// the list, and a save of a typed name stores the pick and forgets the name.
func ModelRepositoryFor(c Coder) ModelRepository {
	if keeper, ok := c.(ModelKeeper); ok && keeper.ModelRepository() != nil {
		return keeper.ModelRepository()
	}
	return noModels{}
}

type noModels struct{}

func (noModels) List() []Model       { return nil }
func (noModels) Note() string        { return "" }
func (noModels) Add(string) error    { return nil }
func (noModels) Delete(string) error { return nil }
func (noModels) Exists(string) bool  { return false }

// WarmModels asks a coder for its list once and drops the answer. It runs
// where a coder is registered for serving, so a repository that fetches the
// CLI's names in the background starts the first fetch at the start and the
// first select somebody opens finds it filled instead of starting the fetch.
// It hangs on the serve start and on no other capability of the coder,
// because the list serves the New coder dialog too, which a coder without the
// conversation capability still gets. A coder without a list is asked nothing.
func WarmModels(c Coder) {
	ModelRepositoryFor(c).List()
}

// NewModelRepository builds the one repository every coder keeps: cli answers
// the CLI's own names fresh on every read, so a list a coder fetches in the
// background is read where it stands, note says where they come from, and the
// added names live in the settings store under the coder's key, one JSON
// array, so every serve process on the state directory sees the same list. A
// nil store keeps them in memory, which is what a test coder gets.
func NewModelRepository(store *settings.Store, coderID, note string, cli func() []string) ModelRepository {
	return &modelRepository{store: store, key: assistant.ModelAddedKey(coderID), note: note, cli: cli}
}

type modelRepository struct {
	store *settings.Store
	key   string
	note  string
	cli   func() []string

	// mu holds a read, modify, write of the added names together, and guards
	// memory, which stands in for the store where there is none.
	mu     sync.Mutex
	memory []string
}

func (r *modelRepository) cliNames() []string {
	if r.cli == nil {
		return nil
	}
	return r.cli()
}

func (r *modelRepository) addedLocked() []string {
	if r.store == nil {
		return slices.Clone(r.memory)
	}
	var names []string
	if raw := r.store.Get(r.key); raw != "" {
		_ = json.Unmarshal([]byte(raw), &names)
	}
	return names
}

func (r *modelRepository) saveLocked(names []string) {
	if r.store == nil {
		r.memory = names
		return
	}
	if len(names) == 0 {
		r.store.Delete(r.key)
		return
	}
	raw, err := json.Marshal(names)
	if err != nil {
		return
	}
	r.store.Set(r.key, string(raw))
}

func (r *modelRepository) listLocked() []Model {
	cli := r.cliNames()
	out := make([]Model, 0, len(cli))
	for _, name := range cli {
		out = append(out, Model{Name: name})
	}
	for _, name := range r.addedLocked() {
		if slices.Contains(cli, name) {
			continue
		}
		out = append(out, Model{Name: name, Added: true})
	}
	return out
}

func (r *modelRepository) List() []Model {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.listLocked()
}

func (r *modelRepository) Note() string { return r.note }

func (r *modelRepository) Exists(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.ContainsFunc(r.listLocked(), func(m Model) bool { return m.Name == name })
}

func (r *modelRepository) Add(raw string) error {
	name, err := assistant.CleanModel(raw)
	if err != nil {
		return err
	}
	if name == "" {
		return errors.New("A model name is needed.")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if slices.ContainsFunc(r.listLocked(), func(m Model) bool { return m.Name == name }) {
		return nil
	}
	r.saveLocked(append(r.addedLocked(), name))
	return nil
}

func (r *modelRepository) Delete(raw string) error {
	name, err := assistant.CleanModel(raw)
	if err != nil {
		return err
	}
	if name == "" {
		return errors.New("A model name is needed.")
	}
	if slices.Contains(r.cliNames(), name) {
		return fmt.Errorf("%s is the CLI's own name and cannot be removed.", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	names := r.addedLocked()
	i := slices.Index(names, name)
	if i < 0 {
		return nil
	}
	r.saveLocked(slices.Delete(names, i, i+1))
	return nil
}
