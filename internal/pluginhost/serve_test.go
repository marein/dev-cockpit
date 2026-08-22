package pluginhost

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/marein/dev-cockpit/plugin"
)

// recordingPlugin adds what its fields carry and keeps the Serve it was
// handed, so a test can poke the sealed object afterwards.
type recordingPlugin struct {
	err      error
	routes   http.Handler
	assets   fstest.MapFS
	elements [][2]string
	slots    map[string]string
	got      plugin.Serve
}

func (p *recordingPlugin) ConfigureServe(s plugin.Serve) error {
	p.got = s
	if p.err != nil {
		return p.err
	}
	if p.routes != nil {
		s.AddRoutes(p.routes)
	}
	if p.assets != nil {
		s.AddAssets(p.assets)
	}
	for _, element := range p.elements {
		s.AddCustomElement(element[0], element[1])
	}
	for slot, html := range p.slots {
		s.AddSlotHTML(slot, html)
	}
	return nil
}

// serveFunc adapts a function to a ServePlugin.
type serveFunc func(s plugin.Serve) error

func (f serveFunc) ConfigureServe(s plugin.Serve) error { return f(s) }

func TestConfigureServeCollects(t *testing.T) {
	first := &recordingPlugin{
		routes:   http.NotFoundHandler(),
		assets:   fstest.MapFS{},
		elements: [][2]string{{"widget", "widget.js"}, {"list", "list.js"}},
		slots:    map[string]string{plugin.SlotProjectsActions: "<first-widget></first-widget>"},
	}
	second := &recordingPlugin{}
	serves := configured(t,
		plugin.Named[plugin.ServePlugin]{ID: "first", Plugin: first},
		plugin.Named[plugin.ServePlugin]{ID: "second", Plugin: second},
	)
	if len(serves) != 2 || serves[0].ID() != "first" || serves[1].ID() != "second" {
		t.Fatalf("ConfigureServe() answered %d serves in the wrong order", len(serves))
	}
	s := serves[0]
	if s.Handler() == nil || s.Assets() == nil {
		t.Fatal("added routes or assets did not survive")
	}
	elements := s.CustomElements()
	if len(elements) != 2 || elements[0] != (Registration{Name: "first-widget", Module: "widget.js"}) {
		t.Fatalf("CustomElements() = %v", elements)
	}
	if got := s.SlotHTML(plugin.SlotProjectsActions); got != "<first-widget></first-widget>" {
		t.Fatalf("SlotHTML() = %q", got)
	}
	if got := s.SlotHTML("unknown-slot"); got != "" {
		t.Fatalf("unknown slot answered %q, want empty", got)
	}
	empty := serves[1]
	if empty.Handler() != nil || empty.Assets() != nil || len(empty.CustomElements()) != 0 {
		t.Fatal("a plugin that adds nothing must answer empty")
	}
}

func TestHandlesAnswerNames(t *testing.T) {
	var routes plugin.Routes
	var element plugin.CustomElement
	capture := serveFunc(func(s plugin.Serve) error {
		routes = s.AddRoutes(http.NotFoundHandler())
		element = s.AddCustomElement("widget", "widget.js")
		return nil
	})
	configured(t, plugin.Named[plugin.ServePlugin]{ID: "first", Plugin: capture})
	if got := routes.Path("/repos"); got != "/plugins/first/repos" {
		t.Fatalf("Path() = %q", got)
	}
	if got := routes.Path("repos"); got != "/plugins/first/repos" {
		t.Fatalf("Path() without slash = %q", got)
	}
	if got := element.Name(); got != "first-widget" {
		t.Fatalf("Name() = %q", got)
	}
}

// The instance stands on the Serve during ConfigureServe: the directories
// answer their real values right away, and the projects facade creates and
// announces through the paths the cockpit handed in. The handle carries the
// canonical name next to the directory.
func TestServeAnswersTheInstance(t *testing.T) {
	var created []string
	create := func(ctx context.Context, name string) (string, error) {
		created = append(created, name)
		return "/srv/projects/" + name, nil
	}
	announced := 0
	changed := func(ctx context.Context) error {
		announced++
		return nil
	}
	checked := false
	check := serveFunc(func(s plugin.Serve) error {
		checked = true
		if s.ProjectsDir() != "/srv/projects" || s.StateDir() != "/srv/state" {
			t.Fatalf("Serve answers %q %q during ConfigureServe", s.ProjectsDir(), s.StateDir())
		}
		proj, err := s.Projects().Create(context.Background(), "fresh")
		if err != nil {
			t.Fatalf("Create() = %v", err)
		}
		if proj.Dir() != "/srv/projects/fresh" {
			t.Fatalf("Dir() = %q", proj.Dir())
		}
		if proj.Name() != "fresh" {
			t.Fatalf("Name() = %q", proj.Name())
		}
		if err := proj.Changed(context.Background()); err != nil {
			t.Fatalf("Changed() = %v", err)
		}
		return nil
	})
	pairs := []plugin.Named[plugin.ServePlugin]{{ID: "aware", Plugin: check}}
	if _, err := ConfigureServe(pairs, "/srv/projects", "/srv/state", create, changed); err != nil {
		t.Fatal(err)
	}
	if !checked || len(created) != 1 || created[0] != "fresh" {
		t.Fatalf("the facade did not delegate: %v", created)
	}
	if announced != 1 {
		t.Fatalf("Changed() reached the announcement path %d times, want 1", announced)
	}
}

