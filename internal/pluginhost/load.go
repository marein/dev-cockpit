package pluginhost

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/plugin"
)

// load reads e's directory: the manifest, and for an enabled plugin its Serve
// and supervisor. A problem lands in e.problem as one sentence.
func (h *Host) load(e *entry) {
	if !ValidID(e.id) || len(e.id) > maxExternalID {
		e.problem = "The directory name is not a valid plugin id."
		return
	}
	dir := filepath.Join(h.Dir(), e.id)
	root, err := os.OpenRoot(dir)
	if err != nil {
		e.problem = "The plugin directory could not be opened."
		return
	}
	m, err := readManifest(root)
	if err != nil {
		_ = root.Close()
		e.problem = err.Error()
		return
	}
	e.manifest = m
	if problem := h.claim(m.ID, e.id); problem != "" {
		_ = root.Close()
		e.problem = problem
		return
	}
	if h.disabled()[e.id] {
		_ = root.Close()
		return
	}
	serve, sup, roots, err := h.build(dir, root, m)
	if err != nil {
		for _, r := range roots {
			_ = r.Close()
		}
		e.problem = err.Error()
		return
	}
	e.serve, e.sup, e.roots = serve, sup, roots
}

// claim answers why a manifest id cannot be used in a directory, "" when it
// can. Requiring the directory name and refusing compiled ids keeps every
// plugin id unique.
func (h *Host) claim(id, dirName string) string {
	if id != dirName {
		return fmt.Sprintf("The directory is %s, but the manifest id is %s.", cut(dirName), id)
	}
	if h.compiled[id] {
		return fmt.Sprintf("A built in plugin has the id %s.", id)
	}
	return ""
}

func readManifest(root *os.Root) (*Manifest, error) {
	f, err := root.Open(ManifestFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, errors.New("The directory has no plugin.json.")
	}
	if err != nil {
		return nil, errors.New("plugin.json could not be opened.")
	}
	defer f.Close()
	return ParseManifest(f)
}

// checkRun requires the run file to be a regular executable inside the
// plugin directory.
func checkRun(root *os.Root, m *Manifest) error {
	info, err := root.Stat(m.Run)
	if err != nil {
		return fmt.Errorf("The run file %s is missing or leaves the plugin directory.", m.Run)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("The run file %s is not executable.", m.Run)
	}
	return nil
}

