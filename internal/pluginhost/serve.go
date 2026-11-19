package pluginhost

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/marein/dev-cockpit/plugin"
)

// definedSlots is what AddSlotHTML checks a slot name against. Markup for a
// slot no template renders would vanish silently, so an unknown name is a
// wiring mistake and panics. The names are the plugin package's constants.
var definedSlots = map[string]bool{
	plugin.SlotProjectsActions:      true,
	plugin.SlotProjectsEmptyActions: true,
}

// SlotDefined answers whether a template renders the named slot.
func SlotDefined(slot string) bool { return definedSlots[slot] }

// Registration is one added custom element as the cockpit reads it: the
// final name next to the module path inside the added assets.
type Registration struct {
	Name   string
	Module string
}

// Serve implements plugin.Serve for one wired plugin, and it is also the
// cockpit's reading side of what that plugin added. What each method means
// is documented on the plugin package's interfaces, the contract; this is
// the construction, the sealing and the collection behind it.
type Serve struct {
	id             string
	sealed         bool
	projectsDir    string
	stateDir       string
	pluginStateDir string
	projects       *projects
	assistants     *Assistants
	routes         http.Handler
	assets         fs.FS
	elements       []Registration
	slots          map[string][]string
	page           *plugin.Page
	memory         []assistantMemory
	settings       []settingValues
	handlers       []eventHandler
	// order is a compiled plugin's place in the wiring.
	order  int
	ctx    context.Context
	cancel context.CancelFunc
	// live is set on an external or optional plugin's Serve, nil on one
	// fixed at serve start.
	live *liveServe
}

// liveServe is what a live Serve carries: its load generation and whether
// the plugin answers.
type liveServe struct {
	generation int64
	running    func() bool
}

var _ plugin.Serve = (*Serve)(nil)

func (s *Serve) ProjectsDir() string { return s.projectsDir }

func (s *Serve) StateDir() string { return s.stateDir }

func (s *Serve) PluginStateDir() string { return s.pluginStateDir }

func (s *Serve) Projects() plugin.Projects { return s.projects }

func (s *Serve) Assistants() plugin.Assistants { return serveAssistants{serve: s} }

func (s *Serve) Context() context.Context { return s.ctx }

func (s *Serve) AddRoutes(handler http.Handler) plugin.Routes {
	s.add()
	s.routes = handler
	return routesHandle{base: "/plugins/" + s.id}
}

func (s *Serve) AddAssets(files fs.FS) {
	s.add()
	s.assets = files
}

func (s *Serve) AddCustomElement(name, module string) plugin.CustomElement {
	s.add()
	if strings.TrimSpace(name) == "" || strings.TrimSpace(module) == "" {
		panic(fmt.Sprintf("plugin %s: AddCustomElement needs a name and a module path", s.id))
	}
	registration := Registration{Name: s.id + "-" + name, Module: module}
	for _, existing := range s.elements {
		if existing.Name == registration.Name {
			panic(fmt.Sprintf("plugin %s: a custom element named %s is already added", s.id, name))
		}
	}
	s.elements = append(s.elements, registration)
	return elementHandle{name: registration.Name}
}

func (s *Serve) AddSlotHTML(slot, html string) {
	s.add()
	if !definedSlots[slot] {
		panic(fmt.Sprintf("plugin %s: unknown slot %q", s.id, slot))
	}
	if s.slots == nil {
		s.slots = map[string][]string{}
	}
	s.slots[slot] = append(s.slots[slot], html)
}

// tablerIcon is one Tabler icon class, the icon lands in a class list and must
// not bring classes of its own.
var tablerIcon = regexp.MustCompile(`^ti-[a-z0-9-]+$`)

func (s *Serve) AddPage(page plugin.Page) {
	s.add()
	if strings.TrimSpace(page.Title) == "" || !tablerIcon.MatchString(page.Icon) {
		panic(fmt.Sprintf("plugin %s: AddPage needs a Title and a Tabler icon class like ti-box, got %q", s.id, page.Icon))
	}
	s.page = &page
}

func (s *Serve) AddAssistantMemory(files fs.FS, overwrite bool) {
	s.add()
	if files == nil {
		panic(fmt.Sprintf("plugin %s: AddAssistantMemory needs files", s.id))
	}
	s.memory = append(s.memory, assistantMemory{files: files, overwrite: overwrite})
}

func (s *Serve) AddSettings(values map[string]string, overwrite bool) {
	s.add()
	copied := make(map[string]string, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) == "" {
			panic(fmt.Sprintf("plugin %s: AddSettings needs a key for every value", s.id))
		}
		copied[key] = value
	}
	s.settings = append(s.settings, settingValues{values: copied, overwrite: overwrite})
}

// add is the seal guard every Add runs first.
func (s *Serve) add() {
	if s.sealed {
		panic(fmt.Sprintf("plugin %s: an Add was called after ConfigureServe returned", s.id))
	}
}

