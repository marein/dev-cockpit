package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/internal/web/render"
	"github.com/marein/dev-cockpit/plugin"
)

// externalShell serves every plugin surface to a signed in browser, the
// example plugin running as a real Python process beside a compiled plugin.
func externalShell(t *testing.T, configure ...func(*pluginhost.HostConfig)) (*gin.Engine, *Server, *pluginhost.Host, <-chan eventbus.Event) {
	t.Helper()
	r, s, _ := assistantEntryServer(t, assistantA)
	compiled, err := pluginhost.ConfigureServe([]plugin.Named[plugin.ServePlugin]{{ID: "fake", Plugin: subtreePlugin{}}}, "", t.TempDir(), nil, nil, nil)
	if err != nil {
		t.Fatalf("ConfigureServe() = %v", err)
	}
	withShell(t, r, s, compiled)
	stateDir := s.cfg.StateDir
	if err := os.CopyFS(filepath.Join(pluginhost.PluginsDir(stateDir), "hello"), os.DirFS("../../examples/plugins/hello")); err != nil {
		t.Fatalf("copy the example: %v", err)
	}
	s.bus = eventbus.New()
	events, cancel := s.bus.Subscribe()
	t.Cleanup(cancel)
	projectsDir := t.TempDir()
	assistants := &pluginhost.Assistants{}
	assistants.Bind(s.PluginAssistants())
	cfg := pluginhost.HostConfig{
		StateDir:    stateDir,
		ProjectsDir: projectsDir,
		Compiled:    compiled,
		CreateProject: func(ctx context.Context, name string) (string, error) {
			dir := filepath.Join(projectsDir, name)
			return dir, os.Mkdir(dir, 0o755)
		},
		ProjectChanged: func(ctx context.Context) error { return nil },
		Assistants:     assistants,
		CSRFField:      CSRFField,
	}
	for _, change := range configure {
		change(&cfg)
	}
	host := pluginhost.NewHost(cfg)
	s.plugins = pluginhost.NewSet(compiled, host)
	r.SetHTMLTemplate(render.HTMLTemplate(s.assets.assetPath, "test", "test", s.plugins, nil))
	host.Scan()
	host.Start(s.PluginHooks())
	t.Cleanup(host.Close)
	signedIn := func(c *gin.Context) { ginsessions.Default(c).Set(sessionUserKey, "admin") }
	r.Any("/plugins/:id/*path", signedIn, s.servePlugins)
	r.GET("/plugin-regions/:region", signedIn, s.handlePluginRegion)
	r.GET(settingsPluginsPath, signedIn, s.handleSettingsPlugins)
	r.POST(settingsPluginsPath, signedIn, s.handleSettingsPluginsSave)
	r.GET(settingsPluginNewPath, signedIn, s.handleSettingsPluginNew)
	r.POST(settingsPluginNewPath, signedIn, s.handleSettingsPluginNewSave)
	r.GET("/ctx/:area", signedIn, s.handleCtx)
	waitPlugin(t, host, "hello", pluginhost.StateRunning)
	return r, s, host, events
}

func waitPlugin(t *testing.T, host *pluginhost.Host, id string, want pluginhost.State) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		st, _ := host.Status(id)
		if st.State == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin %s stands %q (%s), want %q", id, st.State, st.Reason, want)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// closeNotifier lets the reverse proxy watch the recorder for a client that
// leaves, gin passes CloseNotify to the writer below and fails without it.
type closeNotifier struct {
	*httptest.ResponseRecorder
}

func (closeNotifier) CloseNotify() <-chan bool { return make(chan bool) }

func shellRecord(r *gin.Engine, req *http.Request) *httptest.ResponseRecorder {
	rec := closeNotifier{httptest.NewRecorder()}
	r.ServeHTTP(rec, req)
	return rec.ResponseRecorder
}

func shellDo(t *testing.T, r *gin.Engine, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	return shellRecord(r, req)
}

