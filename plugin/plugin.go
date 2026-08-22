// Package plugin is dev-cockpit's compile time extension point, and it is
// contract only: the interfaces a plugin is handed, the wiring pair a
// distribution builds, the slot names and the one sentinel error. A custom
// distribution constructs its plugins and hands them to distro.Main in
// distro.Build as ordered named pairs, see the custom distributions section
// of the README. Nothing is loaded at runtime and there is no registry, a
// binary carries exactly the plugins its distribution passes; the
// implementations behind these interfaces live inside the cockpit.
//
// A plugin does not name itself: the distribution picks the id when it wires
// the plugin in, and everything the plugin contributes carries that id, the
// /plugins/<id>/ URL subtree and the <id>- element name prefix. A plugin is
// typed by the surface it extends, and the one surface so far is the serving
// cockpit: at serve start, after the configuration is loaded and before the
// server listens, the cockpit hands every ServePlugin its own Serve, and the
// plugin's ConfigureServe adds what it contributes. The whole surface is
// experimental, and these interfaces grow with the cockpit: a plugin that
// builds its own fakes of them must expect new methods with any release.
package plugin

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
)

// ServePlugin extends the serving cockpit. ConfigureServe is one call at
// serve start: everything on the Serve answers real values during it, and
// when it returns the Serve is sealed. An error aborts the start, a plugin
// that cannot wire itself is a build wiring mistake and nothing to limp
// past.
type ServePlugin interface {
	ConfigureServe(s Serve) error
}

// Named wires one plugin under the id the distribution picks. The order of
// the pairs is the order every plugin surface renders in.
type Named[T any] struct {
	ID     string
	Plugin T
}

// Slot names the templates offer. A slot is a place in a cockpit page where
// plugin markup renders; which slots exist is the cockpit's decision and the
// list only ever grows.
const (
	// SlotProjectsActions sits in the projects page header, next to the
	// create project button.
	SlotProjectsActions = "projects-actions"
	// SlotProjectsEmptyActions sits in the empty state of the projects page,
	// next to the create project action a fresh install shows.
	SlotProjectsEmptyActions = "projects-empty-actions"
)

// ErrProjectExists is what Projects.Create answers when a project of that
// name already exists and holds content. An existing empty directory is not
// this error, Create adopts it, see there. Check with errors.Is; how a
// dialog words the refusal stays the plugin's.
var ErrProjectExists = errors.New("A project with that name already exists.")

// Serve is the serving cockpit as one plugin sees it, registrar and host in
// one object: what the plugin contributes is added here, and what the
// instance is stands here to read. The cockpit builds one per ServePlugin,
// bound to the wiring id, and hands it to ConfigureServe; everything on it
// answers real values during that call, and when ConfigureServe returns the
// Serve is sealed and every further Add panics. A handler may keep the Serve
// or anything it answered.
type Serve interface {
	// AddRoutes adds the plugin's HTTP handler, mounted under the plugin's
	// URL subtree; the answered handle's Path names where. The handler sees
	// the request path relative to the subtree, so the handler behind
	// Path("/repos") reads r.URL.Path as "/repos". The routes sit inside the
	// cockpit's session like every app route: only a signed in browser
	// reaches them, and an unsafe method has to carry the CSRF token, which
	// the shared @dc/http module attaches on its own. A second call replaces
	// the first.
	AddRoutes(handler http.Handler) Routes
	// AddAssets adds the plugin's static files. They travel through the
	// cockpit's content hashed asset pipeline under the plugin's URL
	// subtree, so they carry the same long lived cache headers as the
	// cockpit's own assets, and references between them are rewritten to the
	// hashed URLs. A second call replaces the first.
	AddAssets(files fs.FS)
	// AddCustomElement adds one custom element: module names a module inside
	// the added assets whose default export is the element class, and the
	// answered handle's Name is the element's final name, which is what slot
	// markup mounts. The cockpit defines the element, plugin code never
	// calls customElements.define. The element loads lazily: the cockpit's
	// import map binds the final name to a generated starter module, and a
	// page that never shows the element loads none of the plugin's code. The
	// starter's own path inside the plugin's subtree, /elements.js, is
	// reserved. A name added twice panics, two registrations would bind one
	// import map key twice.
	AddCustomElement(name, module string) CustomElement
	// AddSlotHTML adds markup for a named slot. The markup is rendered as
	// is, unescaped, which is exactly as trusted as the rest of the plugin:
	// it is compiled into the binary. A plugin usually mounts one of its
	// custom elements here, under the element handle's Name, and hands its
	// endpoints along as data attributes built with the route handle's Path.
	// Several calls for one slot render in call order. A slot outside the
	// defined set panics, markup for a place no template renders would
	// otherwise vanish silently.
	AddSlotHTML(slot, html string)
	// StateDir answers the serving instance's state directory, which is what
	// addresses the running cockpit itself, for example as --state-dir of a
	// `dev-cockpit git` call.
	StateDir() string
	// ProjectsDir answers the serving instance's projects root directory,
	// where a project on the projects page lives.
	ProjectsDir() string
	// Projects answers the cockpit's project surface.
	Projects() Projects
}

// Routes is the handle AddRoutes answers. Path names where the cockpit
// mounted the handler, so markup and clients never hardcode a plugin's URLs.
type Routes interface {
	// Path answers the mounted URL of one of the handler's paths, the path
	// the handler reads in r.URL.Path.
	Path(sub string) string
}

// CustomElement is the handle AddCustomElement answers. Name is the
// element's final name, which is what slot markup mounts.
type CustomElement interface {
	// Name answers the element's final name: the wiring id, a dash, and the
	// name AddCustomElement was given.
	Name() string
}

// Projects is the cockpit's project surface, answered by Serve.Projects.
// Everything it does runs through the exact code the web UI runs, so what a
// plugin does here is what a person doing it on the page would have done.
type Projects interface {
	// Create makes a new project, through the same path the projects page's
	// create action takes: the directory appears under the projects root,
	// the change is announced, and every open page picks it up live. The
	// name is sanitized the way the page sanitizes it, so the answered
	// Project carries where the project really lives and what it is really
	// called. A name whose directory already exists but is empty answers
	// that project instead of an error, a leftover from an earlier attempt
	// is a place to fill and not a refusal; a name whose directory holds
	// content answers ErrProjectExists.
	Create(ctx context.Context, name string) (Project, error)
}

// Project is one project of the serving cockpit.
type Project interface {
	// Dir answers the project's directory.
	Dir() string
	// Name answers the canonical project name, the sanitized directory name
	// the projects page shows.
	Name() string
	// Changed announces that the project's contents moved: it publishes the
	// same event the page's own actions publish, so every open list pulls
	// the project's fresh state. A plugin calls it when it is done writing
	// into the project, however that went, and the list matches reality
	// either way.
	Changed(ctx context.Context) error
}
