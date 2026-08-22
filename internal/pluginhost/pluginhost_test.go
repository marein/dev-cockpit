package pluginhost

import (
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/marein/dev-cockpit/plugin"
)

// testPlugin adds what its fields carry.
type testPlugin struct {
	routeBody string
	assets    fstest.MapFS
	elements  [][2]string
	slots     map[string]string
}

func (p *testPlugin) ConfigureServe(s plugin.Serve) error {
	if p.routeBody != "" {
		body := p.routeBody
		s.AddRoutes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.WriteString(w, body+" "+r.URL.Path)
		}))
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

// configured builds the sealed Serves the cockpit would hand around.
func configured(t *testing.T, plugins ...plugin.Named[plugin.ServePlugin]) []*Serve {
	t.Helper()
	serves, err := ConfigureServe(plugins, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return serves
}

// resolve stands in for the asset manifest: it marks a raw path resolved.
func resolve(raw string) string { return raw + "?hashed" }

func TestServesHTTP(t *testing.T) {
	serves := configured(t,
		plugin.Named[plugin.ServePlugin]{ID: "bare", Plugin: &testPlugin{}},
		plugin.Named[plugin.ServePlugin]{ID: "routed", Plugin: &testPlugin{routeBody: "answer"}},
		plugin.Named[plugin.ServePlugin]{ID: "files", Plugin: &testPlugin{assets: fstest.MapFS{}}},
		plugin.Named[plugin.ServePlugin]{ID: "elemental", Plugin: &testPlugin{elements: [][2]string{{"widget", "widget.js"}}}},
	)
	if ServesHTTP(serves[0]) {
		t.Fatal("a plugin that added nothing must not mount a subtree")
	}
	for _, s := range serves[1:] {
		if !ServesHTTP(s) {
			t.Fatalf("plugin %s must mount its subtree", s.ID())
		}
	}
}

func TestStarterDefinesEveryElement(t *testing.T) {
	serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "demo", Plugin: &testPlugin{
		elements: [][2]string{{"widget", "widget.js"}, {"list", "list.js"}, {"badge", "widget.js"}},
	}})
	starter, ok := Starter(serves[0], resolve)
	if !ok {
		t.Fatal("Starter() answered nothing for a plugin with elements")
	}
	if got := strings.Count(starter, `import C0 from "/plugins/demo/assets/widget.js?hashed";`); got != 1 {
		t.Fatalf("starter imports widget.js %d times:\n%s", got, starter)
	}
	if !strings.Contains(starter, `import C1 from "/plugins/demo/assets/list.js?hashed";`) {
		t.Fatalf("starter misses the list.js import:\n%s", starter)
	}
	for _, define := range []string{
		`if (!customElements.get("demo-widget")) customElements.define("demo-widget", C0);`,
		`if (!customElements.get("demo-list")) customElements.define("demo-list", C1);`,
		`if (!customElements.get("demo-badge")) customElements.define("demo-badge", C0);`,
	} {
		if !strings.Contains(starter, define) {
			t.Fatalf("starter misses %q:\n%s", define, starter)
		}
	}
}

func TestStarterWithoutElements(t *testing.T) {
	serves := configured(t, plugin.Named[plugin.ServePlugin]{ID: "bare", Plugin: &testPlugin{}})
	if _, ok := Starter(serves[0], resolve); ok {
		t.Fatal("Starter() answered something for a plugin without elements")
	}
}

func TestElements(t *testing.T) {
	serves := configured(t,
		plugin.Named[plugin.ServePlugin]{ID: "bare", Plugin: &testPlugin{}},
		plugin.Named[plugin.ServePlugin]{ID: "demo", Plugin: &testPlugin{elements: [][2]string{{"widget", "widget.js"}, {"list", "list.js"}}}},
		plugin.Named[plugin.ServePlugin]{ID: "other", Plugin: &testPlugin{elements: [][2]string{{"badge", "badge.js"}}}},
	)
	got := Elements(serves, resolve)
	want := []Element{
		{Name: "demo-widget", URL: "/plugins/demo/elements.js?hashed"},
		{Name: "demo-list", URL: "/plugins/demo/elements.js?hashed"},
		{Name: "other-badge", URL: "/plugins/other/elements.js?hashed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Elements() = %v, want %v", got, want)
	}
}

func TestSlotHTML(t *testing.T) {
	serves := configured(t,
		plugin.Named[plugin.ServePlugin]{ID: "bare", Plugin: &testPlugin{}},
		plugin.Named[plugin.ServePlugin]{ID: "demo", Plugin: &testPlugin{slots: map[string]string{plugin.SlotProjectsActions: "<demo-widget></demo-widget>"}}},
		plugin.Named[plugin.ServePlugin]{ID: "other", Plugin: &testPlugin{slots: map[string]string{plugin.SlotProjectsActions: "<other-widget></other-widget>"}}},
	)
	got := SlotHTML(serves, plugin.SlotProjectsActions)
	if string(got) != "<demo-widget></demo-widget><other-widget></other-widget>" {
		t.Fatalf("SlotHTML() = %q", got)
	}
	if empty := SlotHTML(serves, "unknown-slot"); empty != "" {
		t.Fatalf("unknown slot answered %q, want empty", empty)
	}
}
