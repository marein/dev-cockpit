package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/plugin"
)

// pagePlugin adds one page and nothing else.
type pagePlugin plugin.Page

func (p pagePlugin) ConfigureServe(s plugin.Serve) error {
	s.AddPage(plugin.Page(p))
	return nil
}

// pagedShell serves the plugins' pages and the phone's sheets to a signed in
// browser, in the layout the plugins given fill.
func pagedShell(t *testing.T, plugins ...plugin.Named[plugin.ServePlugin]) *gin.Engine {
	t.Helper()
	r, s, _ := assistantEntryServer(t, assistantA)
	serves, err := pluginhost.ConfigureServe(plugins, "", t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("ConfigureServe() = %v", err)
	}
	withShell(t, r, s, serves)
	signedIn := func(c *gin.Context) { ginsessions.Default(c).Set(sessionUserKey, "admin") }
	r.Any("/plugins/:id/*path", signedIn, s.servePlugins)
	r.GET("/ctx/:area", signedIn, s.handleCtx)
	r.GET("/assistants/:id", signedIn, s.handleAssistantPage)
	return r
}

func pagedGet(t *testing.T, r *gin.Engine, path string) string {
	t.Helper()
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s answered %d", path, rec.Code)
	}
	return rec.Body.String()
}

// between answers the markup from the first start up to the end that follows.
func between(t *testing.T, body, start, end string) string {
	t.Helper()
	_, rest, ok := strings.Cut(body, start)
	if !ok {
		t.Fatalf("no %q in the page", start)
	}
	part, _, _ := strings.Cut(rest, end)
	return part
}

var labeledPages = []plugin.Named[plugin.ServePlugin]{
	{ID: "tickets", Plugin: pagePlugin{Title: "Ticket board", Icon: "ti-ticket", HTML: "<ticket-board></ticket-board>", NavLabel: "Tickets"}},
	{ID: "long", Plugin: pagePlugin{Title: "Release checklist of every team", Icon: "ti-checklist"}},
}

// The rail and the sheet name a page by its NavLabel, the Title when there is
// none, and the page's heading is always the Title.
func TestPluginPageNavLabelFallsBackToTheTitle(t *testing.T) {
	r := pagedShell(t, labeledPages...)

	body := pagedGet(t, r, "/plugins/tickets/")
	if !strings.Contains(body, `<h1 class="dc-work-title">Ticket board</h1>`) {
		t.Fatal("the page's heading is not its Title")
	}
	rail := between(t, body, `<nav class="dc-rail `, "</nav>")
	for _, want := range []string{
		`<a class="dc-rail-btn active" href="/plugins/tickets/" title="Ticket board" aria-label="Ticket board" data-region-key="tickets"><i class="ti ti-ticket icon" aria-hidden="true"></i><span class="dc-trunc mw-100">Tickets</span></a>`,
		`<a class="dc-rail-btn" href="/plugins/long/" title="Release checklist of every team" aria-label="Release checklist of every team" data-region-key="long"><i class="ti ti-checklist icon" aria-hidden="true"></i><span class="dc-trunc mw-100">Release checklist of every team</span></a>`,
	} {
		if !strings.Contains(rail, want) {
			t.Fatalf("the rail misses %s", want)
		}
	}

	sheet := between(t, pagedGet(t, r, "/ctx/cockpit?path=/plugins/tickets/"), `data-cockpit-section="pages"`, "</section>")
	tickets := between(t, sheet, `href="/plugins/tickets/"`, "</a>")
	if !strings.Contains(tickets, " active\"") || !strings.Contains(tickets, `<span class="flex-fill dc-trunc">Tickets</span>`) {
		t.Fatalf("the sheet's row of the open page is not active under its NavLabel: %s", tickets)
	}
	long := between(t, sheet, `href="/plugins/long/"`, "</a>")
	if strings.Contains(long, " active\"") || !strings.Contains(long, `<span class="flex-fill dc-trunc">Release checklist of every team</span>`) {
		t.Fatalf("the sheet's other row is active or not under its Title: %s", long)
	}
}

// The tab bar keeps its five areas whatever the plugins add, and its Cockpit
// tab stands active on a plugin's page, the sheet it is reached through.
func TestTabBarCarriesNoPluginPage(t *testing.T) {
	for _, tc := range []struct {
		name    string
		plugins []plugin.Named[plugin.ServePlugin]
	}{{"none", nil}, {"two", labeledPages}} {
		r := pagedShell(t, tc.plugins...)
		bodies := map[string]string{"/assistants/" + assistantA: pagedGet(t, r, "/assistants/"+assistantA)}
		for _, p := range tc.plugins {
			bodies[p.ID] = pagedGet(t, r, "/plugins/"+p.ID+"/")
		}
		for page, body := range bodies {
			bar := between(t, body, `<nav class="dc-tabbar `, "</nav>")
			if n := strings.Count(bar, `class="dc-tabbar-btn`); n != 5 || strings.Contains(bar, "/plugins/") {
				t.Fatalf("%s: the tab bar on %s holds %d tabs or a plugin page", tc.name, page, n)
			}
			cockpitActive := strings.Contains(bar, `class="dc-tabbar-btn active" data-ctx-area="cockpit"`)
			if wantActive := !strings.HasPrefix(page, "/"); cockpitActive != wantActive {
				t.Fatalf("%s: the Cockpit tab on %s is active %v, want %v", tc.name, page, cockpitActive, wantActive)
			}
		}
		sheet := pagedGet(t, r, "/ctx/cockpit?path=/")
		if got := strings.Contains(sheet, `data-cockpit-section="pages"`); got != (len(tc.plugins) > 0) {
			t.Fatalf("%s: the sheet shows the plugin pages section %v", tc.name, got)
		}
	}
}