// The accessors below are the cockpit's reading side of a sealed Serve.

// ID answers the wiring id the Serve is bound to.
func (s *Serve) ID() string { return s.id }

// Handler answers the added HTTP handler, nil for none.
func (s *Serve) Handler() http.Handler { return s.routes }

// Assets answers the added asset files, nil for none.
func (s *Serve) Assets() fs.FS { return s.assets }

// CustomElements answers the added elements, in call order.
func (s *Serve) CustomElements() []Registration { return s.elements }

// Page answers the added page, nil for none.
func (s *Serve) Page() *plugin.Page { return s.page }

// SlotHTML answers the markup added for a slot, "" for none.
func (s *Serve) SlotHTML(slot string) string { return strings.Join(s.slots[slot], "") }

// Live answers whether the Serve comes and goes while the cockpit runs, an
// external or optional plugin's. A live Serve stays out of the import map and
// the asset digest, which a page fixes at load, and renders in live regions.
func (s *Serve) Live() bool { return s.live != nil }

// Running answers whether the plugin answers: a compiled one always, an
// external one while its process is up and answering.
func (s *Serve) Running() bool { return s.live == nil || s.live.running() }

// Key names one load of a plugin, so a live region keeps an element that
// stayed and replaces one whose plugin was loaded again.
func (s *Serve) Key() string {
	if s.live == nil {
		return s.id
	}
	return fmt.Sprintf("%s@%d", s.id, s.live.generation)
}

// end ends the Serve's context, a compiled plugin's signal that this load
// is over.
func (s *Serve) end() {
	if s.cancel != nil {
		s.cancel()
	}
}

// routesHandle implements plugin.Routes.
type routesHandle struct {
	base string
}

func (r routesHandle) Path(sub string) string {
	if !strings.HasPrefix(sub, "/") {
		sub = "/" + sub
	}
	return r.base + sub
}

// elementHandle implements plugin.CustomElement.
type elementHandle struct {
	name string
}

func (e elementHandle) Name() string { return e.name }

// projects implements plugin.Projects over the two functions the cockpit
// hands ConfigureServe, and project implements plugin.Project behind it.
type projects struct {
	create  func(ctx context.Context, name string) (string, error)
	changed func(ctx context.Context) error
}

func (p *projects) Create(ctx context.Context, name string) (plugin.Project, error) {
	dir, err := p.create(ctx, name)
	if err != nil {
		return nil, err
	}
	return project{dir: dir, changed: p.changed}, nil
}

type project struct {
	dir     string
	changed func(ctx context.Context) error
}

func (p project) Dir() string { return p.dir }

func (p project) Name() string { return filepath.Base(p.dir) }

func (p project) Changed(ctx context.Context) error { return p.changed(ctx) }

// ConfigureServe runs the ConfigureServe of every pair that is always on,
// each with a fresh Serve bound to the pair's id and to the serving instance,
// and answers the sealed Serves in pair order. A pair marked Optional is left
// to the Host, which configures it at every switch on. Before a pair's
// ConfigureServe runs, the plugin's own state directory exists. It is the
// cockpit's call at serve start, after the configuration is loaded and
// before the server listens; the ids are validated before, see distro.Main.
// createProject is the web UI's own project creation path and projectChanged
// its announcement path, which is what the projects facade delegates to, and
// assistants is the assistant surface, bound later. A failing plugin aborts
// the start with its id on the error.
func ConfigureServe(plugins []plugin.Named[plugin.ServePlugin], projectsDir, stateDir string, createProject func(ctx context.Context, name string) (string, error), projectChanged func(ctx context.Context) error, assistants *Assistants) ([]*Serve, error) {
	shared := &projects{create: createProject, changed: projectChanged}
	serves := make([]*Serve, 0, len(plugins))
	for i, p := range plugins {
		if p.Optional {
			continue
		}
		s, err := newCompiledServe(p.ID, i, projectsDir, stateDir, shared, assistants)
		if err != nil {
			return nil, err
		}
		if err := p.Plugin.ConfigureServe(s); err != nil {
			s.end()
			return nil, fmt.Errorf("plugin %s failed to configure: %w", p.ID, err)
		}
		s.sealed = true
		serves = append(serves, s)
	}
	return serves, nil
}

// newCompiledServe makes the plugin's state directory and a fresh, unsealed
// Serve for one ConfigureServe call.
func newCompiledServe(id string, order int, projectsDir, stateDir string, shared *projects, assistants *Assistants) (*Serve, error) {
	pluginStateDir := filepath.Join(stateDir, "plugins", id)
	if err := os.MkdirAll(pluginStateDir, 0o700); err != nil {
		return nil, fmt.Errorf("plugin %s has no state directory: %w", id, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Serve{id: id, order: order, ctx: ctx, cancel: cancel, projectsDir: projectsDir, stateDir: stateDir, pluginStateDir: pluginStateDir, projects: shared, assistants: assistants}, nil
}
