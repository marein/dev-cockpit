package pluginhost

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
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
	id          string
	sealed      bool
	projectsDir string
	stateDir    string
	projects    *projects
	routes      http.Handler
	assets      fs.FS
	elements    []Registration
	slots       map[string][]string
}

var _ plugin.Serve = (*Serve)(nil)

func (s *Serve) ProjectsDir() string { return s.projectsDir }

func (s *Serve) StateDir() string { return s.stateDir }

func (s *Serve) Projects() plugin.Projects { return s.projects }

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

// SlotHTML answers the markup added for a slot, "" for none.
func (s *Serve) SlotHTML(slot string) string { return strings.Join(s.slots[slot], "") }

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

// ConfigureServe runs every pair's ConfigureServe, each with a fresh Serve
// bound to the pair's id and to the serving instance, and answers the sealed
// Serves in pair order. It is the cockpit's call at serve start, after the
// configuration is loaded and before the server listens; the ids are
// validated before, see distro.Main. createProject is the web UI's own
// project creation path and projectChanged its announcement path, which is
// what the projects facade delegates to. A failing plugin aborts the start
// with its id on the error.
func ConfigureServe(plugins []plugin.Named[plugin.ServePlugin], projectsDir, stateDir string, createProject func(ctx context.Context, name string) (string, error), projectChanged func(ctx context.Context) error) ([]*Serve, error) {
	shared := &projects{create: createProject, changed: projectChanged}
	serves := make([]*Serve, 0, len(plugins))
	for _, p := range plugins {
		s := &Serve{id: p.ID, projectsDir: projectsDir, stateDir: stateDir, projects: shared}
		if err := p.Plugin.ConfigureServe(s); err != nil {
			return nil, fmt.Errorf("plugin %s failed to configure: %w", p.ID, err)
		}
		s.sealed = true
		serves = append(serves, s)
	}
	return serves, nil
}
