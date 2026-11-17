package web

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/plugin"
)

// lampPlugin is an optional compiled plugin with routes, an element, a slot
// and a page.
type lampPlugin struct{}

func (lampPlugin) ConfigureServe(s plugin.Serve) error {
	s.AddAssets(fstest.MapFS{"widget.js": &fstest.MapFile{Data: []byte("export default class extends HTMLElement {}")}})
	widget := s.AddCustomElement("widget", "widget.js")
	routes := s.AddRoutes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("lit " + r.URL.Path)) }))
	s.AddSlotHTML(plugin.SlotProjectsActions, `<`+widget.Name()+` data-state-url="`+routes.Path("/state")+`">lamp slot</`+widget.Name()+`>`)
	s.AddPage(plugin.Page{Title: "Lamp", Icon: "ti-bulb", HTML: `<` + widget.Name() + ` class="d-block">lamp page</` + widget.Name() + `>`})
	return nil
}

// optionalShell is externalShell wired with fake always on and lamp optional.
func optionalShell(t *testing.T) (*gin.Engine, *pluginhost.Host) {
	t.Helper()
	r, _, host, _ := externalShell(t, func(cfg *pluginhost.HostConfig) {
		cfg.Wiring = []plugin.Named[plugin.ServePlugin]{
			{ID: "fake", Plugin: subtreePlugin{}},
			{ID: "lamp", Plugin: lampPlugin{}, Optional: true},
		}
	})
	return r, host
}

// Settings › Plugins lists the compiled plugins as built in, the optional one
// with only its switch, the one always on without actions.
func TestThePluginsPageListsBuiltInPlugins(t *testing.T) {
	r, _ := optionalShell(t)
	page := shellDo(t, r, http.MethodGet, settingsPluginsPath, "").Body.String()
	builtIn := between(t, page, `data-plugin-builtin>`, `data-plugin-installed>`)
	fake := between(t, builtIn, `data-plugin-row="fake"`, `data-plugin-row="lamp"`)
	if !strings.Contains(fake, ">Always on<") || !strings.Contains(fake, "always on") || strings.Contains(fake, "data-plugin-action") {
		t.Fatalf("the required row reads %s", fake)
	}
	lamp := between(t, builtIn, `data-plugin-row="lamp"`, `data-plugin-actions`)
	if !strings.Contains(lamp, `data-plugin-state="disabled"`) || !strings.Contains(lamp, ">Disabled<") || !strings.Contains(lamp, "Built in, optional") {
		t.Fatalf("the optional row reads %s", lamp)
	}
	actions := between(t, builtIn[strings.Index(builtIn, `data-plugin-row="lamp"`):], `data-plugin-actions>`, `</div>`)
	if !strings.Contains(actions, `data-plugin-action="enable"`) || strings.Contains(actions, `"remove"`) || strings.Contains(actions, `"log"`) || strings.Contains(actions, `"restart"`) {
		t.Fatalf("the optional row offers %s", actions)
	}
	if !strings.Contains(between(t, page, `data-plugin-installed>`, `data-plugin-list`), `data-plugin-row="hello"`) {
		t.Fatal("the external plugin left the Installed list")
	}
}