// An external plugin reaches its page, the rail, the Cockpit sheet, its slot,
// its hashed assets with the starter and, through its process, the cockpit's
// project and assistant surfaces. The compiled plugin beside it keeps
// working.
func TestAnExternalPluginReachesEverySurface(t *testing.T) {
	r, _, _, _ := externalShell(t)

	page := shellDo(t, r, http.MethodGet, "/plugins/hello/", "")
	body := page.Body.String()
	if page.Code != http.StatusOK || !strings.Contains(body, `<h1 class="dc-work-title">Hello notes</h1>`) {
		t.Fatalf("the page answered %d without its heading", page.Code)
	}
	mount := regexp.MustCompile(`<div class="dc-plugin-mount" data-region-key="hello@\d+" data-dc-module="(/plugins/hello/elements\.[0-9a-f]{12}\.js)"><hello-board class="d-block dc-narrow" data-notes-url="/plugins/hello/notes" data-events-url="/plugins/hello/events" data-assistants-url="/plugins/hello/assistants">\s*<form `).FindStringSubmatch(body)
	if mount == nil || !strings.Contains(body, `placeholder="Write a note"`) {
		t.Fatalf("the page does not carry the board's markup: %s", between(t, body, `data-region="page-hello"`, "</dc-plugin-region>"))
	}
	rail := between(t, body, `<nav class="dc-rail `, "</nav>")
	for _, want := range []string{`href="/plugins/hello/"`, `href="/plugins/fake/"`, `class="dc-rail-btn active" href="/plugins/hello/"`} {
		if !strings.Contains(rail, want) {
			t.Fatalf("the rail misses %s", want)
		}
	}
	if sheet := shellDo(t, r, http.MethodGet, "/ctx/cockpit?path=/plugins/hello/", "").Body.String(); !strings.Contains(sheet, `<span class="flex-fill dc-trunc">Hello</span>`) {
		t.Fatal("the Cockpit sheet lists no Hello page")
	}

	starter := shellDo(t, r, http.MethodGet, mount[1], "")
	module := regexp.MustCompile(`import C\d from "(/plugins/hello/assets/board\.[0-9a-f]{12}\.js)"`).FindStringSubmatch(starter.Body.String())
	if starter.Code != http.StatusOK || module == nil || !strings.Contains(starter.Body.String(), `customElements.define("hello-board", C`) {
		t.Fatalf("the starter answered %d %s", starter.Code, starter.Body.String())
	}
	asset := shellDo(t, r, http.MethodGet, module[1], "")
	if asset.Code != http.StatusOK || !strings.Contains(asset.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("the hashed module answered %d %q", asset.Code, asset.Header().Get("Cache-Control"))
	}

	slot := shellDo(t, r, http.MethodGet, "/plugin-regions/slot-projects-actions", "").Body.String()
	if !strings.Contains(slot, `<hello-button data-projects-url="/plugins/hello/projects">`) || !strings.Contains(slot, `title="New project from Hello"`) {
		t.Fatalf("the slot region holds %s", slot)
	}

	notes := shellDo(t, r, http.MethodPost, "/plugins/hello/notes", `{"text":"from the test"}`)
	if notes.Code != http.StatusOK || !strings.Contains(notes.Body.String(), "from the test") {
		t.Fatalf("the plugin's route answered %d %s", notes.Code, notes.Body.String())
	}
	created := shellDo(t, r, http.MethodPost, "/plugins/hello/projects", `{"name":"made-by-hello"}`)
	var project map[string]string
	_ = json.Unmarshal(created.Body.Bytes(), &project)
	if created.Code != http.StatusCreated || project["name"] != "made-by-hello" {
		t.Fatalf("the plugin's create answered %d %s", created.Code, created.Body.String())
	}
	coders := shellDo(t, r, http.MethodGet, "/plugins/hello/assistants/coders", "")
	if coders.Code != http.StatusOK || strings.Join(strings.Fields(coders.Body.String()), " ") != `{"coders": [{"id": "claude", "name": "Claude", "default": true}]}` {
		t.Fatalf("the plugin's coder list answered %d %s", coders.Code, coders.Body.String())
	}
	started := shellDo(t, r, http.MethodPost, "/plugins/hello/assistants", `{"name":"Made by hello","coder_id":"claude"}`)
	var answer struct {
		Assistant map[string]any `json:"assistant"`
	}
	_ = json.Unmarshal(started.Body.Bytes(), &answer)
	assistant := answer.Assistant
	id, _ := assistant["id"].(string)
	if started.Code != http.StatusCreated || id == "" || assistant["name"] != "Made by hello" || assistant["named"] != true || assistant["coder_id"] != "claude" || assistant["url"] != "/assistants/"+id {
		t.Fatalf("the plugin's assistant start answered %d %s", started.Code, started.Body.String())
	}
	listed := shellDo(t, r, http.MethodGet, "/plugins/hello/assistants", "")
	var list struct {
		Assistants []plugin.Assistant `json:"assistants"`
	}
	_ = json.Unmarshal(listed.Body.Bytes(), &list)
	if listed.Code != http.StatusOK || len(list.Assistants) != 2 || list.Assistants[0].ID != id || list.Assistants[1].ID != assistantA {
		t.Fatalf("the plugin's assistant list answered %d %s", listed.Code, listed.Body.String())
	}
	if last := shellDo(t, r, http.MethodGet, "/plugins/hello/assistants/"+assistantA+"/last-message", ""); last.Code != http.StatusOK || !strings.Contains(last.Body.String(), `"id": ""`) {
		t.Fatalf("an assistant without messages read %d %s", last.Code, last.Body.String())
	}
	if gone := shellDo(t, r, http.MethodGet, "/plugins/hello/assistants/..%2Fescape/last-message", ""); gone.Code != http.StatusNotFound || !strings.Contains(gone.Body.String(), plugin.ErrAssistantNotFound.Error()) {
		t.Fatalf("a missing assistant read %d %s", gone.Code, gone.Body.String())
	}
	if fake := shellDo(t, r, http.MethodGet, "/plugins/fake/page", ""); fake.Code != http.StatusOK || fake.Body.String() != "plugin page" {
		t.Fatalf("the compiled plugin answered %d %q", fake.Code, fake.Body.String())
	}
}