// build makes an enabled plugin's Serve from its manifest through the Adds a
// compiled plugin calls. The returned roots stay open until the cockpit has
// read the assets and the memory.
func (h *Host) build(dir string, root *os.Root, m *Manifest) (*Serve, *supervisor, []*os.Root, error) {
	roots := []*os.Root{root}
	if err := checkRun(root, m); err != nil {
		return nil, nil, roots, err
	}
	pluginStateDir := filepath.Join(h.cfg.StateDir, "plugins", m.ID)
	if err := os.MkdirAll(pluginStateDir, 0o700); err != nil {
		return nil, nil, roots, errors.New("The plugin's state directory could not be created.")
	}
	runDir, err := h.runDir(m.ID)
	if err != nil {
		return nil, nil, roots, errors.New("The plugin's socket directory could not be created.")
	}
	p, partRoots, err := readParts(root, m)
	roots = append(roots, partRoots...)
	if err != nil {
		return nil, nil, roots, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Serve{id: m.ID, ctx: ctx, cancel: cancel, projectsDir: h.cfg.ProjectsDir, stateDir: h.cfg.StateDir, pluginStateDir: pluginStateDir, projects: h.projects}
	sup := newSupervisor(h, m, dir, runDir, pluginStateDir)
	s.AddRoutes(sup)
	if p.assets != nil {
		s.AddAssets(p.assets)
	}
	for _, element := range m.Elements {
		s.AddCustomElement(element.Name, element.Module)
	}
	for _, slot := range p.slots {
		s.AddSlotHTML(slot.slot, slot.markup)
	}
	if page := m.Page; page != nil {
		s.AddPage(plugin.Page{Title: page.Title, Icon: page.Icon, NavLabel: page.NavLabel, HTML: p.page})
	}
	if st := m.Settings; st != nil && len(st.Values) > 0 {
		s.AddSettings(st.Values, st.Overwrite)
	}
	if p.memory != nil {
		s.AddAssistantMemory(p.memory, m.Memory.Overwrite)
	}
	if len(m.Events) > 0 {
		s.AddEventHandler(m.Events, sup.deliverEvent)
	}
	s.live = &liveServe{generation: h.gen.Add(1), running: sup.running}
	s.sealed = true
	return s, sup, roots, nil
}

// parts is what build reads beneath the plugin directory besides the
// manifest, the markup with its base filled in.
type parts struct {
	assets fs.FS
	slots  []slotMarkup
	page   string
	memory fs.FS
}

type slotMarkup struct {
	slot   string
	markup string
}

// readParts reads what the manifest names beneath root, so no path leads
// outside the plugin directory, symlinks included. The caller closes the
// returned roots, on an error too.
func readParts(root *os.Root, m *Manifest) (parts, []*os.Root, error) {
	var p parts
	var roots []*os.Root
	if m.Assets != "" {
		assets, err := root.OpenRoot(m.Assets)
		if err != nil {
			return p, roots, fmt.Errorf("The assets directory %s is missing or leaves the plugin directory.", m.Assets)
		}
		roots = append(roots, assets)
		if err := checkAssets(assets.FS(), m); err != nil {
			return p, roots, err
		}
		p.assets = assets.FS()
	}
	base := "/plugins/" + m.ID
	for _, slot := range m.Slots {
		markup, err := readMarkup(root, slot.HTML, base)
		if err != nil {
			return p, roots, err
		}
		p.slots = append(p.slots, slotMarkup{slot: slot.Slot, markup: markup})
	}
	if page := m.Page; page != nil {
		markup, err := readMarkup(root, page.HTML, base)
		if err != nil {
			return p, roots, err
		}
		p.page = markup
	}
	if mem := m.Memory; mem != nil {
		memory, err := root.OpenRoot(mem.Dir)
		if err != nil {
			return p, roots, fmt.Errorf("The memory directory %s is missing or leaves the plugin directory.", mem.Dir)
		}
		roots = append(roots, memory)
		p.memory = memory.FS()
	}
	return p, roots, nil
}

// checkAssets bounds the assets and proves that every asset and element
// module resolves inside the assets directory.
func checkAssets(files fs.FS, m *Manifest) error {
	count, size := 0, int64(0)
	err := fs.WalkDir(files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("The asset %s could not be read.", name)
		}
		if d.IsDir() {
			return nil
		}
		info, err := fs.Stat(files, name)
		if err != nil {
			return fmt.Errorf("The asset %s leaves the plugin directory.", name)
		}
		count++
		size += info.Size()
		if count > maxAssetFiles || size > maxAssetBytes {
			return fmt.Errorf("The assets exceed %d files or 32 MiB.", maxAssetFiles)
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, element := range m.Elements {
		info, err := fs.Stat(files, filepath.ToSlash(element.Module))
		if err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("The module %s of element %s is not in the assets directory.", element.Module, element.Name)
		}
	}
	return nil
}

// maxSocketPath mirrors the local API's bound for a unix socket address.
const maxSocketPath = 100

// runRoot answers the directory of the plugins' sockets and pid files: in
// the state directory, or, when that path is too long for a socket, in the
// temp directory under a hash of it, as the local API falls back.
func runRoot(stateDir string) string {
	dir := filesystem.AbsDir(stateDir)
	inState := filepath.Join(dir, "plugin-run")
	if len(inState)+1+maxExternalID+2 <= maxSocketPath {
		return inState
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join(os.TempDir(), "dev-cockpit-plugins-"+hex.EncodeToString(sum[:8]))
}

// runDir makes one plugin's socket directory, owner only like its parent,
// since a socket's own mode cannot be trusted before it is bound.
func (h *Host) runDir(id string) (string, error) {
	root := runRoot(h.cfg.StateDir)
	dir := filepath.Join(root, id)
	for _, d := range []string{root, dir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return "", err
		}
		if err := os.Chmod(d, 0o700); err != nil {
			return "", err
		}
	}
	return dir, nil
}

// pluginEnv is the cockpit's environment plus the protocol's variables,
// without the tmux and DEV_COCKPIT_ ones, which name the cockpit's own
// session and sockets. The per run ones join at start.
func pluginEnv(h *Host, m *Manifest, dir, runDir, pluginStateDir string) []string {
	var env []string
	for _, kv := range os.Environ() {
		name, _, _ := strings.Cut(kv, "=")
		if name == "TMUX" || name == "TMUX_PANE" || strings.HasPrefix(name, "DEV_COCKPIT_") {
			continue
		}
		env = append(env, kv)
	}
	return append(env,
		fmt.Sprintf("DEV_COCKPIT_PROTOCOL=%d", Protocol),
		"DEV_COCKPIT_PLUGIN_ID="+m.ID,
		"DEV_COCKPIT_PLUGIN_DIR="+dir,
		"DEV_COCKPIT_PLUGIN_BASE=/plugins/"+m.ID,
		"DEV_COCKPIT_PLUGIN_SOCKET="+filepath.Join(runDir, "p"),
		"DEV_COCKPIT_SOCKET="+filepath.Join(runDir, "c"),
		"DEV_COCKPIT_PLUGIN_STATE_DIR="+pluginStateDir,
		"DEV_COCKPIT_STATE_DIR="+h.cfg.StateDir,
		"DEV_COCKPIT_PROJECTS_DIR="+h.cfg.ProjectsDir,
	)
}
