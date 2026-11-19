package pluginhost

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/marein/dev-cockpit/internal/update"
	"github.com/marein/dev-cockpit/plugin"
)

// PluginsDir answers the external plugins folder of a state directory, one
// directory per plugin id.
func PluginsDir(stateDir string) string { return filepath.Join(stateDir, "external-plugins") }

// enabledPath holds the plugin switches and origins. An external plugin is
// on unless listed, so a dropped in directory runs. An optional compiled
// plugin is off unless listed, so a build that adds one runs nothing new.
func enabledPath(stateDir string) string { return filepath.Join(stateDir, "external-plugins.json") }

type hostState struct {
	Disabled []string           `json:"disabled"`
	Origins  map[string]*origin `json:"origins,omitempty"`
	On       []string           `json:"on,omitempty"`
}

// State is a plugin's state on the Plugins page.
type State string

const (
	StateDisabled State = "disabled"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateCrashed  State = "crashed"
	StateBroken   State = "broken"
)

// Status is one plugin as the Plugins page shows it. Reason says why a
// crashed plugin went down or a broken one is unusable, Tail holds a crashed
// plugin's last output lines. PageTitle is the page title its manifest
// declares, for a compiled plugin the one its last load added, "" for none.
// BuiltIn marks a compiled plugin, Optional one that has a switch.
type Status struct {
	ID          string
	Name        string
	Version     string
	Description string
	PageTitle   string
	State       State
	Reason      string
	Tail        []string
	Origin      Origin
	BuiltIn     bool
	Optional    bool
	order       int
}

// Bounds of what the folder and one plugin may hold.
const (
	maxPlugins     = 64
	maxAssetFiles  = 500
	maxAssetBytes  = 32 << 20
	crashTailLines = 12
)

// scanInterval is how often the plugins folder is read for changes.
var scanInterval = 2 * time.Second

// HostConfig is what a Host serves from. Compiled holds the Serves of the
// compiled plugins always on. Wiring is every compiled pair: the host
// switches the optional ones, and no external plugin may carry one of its
// ids. CreateProject and ProjectChanged are the web UI's paths the callback
// API delegates to, Assistants the assistant surface. CSRFField is the form field a proxied request never
// forwards. PluginAPI is a dev build's release stub, see APIURLEnv,
// CheckEvery the update check cadence, 0 for the default.
type HostConfig struct {
	StateDir       string
	ProjectsDir    string
	Compiled       []*Serve
	Wiring         []plugin.Named[plugin.ServePlugin]
	CreateProject  func(ctx context.Context, name string) (string, error)
	ProjectChanged func(ctx context.Context) error
	Assistants     *Assistants
	CSRFField      string
	PluginAPI      string
	CheckEvery     time.Duration
}

// Hooks are the cockpit's reactions to plugins loading and leaving. Prepare
// puts a plugin's assets through the asset pipeline while their directory
// is open, an error marks the plugin broken. Release drops them, Loaded
// applies settings and memory, Changed announces that the set changed.
type Hooks struct {
	Prepare func(*Serve) error
	Release func(id string)
	Loaded  func(*Serve)
	Changed func()
}

// Host runs the external plugins of one serving cockpit. It follows the
// plugins folder, builds each enabled plugin's Serve through the Adds a
// compiled plugin calls, supervises its process and installs and updates
// plugins from repository releases. It also switches the optional compiled
// plugins. Changes hold ops, which may wait for a process to end but never
// for a download. Readers take only mu, so a render never waits on a plugin.
type Host struct {
	cfg      HostConfig
	projects *projects
	compiled map[string]bool
	optional []*optional
	client   *http.Client

	ops      sync.Mutex
	checking sync.Mutex

	mu      sync.Mutex
	entries map[string]*entry
	hooks   Hooks
	started bool
	offers  map[string]*Offer
	feeds   map[string]*update.Feed
	checks  map[string]checkInfo
	busy    map[string]string
	refused map[string]string
	trials  map[string]string
	resume  [][2]string

	boot     string
	version  atomic.Int64
	gen      atomic.Int64
	stop     chan struct{}
	stopOnce sync.Once
	watching sync.WaitGroup
}

// entry is one directory of the plugins folder.
type entry struct {
	id       string
	sig      string
	manifest *Manifest
	problem  string
	serve    *Serve
	roots    []*os.Root
	sup      *supervisor
}