// A disabled plugin leaves every surface, the event names the new version and
// its row says disabled. Enabled again it is back.
func TestADisabledPluginLeavesEverySurface(t *testing.T) {
	r, _, host, events := externalShell(t)
	if err := host.SetEnabled("hello", false); err != nil {
		t.Fatal(err)
	}
	seen := false
	for !seen {
		select {
		case ev := <-events:
			data, _ := ev.Data.(map[string]string)
			seen = ev.Type == "plugins" && data["version"] == host.Version()
		case <-time.After(5 * time.Second):
			t.Fatal("no plugins event named the new version")
		}
	}
	page := shellDo(t, r, http.MethodGet, "/plugins/hello/", "")
	if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), `<h1 class="dc-work-title">Hello notes</h1>`) || !strings.Contains(between(t, page.Body.String(), `data-region="page-hello"`, "</dc-plugin-region>"), "Hello is not running.") {
		t.Fatalf("the disabled plugin's page answered %d without its notice", page.Code)
	}
	if region := shellDo(t, r, http.MethodGet, "/plugin-regions/page-hello", ""); region.Code != http.StatusOK || !strings.Contains(region.Body.String(), "Hello is not running.") || strings.Contains(region.Body.String(), "hello-board") {
		t.Fatalf("the disabled plugin's page region answered %d %s", region.Code, region.Body.String())
	}
	if code := shellDo(t, r, http.MethodGet, "/plugins/hello/notes", "").Code; code != http.StatusNotFound {
		t.Fatalf("the disabled plugin's route answered %d", code)
	}
	if slot := shellDo(t, r, http.MethodGet, "/plugin-regions/slot-projects-actions", "").Body.String(); strings.Contains(slot, "hello-button") {
		t.Fatalf("the slot still holds the disabled plugin: %s", slot)
	}
	if rail := shellDo(t, r, http.MethodGet, "/plugin-regions/rail", "").Body.String(); strings.Contains(rail, "/plugins/hello/") || !strings.Contains(rail, "/plugins/fake/") {
		t.Fatalf("the rail region holds %s", rail)
	}
	row := between(t, shellDo(t, r, http.MethodGet, settingsPluginsPath, "").Body.String(), `data-plugin-row="hello"`, `data-plugin-actions`)
	if !strings.Contains(row, `data-plugin-state="disabled"`) && !strings.Contains(row, ">Disabled<") {
		t.Fatalf("the row reads %s", row)
	}
	if err := host.SetEnabled("hello", true); err != nil {
		t.Fatal(err)
	}
	waitPlugin(t, host, "hello", pluginhost.StateRunning)
	if code := shellDo(t, r, http.MethodGet, "/plugins/hello/notes", "").Code; code != http.StatusOK {
		t.Fatalf("the enabled plugin's route answered %d", code)
	}
	if region := shellDo(t, r, http.MethodGet, "/plugin-regions/page-hello", "").Body.String(); !strings.Contains(region, "<hello-board ") {
		t.Fatalf("the enabled plugin's page region holds %s", region)
	}
}

