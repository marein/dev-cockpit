// Package plugin is dev-cockpit's compile time extension point, and it is
// contract only: the interfaces a plugin is handed, the wiring pair a
// distribution builds, the slot names and the one sentinel error. A custom
// distribution constructs its plugins and hands them to distro.Main in
// distro.Build as ordered named pairs, see the custom distributions section
// of the README. There is no registry, a binary carries exactly the compiled
// plugins its distribution passes; the implementations behind these
// interfaces live inside the cockpit. External plugins are separate
// processes the cockpit loads at runtime, they reach the same surface through
// a manifest and a callback socket, see the plugins section of the README.
//
// A plugin does not name itself: the distribution picks the id when it wires
// the plugin in, and everything the plugin contributes carries that id, the
// /plugins/<id>/ URL subtree and the <id>- element name prefix. A plugin is
// typed by the surface it extends, and the one surface so far is the serving
// cockpit: at serve start, after the configuration is loaded and before the
// server listens, the cockpit hands every ServePlugin its own Serve, and the
// plugin's ConfigureServe adds what it contributes. A plugin wired with
// Named.Optional ships switched off and configures at every switch on under
// Settings › Plugins. The whole surface is experimental, and these
// interfaces grow with the cockpit: a plugin that builds its own fakes of
// them must expect new methods with any release.
package plugin

import (
	"context"
	"errors"
	"io/fs"
	"net/http"
	"time"
)

// ServePlugin extends the serving cockpit. ConfigureServe is one call at
// serve start: everything on the Serve answers real values during it, and
// when it returns the Serve is sealed. An error aborts the start, a plugin
// that cannot wire itself is a build wiring mistake and nothing to limp
// past.
//
// An optional plugin, see Named.Optional, is configured with a fresh Serve at
// every switch on. Every switch off ends that Serve's Context and takes away
// what it added, so what ConfigureServe starts must end with the Context. An
// error or a panic refuses the switch on, the cockpit goes on.
type ServePlugin interface {
	ConfigureServe(s Serve) error
}

// Named wires one plugin under the id the distribution picks. The order of
// the pairs is the order every plugin surface renders in.
type Named[T any] struct {
	ID     string
	Plugin T
	// Optional ships the plugin switched off, with a switch under Settings ›
	// Plugins that lasts across restarts. Nothing of it runs or is written
	// while it is off. Without it the plugin is always on.
	Optional bool
}

// Slot names the templates offer. A slot is a place in a cockpit page where
// plugin markup renders; which slots exist is the cockpit's decision and the
// list only ever grows.
const (
	// SlotProjectsActions sits in the projects column's tools, left of the
	// create project button, on every screen: the column is what the phone
	// opens as its sheet.
	SlotProjectsActions = "projects-actions"
	// SlotProjectsEmptyActions sits in the empty state of the projects page,
	// next to the create project action a fresh install shows.
	SlotProjectsEmptyActions = "projects-empty-actions"
)

// Event sources for AddEventHandler. The cockpit decides which exist, the
// list only ever grows.
const (
	// EventJob is an assistant's job closing, done, blocked or expired.
	EventJob = "job"
	// EventCoder is a coder that ended its turn or asks a question.
	EventCoder = "coder"
	// EventCompose is a compose run ending.
	EventCompose = "compose"
)

// Event is one thing that happened in the cockpit, as an assistant's trigger
// sees it. Fields are only ever added. The JSON names are what an external
// plugin reads on its callback socket.
type Event struct {
	// Source is one of the Event constants.
	Source string `json:"source"`
	// Kind says what happened within the source, like done or asks.
	Kind string `json:"kind"`
	// Target is what the event is about: the terminal of a job or a coder,
	// the run of a compose event.
	Target string `json:"target,omitempty"`
	// Time is when it happened.
	Time time.Time `json:"time"`
	// Headline is one line that says what happened, Body the rest, like a
	// job's report.
	Headline string `json:"headline"`
	Body     string `json:"body,omitempty"`
	// Project names the project the event is about, where one is known.
	Project string `json:"project,omitempty"`
}