// NewHost builds a Host. Nothing is read or started before Sweep, Scan and
// Start.
func NewHost(cfg HostConfig) *Host {
	compiled := map[string]bool{}
	for _, s := range cfg.Compiled {
		compiled[s.ID()] = true
	}
	var optionals []*optional
	for i, p := range cfg.Wiring {
		compiled[p.ID] = true
		if p.Optional {
			optionals = append(optionals, &optional{order: i, id: p.ID, plugin: p.Plugin})
		}
	}
	return &Host{
		cfg:      cfg,
		projects: &projects{create: cfg.CreateProject, changed: cfg.ProjectChanged},
		compiled: compiled,
		optional: optionals,
		client:   fetchClient(cfg.PluginAPI),
		entries:  map[string]*entry{},
		offers:   map[string]*Offer{},
		feeds:    map[string]*update.Feed{},
		checks:   map[string]checkInfo{},
		busy:     map[string]string{},
		refused:  map[string]string{},
		trials:   map[string]string{},
		boot:     randomHex(4),
		stop:     make(chan struct{}),
	}
}

// Dir answers the plugins folder.
func (h *Host) Dir() string { return PluginsDir(h.cfg.StateDir) }

// Version names the state of the external plugins, see Set.Version.
func (h *Host) Version() string {
	return h.boot + "-" + strconv.FormatInt(h.version.Load(), 10)
}

// Scan completes or undoes an install or update an earlier cockpit left
// unfinished, builds the Serve of every enabled external plugin without
// starting it and configures the optional compiled plugins switched on. It
// runs at serve start before the settings store is read, so their settings
// and memory apply at the same point as those of the plugins always on.
func (h *Host) Scan() {
	h.ops.Lock()
	defer h.ops.Unlock()
	h.recover()
	h.scanLocked()
	h.startOptional()
}

// Start sets the hooks, prepares and starts every plugin Scan loaded, then
// follows the folder and checks for updates.
func (h *Host) Start(hooks Hooks) {
	h.ops.Lock()
	h.mu.Lock()
	h.hooks = hooks
	h.started = true
	var loaded []*entry
	for _, e := range h.entries {
		if e.serve != nil {
			loaded = append(loaded, e)
		}
	}
	h.mu.Unlock()
	for _, e := range loaded {
		h.activate(e, false)
	}
	h.prepareOptional(hooks)
	h.mu.Lock()
	resume := h.resume
	h.resume = nil
	h.mu.Unlock()
	h.ops.Unlock()
	h.changed()
	for _, trial := range resume {
		h.beginTrial(trial[0], trial[1])
	}
	h.watching.Add(2)
	go h.watch()
	go h.checkLoop()
}

// Close stops the folder watch and the update checks, discards every offer's
// staging and ends every plugin's process group and compiled Serve context.
func (h *Host) Close() {
	h.stopOnce.Do(func() { close(h.stop) })
	h.watching.Wait()
	h.dropOffers()
	h.ops.Lock()
	defer h.ops.Unlock()
	for _, s := range h.cfg.Compiled {
		s.end()
	}
	for _, s := range h.Optional() {
		s.end()
	}
	var wg sync.WaitGroup
	h.mu.Lock()
	for _, e := range h.entries {
		if e.sup != nil {
			wg.Add(1)
			go func(s *supervisor) { defer wg.Done(); s.halt() }(e.sup)
		}
	}
	h.mu.Unlock()
	wg.Wait()
}

func (h *Host) watch() {
	defer h.watching.Done()
	ticker := time.NewTicker(scanInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stop:
			return
		case <-ticker.C:
			h.ops.Lock()
			if h.scanLocked() {
				h.changed()
			}
			h.ops.Unlock()
		}
	}
}

// Serves answers the Serve of every loaded external plugin by id, running or
// not.
func (h *Host) Serves() []*Serve {
	h.mu.Lock()
	defer h.mu.Unlock()
	var serves []*Serve
	for _, e := range h.entries {
		if e.serve != nil {
			serves = append(serves, e.serve)
		}
	}
	sortServes(serves)
	return serves
}

// List answers every plugin as the Plugins page shows it: the compiled ones
// in wiring order, then every directory of the plugins folder by id.
func (h *Host) List() []Status {
	origins := h.origins()
	h.mu.Lock()
	defer h.mu.Unlock()
	statuses := make([]Status, 0, len(h.entries))
	for _, e := range h.entries {
		statuses = append(statuses, h.status(e, origins))
	}
	slices.SortFunc(statuses, func(a, b Status) int { return strings.Compare(a.ID, b.ID) })
	return append(h.compiledStatuses(), statuses...)
}