// A removed plugin's open page says so through its region, a reload under
// 404. A compiled plugin's page and an invalid id have no such region.
func TestARemovedPluginsPageSaysSo(t *testing.T) {
	r, _, host, _ := externalShell(t)
	if err := host.Remove("hello"); err != nil {
		t.Fatal(err)
	}
	region := shellDo(t, r, http.MethodGet, "/plugin-regions/page-hello", "")
	if region.Code != http.StatusOK || !strings.Contains(region.Body.String(), "The plugin is not installed anymore.") {
		t.Fatalf("the removed plugin's page region answered %d %s", region.Code, region.Body.String())
	}
	page := shellDo(t, r, http.MethodGet, "/plugins/hello/", "")
	if page.Code != http.StatusNotFound || !strings.Contains(between(t, page.Body.String(), `data-region="page-hello"`, "</dc-plugin-region>"), "The plugin is not installed anymore.") {
		t.Fatalf("the removed plugin's page answered %d without its notice", page.Code)
	}
	if code := shellDo(t, r, http.MethodGet, "/plugins/hello/notes", "").Code; code != http.StatusNotFound {
		t.Fatalf("the removed plugin's route answered %d", code)
	}
	for _, name := range []string{"page-fake", "page-Bad%20id"} {
		if code := shellDo(t, r, http.MethodGet, "/plugin-regions/"+name, "").Code; code != http.StatusNotFound {
			t.Fatalf("the region %s answered %d", name, code)
		}
	}
}

// The page warns once and names the folder, a pull gets the rows alone, a
// refused install keeps the path and the local socket changes no plugin.
func TestThePluginsPageRefusesWithTheValueKept(t *testing.T) {
	r, _, host, _ := externalShell(t)
	page := shellDo(t, r, http.MethodGet, settingsPluginsPath, "").Body.String()
	if strings.Count(page, "runs as your user") != 1 || !strings.Contains(page, host.Dir()) || !strings.Contains(page, `data-plugin-state="running"`) {
		t.Fatal("the page misses the warning, the folder or the running row")
	}
	pull := httptest.NewRequest(http.MethodGet, settingsPluginsPath, nil)
	pull.Header.Set(pullHeader, "1")
	if rows := shellRecord(r, pull).Body.String(); !strings.HasPrefix(strings.TrimSpace(rows), "<div data-plugin-list>") || !strings.Contains(rows, `data-plugin-row="hello"`) {
		t.Fatalf("the list's pull answered more or less than the rows: %.200s", rows)
	}
	form := "form=install&path=relative%2Fplugin"
	req := httptest.NewRequest(http.MethodPost, settingsPluginsPath, strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := shellRecord(r, req)
	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `value="relative/plugin"`) || !strings.Contains(between(t, body, `data-plugin-path-error>`, "</div>"), "absolute path") {
		t.Fatalf("a refused install answered %d", rec.Code)
	}
	local := httptest.NewRequest(http.MethodPost, settingsPluginsPath, strings.NewReader("form=disable&id=hello"))
	local.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec = shellRecord(r, local.WithContext(context.WithValue(local.Context(), localCallKey, true)))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("the local socket's change answered %d", rec.Code)
	}
	if st, _ := host.Status("hello"); st.State != pluginhost.StateRunning {
		t.Fatalf("the local call moved the plugin to %s", st.State)
	}
}
