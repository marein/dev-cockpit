package pluginhost

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/plugin"
)

const goodManifest = `{
  "protocol": 1, "id": "demo", "name": "Demo", "version": "1.0.0", "run": "run.sh", "assets": "assets",
  "elements": [{"name": "button", "module": "button.js"}],
  "slots": [{"slot": "projects-actions", "html": "slot.html"}],
  "page": {"title": "Demo", "icon": "ti-box", "html": "page.html"},
  "settings": {"values": {"demo-key": "v"}},
  "memory": {"dir": "memory"}
}`

func TestTheManifestGate(t *testing.T) {
	if _, err := ParseManifest(strings.NewReader(goodManifest)); err != nil {
		t.Fatalf("the good manifest is refused: %v", err)
	}
	for _, tc := range []struct{ from, to, want string }{
		{`"protocol": 1`, `"protocol": 2`, "uses protocol 2"},
		{`"id": "demo"`, `"id": "Demo"`, "lowercase words"},
		{`"id": "demo"`, `"id": "` + strings.Repeat("a", 41) + `"`, "at most 40"},
		{`"name": "Demo"`, `"name": "a\u0007b"`, "plain text"},
		{`"run": "run.sh"`, `"run": "../run.sh"`, "relative path inside"},
		{`"run": "run.sh"`, `"run": "/bin/sh"`, "relative path inside"},
		{`"run": "run.sh"`, `"run": "./run.sh"`, "relative path inside"},
		{`"assets": "assets"`, `"assets": "../.."`, "relative path inside"},
		{`"module": "button.js"`, `"module": "button.css"`, ".js file"},
		{`"slot": "projects-actions"`, `"slot": "nowhere"`, "no slot named"},
		{`"html": "slot.html"`, `"html": "../slot.html"`, "relative path inside"},
		{`"html": "slot.html"`, `"html": ""`, "html of the slot projects-actions path is missing"},
		{`"html": "page.html"`, `"html": "/etc/passwd"`, "relative path inside"},
		{`"html": "page.html"`, `"element": "button"`, "is invalid"},
		{`"icon": "ti-box"`, `"icon": "ti-box extra"`, "Tabler icon"},
		{`"dir": "memory"`, `"dir": "/root"`, "relative path inside"},
		{`"version": "1.0.0"`, `"version": "1.0.0", "html": "<b>"`, "is invalid"},
		{`"demo-key": "v"`, `"demo key": "v"`, "without spaces"},
		{`"dir": "memory"}`, `"dir": "memory"}, "events": ["weather"]`, "unknown or listed twice"},
		{`"dir": "memory"}`, `"dir": "memory"}, "events": ["job", "job"]`, "listed twice"},
	} {
		raw := strings.Replace(goodManifest, tc.from, tc.to, 1)
		if _, err := ParseManifest(strings.NewReader(raw)); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%s: got %v, want a refusal with %q", tc.to, err, tc.want)
		}
	}
	if _, err := ParseManifest(strings.NewReader(strings.Repeat(" ", maxManifestBytes+1))); err == nil {
		t.Fatal("an oversized manifest is taken")
	}
}

// Markup gets its base filled in and stays inside its place: what it leaves
// open is closed, extra end tags and plaintext elements are dropped.
func TestMarkupIsReadOnceAndKeptInsideItsPlace(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"open.html":     `<a href="{{base}}/items" data-items="{{base}}/items">Items</a><div class="d-block"><span>left open`,
		"closing.html":  `<b>x</b></div></div></main><!-- unclosed`,
		"plain.html":    `<p>before</p><div><plaintext>swallows</div>`,
		"big.html":      strings.Repeat("x", maxMarkupBytes+1),
		"latin.html":    "caf\xe9",
		"assets/x.html": "<i>x</i>",
	}
	for name, content := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(dir, "out.html")); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for name, want := range map[string]string{
		"open.html":    `<a href="/plugins/demo/items" data-items="/plugins/demo/items">Items</a><div class="d-block"><span>left open</span></div>`,
		"closing.html": `<b>x</b><!-- unclosed-->`,
		"plain.html":   `<p>before</p><div></div>`,
	} {
		got, err := readMarkup(root, name, "/plugins/demo")
		if err != nil || got != want {
			t.Fatalf("readMarkup(%s) = %q, %v, want %q", name, got, err, want)
		}
	}
	for name, refusal := range map[string]string{
		"big.html":     "larger than 64 KiB",
		"latin.html":   "not UTF-8",
		"out.html":     "leaves the plugin directory",
		"assets":       "not a file",
		"missing.html": "is missing",
	} {
		if _, err := readMarkup(root, name, "/plugins/demo"); err == nil || !strings.Contains(err.Error(), refusal) {
			t.Fatalf("readMarkup(%s) = %v, want a refusal with %q", name, err, refusal)
		}
	}
}

// A live plugin's markup gets one mount per slot carrying the plugin load and
// its starter, none without elements. It stays out of the compiled surfaces
// and leaves when the plugin stops answering.
func TestAnExternalMountHoldsTheMarkup(t *testing.T) {
	running := true
	s := &Serve{
		id:       "demo",
		elements: []Registration{{Name: "demo-button", Module: "button.js"}},
		slots:    map[string][]string{plugin.SlotProjectsActions: {`<demo-button></demo-button>`, `<a href="/plugins/demo/">Demo</a>`}},
		page:     &plugin.Page{Title: "Demo", Icon: "ti-box", HTML: `<demo-button></demo-button>`},
		live:     &liveServe{generation: 7, running: func() bool { return running }},
	}
	resolve := func(p string) string { return p + "?hashed" }
	views := SlotMounts([]*Serve{s}, plugin.SlotProjectsActions, resolve)
	want := `<div class="dc-plugin-mount" data-region-key="demo@7" data-dc-module="/plugins/demo/elements.js?hashed"><demo-button></demo-button><a href="/plugins/demo/">Demo</a></div>`
	if len(views) != 1 || string(views[0].HTML) != want || views[0].Key != "demo@7" {
		t.Fatalf("SlotMounts() = %+v, want one mount\n%s", views, want)
	}
	if page, ok := PageMountView(s, resolve); !ok || string(page.HTML) != `<div class="dc-plugin-mount" data-region-key="demo@7" data-dc-module="/plugins/demo/elements.js?hashed"><demo-button></demo-button></div>` {
		t.Fatalf("PageMountView() = %q %v", page.HTML, ok)
	}
	if Elements([]*Serve{s}, resolve) != nil || SlotHTML([]*Serve{s}, plugin.SlotProjectsActions) != "" {
		t.Fatal("the external markup or element joined a compiled surface")
	}
	s.elements = nil
	if views := SlotMounts([]*Serve{s}, plugin.SlotProjectsActions, resolve); len(views) != 1 || strings.Contains(string(views[0].HTML), "data-dc-module") {
		t.Fatalf("a plugin without elements names a starter: %+v", views)
	}
	running = false
	if _, ok := PageMountView(s, resolve); ok || len(SlotMounts([]*Serve{s}, plugin.SlotProjectsActions, resolve)) != 0 {
		t.Fatal("a plugin that does not answer still shows its markup")
	}
}