// status reads one entry under mu, with its origin when origins is not nil.
func (h *Host) status(e *entry, origins map[string]*origin) Status {
	st := Status{ID: e.id, Name: e.id}
	if origins != nil {
		st.Origin = h.originOf(e.id, origins[e.id])
	}
	if m := e.manifest; m != nil {
		st.Name, st.Version, st.Description = m.Name, m.Version, m.Description
		if m.Page != nil {
			st.PageTitle = m.Page.Title
		}
	}
	switch {
	case e.problem != "":
		st.State, st.Reason = StateBroken, e.problem
	case e.sup == nil:
		st.State = StateDisabled
	default:
		st.State, st.Reason = e.sup.snapshot()
		if st.State == StateCrashed {
			st.Tail = e.sup.log.Tail(crashTailLines)
		}
	}
	return st
}

// Status answers one plugin as List does, a compiled one before a folder
// entry of the same id, false for an unknown id.
func (h *Host) Status(id string) (Status, bool) {
	origins := h.origins()
	h.mu.Lock()
	defer h.mu.Unlock()
	if st, ok := h.compiledStatus(id); ok {
		return st, true
	}
	e := h.entries[id]
	if e == nil {
		return Status{}, false
	}
	return h.status(e, origins), true
}

// Log answers the last lines a plugin's process wrote, false for an id not
// in the folder.
func (h *Host) Log(id string) ([]string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.entries[id]
	if e == nil {
		return nil, false
	}
	if e.sup == nil {
		return nil, true
	}
	return e.sup.log.Tail(logLines), true
}

// ErrUnknownPlugin refuses a change of a plugin not in the folder.
var ErrUnknownPlugin = errors.New("There is no plugin with that id.")

// SetEnabled switches one plugin. Off ends its process group and removes its
// routes, assets and mounts, on reloads it from disk and starts it. An
// optional compiled plugin switches through switchOptional, one always on is
// refused.
func (h *Host) SetEnabled(id string, on bool) error {
	h.ops.Lock()
	defer h.ops.Unlock()
	if o := h.optionalOf(id); o != nil {
		return h.switchOptional(o, on)
	}
	e, err := h.known(id)
	if err != nil {
		return err
	}
	if err := h.writeDisabled(id, !on); err != nil {
		return err
	}
	h.unload(e)
	h.reload(id)
	h.changed()
	return nil
}

// Restart reloads one plugin from disk and starts it with a fresh backoff.
func (h *Host) Restart(id string) error {
	h.ops.Lock()
	defer h.ops.Unlock()
	e, err := h.known(id)
	if err != nil {
		return err
	}
	h.unload(e)
	h.reload(id)
	h.changed()
	return nil
}

// Remove ends one plugin and deletes its directory, of a link only the link,
// and the releases of a plugin installed from a repository. Its state
// directory stays, like a compiled plugin's when a build drops it.
func (h *Host) Remove(id string) error {
	h.ops.Lock()
	defer h.ops.Unlock()
	e, err := h.known(id)
	if err != nil {
		return err
	}
	h.unload(e)
	if err := h.removeDir(id); err != nil {
		h.reload(id)
		h.changed()
		return err
	}
	h.mu.Lock()
	delete(h.entries, id)
	h.mu.Unlock()
	if err := h.changeState(func(st *hostState) {
		st.Disabled = slices.DeleteFunc(st.Disabled, func(listed string) bool { return listed == id })
		delete(st.Origins, id)
	}); err != nil {
		log.Printf("plugin %s: removed, but its switch and origin could not be cleared: %v", id, err)
	}
	h.keepVersions(id)
	h.clearCheck(id)
	h.changed()
	return nil
}

// known answers the folder entry of an id. A compiled id the folder does not
// hold is refused with what that plugin allows. A folder directory that
// clashes with a compiled id resolves to the directory, so it can be
// restarted and removed.
func (h *Host) known(id string) (*entry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	e := h.entries[id]
	switch {
	case e != nil:
		return e, nil
	case h.optionalOf(id) != nil:
		return nil, refuse("%s is built in and can only be switched on or off.", id)
	case h.compiled[id]:
		return nil, refuse("%s is built in and always on.", id)
	}
	return nil, ErrUnknownPlugin
}

// removeDir renames a plugin's directory aside before deleting it, so a
// failed delete never leaves half a plugin under its id.
func (h *Host) removeDir(id string) error {
	if !ValidID(id) {
		return ErrUnknownPlugin
	}
	path := filepath.Join(h.Dir(), id)
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return os.Remove(path)
	}
	aside := filepath.Join(h.Dir(), ".remove-"+randomHex(6))
	if err := os.Rename(path, aside); err != nil {
		return err
	}
	return os.RemoveAll(aside)
}