func TestProjectsCreatePassesTheError(t *testing.T) {
	facade := &projects{create: func(ctx context.Context, name string) (string, error) { return "", plugin.ErrProjectExists }}
	if _, err := facade.Create(context.Background(), "taken"); !errors.Is(err, plugin.ErrProjectExists) {
		t.Fatalf("Create() = %v, want ErrProjectExists", err)
	}
}

func TestProjectChangedPassesTheError(t *testing.T) {
	boom := errors.New("the announcement path is gone")
	facade := &projects{
		create:  func(ctx context.Context, name string) (string, error) { return "/srv/projects/kept", nil },
		changed: func(ctx context.Context) error { return boom },
	}
	proj, err := facade.Create(context.Background(), "kept")
	if err != nil {
		t.Fatal(err)
	}
	if err := proj.Changed(context.Background()); !errors.Is(err, boom) {
		t.Fatalf("Changed() = %v, want the path's error", err)
	}
}

func TestConfigureServeErrorCarriesTheID(t *testing.T) {
	boom := errors.New("no upstream configured")
	_, err := ConfigureServe([]plugin.Named[plugin.ServePlugin]{
		{ID: "fine", Plugin: &recordingPlugin{}},
		{ID: "broken", Plugin: &recordingPlugin{err: boom}},
	}, "", "", nil, nil)
	if err == nil {
		t.Fatal("ConfigureServe() = nil, want the broken plugin's error")
	}
	if !strings.Contains(err.Error(), "broken") || !errors.Is(err, boom) {
		t.Fatalf("ConfigureServe() = %q, want it to name plugin broken and wrap the cause", err)
	}
}

func TestSlotHTMLAppendsInCallOrder(t *testing.T) {
	ordered := serveFunc(func(s plugin.Serve) error {
		s.AddSlotHTML(plugin.SlotProjectsActions, "<a></a>")
		s.AddSlotHTML(plugin.SlotProjectsActions, "<b></b>")
		return nil
	})
	serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "ordered", Plugin: ordered})
	if got := serves[0].SlotHTML(plugin.SlotProjectsActions); got != "<a></a><b></b>" {
		t.Fatalf("SlotHTML() = %q, want call order", got)
	}
}

func TestAddSlotHTMLRefusesUnknownSlots(t *testing.T) {
	defer func() {
		msg, ok := recover().(string)
		if !ok || !strings.Contains(msg, "typo") || !strings.Contains(msg, "misplaced-slot") {
			t.Fatalf("panicked with %v, want the plugin id and the slot name", msg)
		}
	}()
	_, _ = ConfigureServe([]plugin.Named[plugin.ServePlugin]{{ID: "typo", Plugin: &recordingPlugin{
		slots: map[string]string{"misplaced-slot": "<x></x>"},
	}}}, "", "", nil, nil)
	t.Fatal("an unknown slot was accepted")
}

func TestAddCustomElementRefusesDuplicateNames(t *testing.T) {
	defer func() {
		msg, ok := recover().(string)
		if !ok || !strings.Contains(msg, "doubled") || !strings.Contains(msg, "widget") {
			t.Fatalf("panicked with %v, want the plugin id and the element name", msg)
		}
	}()
	_, _ = ConfigureServe([]plugin.Named[plugin.ServePlugin]{{ID: "doubled", Plugin: &recordingPlugin{
		elements: [][2]string{{"widget", "widget.js"}, {"widget", "other.js"}},
	}}}, "", "", nil, nil)
	t.Fatal("a duplicate element name was accepted")
}

func TestSealedServeRefuses(t *testing.T) {
	p := &recordingPlugin{}
	configured(t, plugin.Named[plugin.ServePlugin]{ID: "kept", Plugin: p})
	defer func() {
		msg, ok := recover().(string)
		if !ok || !strings.Contains(msg, "kept") {
			t.Fatalf("a late Add panicked with %v, want the plugin id", msg)
		}
	}()
	p.got.AddCustomElement("late", "late.js")
	t.Fatal("a sealed Serve accepted an Add")
}