// Switched on, the lamp's surfaces appear and its element loads through its
// mount, not the import map. Switched off they leave and its page says it is
// not running, under 200. Neither compiled plugin can be removed, the one
// always on cannot be switched.
func TestAnOptionalPluginComesAndGoesLive(t *testing.T) {
	r, host := optionalShell(t)
	off := func() {
		t.Helper()
		page := shellDo(t, r, http.MethodGet, "/plugins/lamp/", "")
		if page.Code != http.StatusOK || !strings.Contains(between(t, page.Body.String(), `data-region="page-lamp"`, "</dc-plugin-region>"), "lamp is not running.") {
			t.Fatalf("the switched off lamp's page answered %d without its notice", page.Code)
		}
		if code := shellDo(t, r, http.MethodGet, "/plugins/lamp/state", "").Code; code != http.StatusNotFound {
			t.Fatalf("the switched off lamp's route answered %d", code)
		}
		if slot := shellDo(t, r, http.MethodGet, "/plugin-regions/slot-projects-actions", "").Body.String(); strings.Contains(slot, "lamp") {
			t.Fatalf("the slot holds the switched off lamp: %s", slot)
		}
		if rail := shellDo(t, r, http.MethodGet, "/plugin-regions/rail", "").Body.String(); strings.Contains(rail, "/plugins/lamp/") {
			t.Fatalf("the rail holds the switched off lamp: %s", rail)
		}
	}
	off()

	if rec := postForm(t, r, settingsPluginsPath, url.Values{"form": {"enable"}, "id": {"lamp"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("switching the lamp on answered %d", rec.Code)
	}
	waitPlugin(t, host, "lamp", pluginhost.StateRunning)
	page := shellDo(t, r, http.MethodGet, "/plugins/lamp/", "").Body.String()
	mount := regexp.MustCompile(`<div class="dc-plugin-mount" data-region-key="lamp@\d+" data-dc-module="(/plugins/lamp/elements\.[0-9a-f]{12}\.js)"><lamp-widget class="d-block">lamp page</lamp-widget></div>`).FindStringSubmatch(page)
	if mount == nil || !strings.Contains(page, `<h1 class="dc-work-title">Lamp</h1>`) {
		t.Fatalf("the lamp's page holds %s", between(t, page, `data-region="page-lamp"`, "</dc-plugin-region>"))
	}
	if strings.Contains(between(t, page, `<script type="importmap"`, "</script>"), "lamp-widget") {
		t.Fatal("the lamp's element is in the import map, which a page fixes when it loads")
	}
	starter := shellDo(t, r, http.MethodGet, mount[1], "")
	if starter.Code != http.StatusOK || !strings.Contains(starter.Body.String(), `customElements.define("lamp-widget", C`) {
		t.Fatalf("the lamp's starter answered %d %s", starter.Code, starter.Body.String())
	}
	if slot := shellDo(t, r, http.MethodGet, "/plugin-regions/slot-projects-actions", "").Body.String(); !strings.Contains(slot, `<lamp-widget data-state-url="/plugins/lamp/state">lamp slot</lamp-widget>`) {
		t.Fatalf("the slot holds %s", slot)
	}
	if rail := shellDo(t, r, http.MethodGet, "/plugin-regions/rail", "").Body.String(); !strings.Contains(rail, `href="/plugins/lamp/"`) || strings.Index(rail, "/plugins/fake/") > strings.Index(rail, "/plugins/lamp/") {
		t.Fatalf("the rail does not hold the lamp after the fake: %s", rail)
	}
	if state := shellDo(t, r, http.MethodGet, "/plugins/lamp/state", ""); state.Code != http.StatusOK || state.Body.String() != "lit /state" {
		t.Fatalf("the lamp's route answered %d %q", state.Code, state.Body.String())
	}

	if rec := postForm(t, r, settingsPluginsPath, url.Values{"form": {"disable"}, "id": {"lamp"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("switching the lamp off answered %d", rec.Code)
	}
	off()
	if code := shellDo(t, r, http.MethodGet, mount[1], "").Code; code != http.StatusNotFound {
		t.Fatalf("the switched off lamp's starter answered %d", code)
	}

	for form, want := range map[string]string{
		"remove lamp":  "lamp is built in",
		"restart lamp": "lamp is built in",
		"disable fake": "fake is built in and always on.",
		"remove fake":  "fake is built in and always on.",
	} {
		action, id, _ := strings.Cut(form, " ")
		rec := postForm(t, r, settingsPluginsPath, url.Values{"form": {action}, "id": {id}})
		if rec.Code != http.StatusConflict || !strings.Contains(between(t, rec.Body.String(), `data-plugin-refusal>`, "</div>"), want) {
			t.Fatalf("%s answered %d without %q at its row", form, rec.Code, want)
		}
	}
	if code := shellDo(t, r, http.MethodGet, "/plugins/fake/page", "").Code; code != http.StatusOK {
		t.Fatalf("the fake answered %d after the refusals", code)
	}
}