// scanLocked syncs the entries with the folder and answers whether any
// changed. A directory with an unchanged signature is left alone.
func (h *Host) scanLocked() bool {
	if err := os.MkdirAll(h.Dir(), 0o700); err != nil {
		log.Printf("plugins: the folder %s could not be made: %v", h.Dir(), err)
	}
	dirEntries, err := os.ReadDir(h.Dir())
	if err != nil {
		return false
	}
	seen := map[string]bool{}
	moved := false
	for _, d := range dirEntries {
		name := d.Name()
		if strings.HasPrefix(name, ".") || len(seen) >= maxPlugins {
			continue
		}
		seen[name] = true
		h.mu.Lock()
		e := h.entries[name]
		h.mu.Unlock()
		if e != nil && e.sig == h.signature(name) {
			continue
		}
		if e != nil {
			h.unload(e)
		}
		h.reload(name)
		moved = true
	}
	h.mu.Lock()
	var gone []*entry
	for name, e := range h.entries {
		if !seen[name] {
			gone = append(gone, e)
			delete(h.entries, name)
		}
	}
	h.mu.Unlock()
	for _, e := range gone {
		h.unload(e)
		moved = true
	}
	if moved {
		h.forget()
	}
	return moved
}

// signature detects a change without reading the manifest: size and mtime
// of the directory and the manifest, plus a link's target.
func (h *Host) signature(name string) string {
	path := filepath.Join(h.Dir(), name)
	var parts []string
	for _, p := range []string{path, filepath.Join(path, ManifestFile)} {
		info, err := os.Stat(p)
		if err != nil {
			parts = append(parts, "-")
			continue
		}
		parts = append(parts, fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano()))
	}
	if target, err := os.Readlink(path); err == nil {
		parts = append(parts, target)
	}
	return strings.Join(parts, "|")
}

// reload replaces an entry with a fresh read of its directory.
func (h *Host) reload(name string) {
	e := &entry{id: name, sig: h.signature(name)}
	h.load(e)
	h.mu.Lock()
	h.entries[name] = e
	started := h.started
	h.mu.Unlock()
	if started && e.serve != nil {
		h.activate(e, true)
	}
}

// activate prepares a loaded plugin, closes its directory handles and starts
// its process. Settings and memory apply here only when live, at serve start
// they apply with those of the compiled plugins.
func (h *Host) activate(e *entry, live bool) {
	h.mu.Lock()
	hooks := h.hooks
	h.mu.Unlock()
	if hooks.Prepare != nil {
		if err := hooks.Prepare(e.serve); err != nil {
			e.serve.end()
			h.mu.Lock()
			e.problem = err.Error()
			e.serve = nil
			e.sup = nil
			h.mu.Unlock()
			e.closeRoots()
			return
		}
	}
	if live && hooks.Loaded != nil {
		hooks.Loaded(e.serve)
	}
	e.closeRoots()
	e.sup.begin()
}

// unload ends one entry's process group and takes its Serve away.
func (h *Host) unload(e *entry) {
	if e.sup != nil {
		e.sup.halt()
	}
	h.mu.Lock()
	hooks := h.hooks
	serve := e.serve
	e.serve = nil
	e.sup = nil
	h.mu.Unlock()
	e.closeRoots()
	if serve != nil {
		serve.end()
		if hooks.Release != nil {
			hooks.Release(e.id)
		}
	}
}

func (e *entry) closeRoots() {
	for _, root := range e.roots {
		_ = root.Close()
	}
	e.roots = nil
}

func (h *Host) disabled() map[string]bool {
	var st hostState
	statefile.Load(enabledPath(h.cfg.StateDir), &st)
	off := map[string]bool{}
	for _, id := range st.Disabled {
		off[id] = true
	}
	return off
}

func (h *Host) writeDisabled(id string, off bool) error {
	return h.changeState(func(st *hostState) {
		st.Disabled = slices.DeleteFunc(st.Disabled, func(listed string) bool { return listed == id })
		if off {
			st.Disabled = append(st.Disabled, id)
			slices.Sort(st.Disabled)
		}
	})
}

// changed bumps the version and announces it.
func (h *Host) changed() {
	h.version.Add(1)
	h.mu.Lock()
	notify := h.hooks.Changed
	h.mu.Unlock()
	if notify != nil {
		notify()
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("random: " + err.Error())
	}
	return hex.EncodeToString(b)
}