// ErrProjectExists is what Projects.Create answers when a project of that
// name already exists and holds content. An existing empty directory is not
// this error, Create adopts it, see there. Check with errors.Is; how a
// dialog words the refusal stays the plugin's.
var ErrProjectExists = errors.New("A project with that name already exists.")

// Serve is the serving cockpit as one plugin sees it, registrar and host in
// one object: what the plugin contributes is added here, and what the
// instance is stands here to read. The cockpit builds one per ServePlugin and
// call of ConfigureServe, bound to the wiring id, and hands it to
// ConfigureServe; everything on it answers real values during that call, and
// when ConfigureServe returns the Serve is sealed and every further Add
// panics. A handler may keep the Serve or anything it answered.
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
	// AddPage gives the plugin a page of its own at the root of its URL
	// subtree, reached from an entry in the side rail and, on a phone, from
	// a row in the Cockpit sheet. The cockpit renders the page in its own
	// layout, see Page for what each field becomes. The page takes GET of the
	// subtree root, a route the plugin added there no longer answers it. A
	// second call replaces the first, an empty Title or an Icon that is not
	// one Tabler class panics.
	AddPage(page Page)
	// AddAssistantMemory adds top level <slug>.md files to the assistant's
	// shared memory, a slug being lower case letters, digits and dashes,
	// other files are skipped. Applied at serve start in plugin order, an
	// optional plugin's at every switch on, without overwrite an existing
	// file is kept. A switch off leaves the written files as they are.
	AddAssistantMemory(files fs.FS, overwrite bool)
	// AddEventHandler subscribes the plugin to the events of the named
	// sources, each one of the Event constants, any other panics. Every
	// handler runs on the cockpit's one event goroutine in publish order, so
	// it returns quickly and hands longer work to a goroutine of its own.
	// Events that arrive while the plugins fall behind are dropped.
	AddEventHandler(sources []string, handler func(Event))
	// AddSettings adds values to the settings store, key to value. Applied at
	// serve start before anything reads the store, in plugin order, an
	// optional plugin's at every switch on, without overwrite an existing key
	// is kept. A switch off leaves the written keys as they are.
	AddSettings(values map[string]string, overwrite bool)
	// StateDir answers the serving instance's state directory, which is what
	// addresses the running cockpit itself, for example as --state-dir of a
	// `dev-cockpit git` call.
	StateDir() string
	// PluginStateDir answers the plugin's own directory, plugins/<wiring id>
	// inside the state directory, created owner only before ConfigureServe
	// runs. What the plugin keeps there is its own business, and the
	// directory is not part of the cockpit backup. Unlike an Add it keeps
	// answering after the Serve is sealed, so a handler uses it at runtime.
	PluginStateDir() string
	// Context answers a context that ends when the plugin is switched off or
	// the cockpit shuts down. What ConfigureServe starts beyond the call, a
	// goroutine, a ticker, a connection, ends with it, so an optional plugin
	// runs nothing while it is off. Unlike an Add it keeps answering after the
	// Serve is sealed.
	Context() context.Context
	// ProjectsDir answers the serving instance's projects root directory,
	// where a project on the projects page lives.
	ProjectsDir() string
	// Projects answers the cockpit's project surface.
	Projects() Projects
}

// Page is a plugin's own page as AddPage takes it. Fields only ever get
// added, so a plugin sets them by name.
type Page struct {
	// Title is the page's heading, required.
	Title string
	// Icon is the entry's icon, one Tabler icon class like "ti-ticket",
	// required.
	Icon string
	// HTML is the work surface below the heading, rendered unescaped and as
	// trusted as AddSlotHTML; a plugin usually mounts one of its custom
	// elements there.
	HTML string
	// NavLabel is the entry's short name in the rail and the Cockpit sheet,
	// optional, empty falls back to Title. A name too long for the rail
	// ends in an ellipsis there, the Title stays its tooltip and its
	// accessible name.
	NavLabel string
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
