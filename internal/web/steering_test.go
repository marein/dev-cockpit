package web

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const (
	steererOne = "22222222-2222-4222-8222-222222222222"
	steererTwo = "33333333-3333-4333-8333-333333333333"
	// steererTitle carries what every output has to escape.
	steererTitle = `Nightly <b>checks</b> & "co"`
)

// steeringServer is a server with two assistants, one named and one that
// never saw a prompt, and the jobs it is handed, saved straight into the
// store.
func steeringServer(t *testing.T, jobs ...assistant.Job) *Server {
	t.Helper()
	stateDir := t.TempDir()
	conversations, _, err := assistant.New(stateDir, noCoders{}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatalf("assistant: %v", err)
	}
	store := assistant.NewStore(stateDir)
	store.Save(assistant.Instance{Summary: assistant.Summary{ID: steererOne, Title: steererTitle, CoderID: "claude"}})
	store.Save(assistant.Instance{Summary: assistant.Summary{ID: steererTwo, Title: assistant.DefaultTitle, CoderID: "claude"}})
	registry := assistant.NewJobs(assistant.NewStore(stateDir))
	for _, job := range jobs {
		job.CoderID, job.DoneWhen = "claude", "x"
		registry.Of(job.Owner).Save(job)
	}
	return &Server{assistants: conversations, watcher: assistant.NewWatcher(conversations, registry, nil, nil)}
}

// Every open job names the assistant holding it under the title its row in
// the assistants list shows, and links its thread; a closed job marks nothing
// and only hands the steer dialog its criterion.
func TestTheMarksNameTheAssistantOfEveryOpenJob(t *testing.T) {
	s := steeringServer(t,
		assistant.Job{Owner: steererOne, Terminal: "t1", State: assistant.JobSteering},
		assistant.Job{Owner: steererTwo, Terminal: "t2", State: assistant.JobSteering},
		assistant.Job{Owner: steererOne, Terminal: "t3", State: assistant.JobDone},
	)
	steered, prefill, by := s.steerMarks()
	if !steered["t1"] || !steered["t2"] || steered["t3"] || len(steered) != 2 {
		t.Fatalf("the marks read %v", steered)
	}
	if prefill["t3"] != "x" || prefill["t1"] != "" {
		t.Fatalf("the prefill reads %v", prefill)
	}
	if got := by["t1"]; got.Title != steererTitle || got.URL != "/assistants/"+steererOne {
		t.Fatalf("t1 is steered by %+v", got)
	}
	if got := by["t2"]; got.Title != assistant.DefaultTitle || got.URL != "/assistants/"+steererTwo {
		t.Fatalf("t2 is steered by %+v", got)
	}
	if _, ok := by["t3"]; ok || len(by) != 2 {
		t.Fatalf("a closed job names an assistant: %v", by)
	}
}

// The name is the owner's own thread title: a guest's page answers nobody
// for a steered terminal, even where a render filled the map.
func TestAGuestPageNamesNoSteeringAssistant(t *testing.T) {
	steerers := map[string]render.Steerer{"t1": {Title: steererTitle, URL: "/assistants/" + steererOne}}
	if by := (render.Page{Steerers: steerers}).SteeredBy("t1"); by == nil || by.Title != steererTitle {
		t.Fatalf("the owner reads %+v", by)
	}
	if by := (render.Page{Steerers: steerers}).SteeredBy("t2"); by != nil {
		t.Fatalf("an unsteered terminal names %+v", by)
	}
	if by := (render.Page{Steerers: steerers, Access: render.Access{Guest: true}}).SteeredBy("t1"); by != nil {
		t.Fatalf("a guest reads %+v", by)
	}
}

// The icon's tooltip names the coder and the assistant steering it, and the
// icon carries the name and the thread the row menus offer. An unsteered
// coder's icon says nothing of either.
func TestTheSteeredIconNamesItsAssistant(t *testing.T) {
	tpl := testTemplates(t)
	by := &render.Steerer{Title: steererTitle, URL: "/assistants/" + steererOne}
	icon := renderTemplate(t, tpl, "steered_icon.gohtml", map[string]any{"ID": "t1", "Coder": "claude", "Steered": true, "By": by})
	escaped := "Nightly &lt;b&gt;checks&lt;/b&gt; &amp; &#34;co&#34;"
	for _, want := range []string{
		`title="Claude, steered by ` + escaped + `"`,
		`aria-label="Claude, steered by ` + escaped + `"`,
		`data-steered-by="` + escaped + `"`,
		`data-steered-url="/assistants/` + steererOne + `"`,
	} {
		if !strings.Contains(icon, want) {
			t.Fatalf("want %s in %s", want, icon)
		}
	}
	plain := renderTemplate(t, tpl, "steered_icon.gohtml", map[string]any{"ID": "t1", "Coder": "claude", "Steered": true, "By": (*render.Steerer)(nil)})
	if !strings.Contains(plain, `title="Claude"`) || strings.Contains(plain, "steered by") || strings.Contains(plain, "data-steered") {
		t.Fatalf("an icon nobody names reads %s", plain)
	}
}

// The strip names the assistant on a steered coder's icon for the owner, and
// a guest's strip wears the same purple mark without a name or a thread.
func TestTheStripNamesTheSteeringAssistantToTheOwnerAlone(t *testing.T) {
	tpl := testTemplates(t)
	steered := render.TerminalTab{ID: "t1", Name: "steered", URL: "/coders/t1", Project: "p", Coder: "claude", Kind: "coder"}
	plain := render.TerminalTab{ID: "t2", Name: "plain", URL: "/coders/t2", Project: "p", Coder: "claude", Kind: "coder"}
	page := render.Page{
		Steered:  map[string]bool{"t1": true},
		Steerers: map[string]render.Steerer{"t1": {Title: steererTitle, URL: "/assistants/" + steererOne}},
		QuickNav: render.QuickNav{Active: []render.TerminalTab{steered, plain}, Strip: []render.StripTab{{TerminalTab: steered}, {TerminalTab: plain}}},
	}
	strip := func(p render.Page) string {
		var out bytes.Buffer
		if err := tpl.ExecuteTemplate(&out, "terminal_tabs_strip.gohtml", p); err != nil {
			t.Fatalf("the strip does not render: %v", err)
		}
		return out.String()
	}
	owner := strip(page)
	if !strings.Contains(owner, `title="Claude, steered by Nightly &lt;b&gt;checks&lt;/b&gt; &amp; &#34;co&#34;" data-steered-by=`) ||
		strings.Count(owner, `data-steered-url="/assistants/`+steererOne+`"`) != 1 ||
		strings.Count(owner, `data-steered-label`) != 1 || !strings.Contains(owner, `<span class="text-truncate">Nightly &lt;b&gt;checks&lt;/b&gt; &amp; &#34;co&#34;</span>`) {
		t.Fatalf("the owner's strip reads %s", owner)
	}
	page.Access = render.Access{Guest: true, Base: "/share/token"}
	guest := strip(page)
	if !strings.Contains(guest, " steered\"") || strings.Contains(guest, "Nightly") || strings.Contains(guest, "steered by") ||
		strings.Contains(guest, "data-steered") || strings.Contains(guest, "/assistants/") {
		t.Fatalf("the guest's strip reads %s", guest)
	}
}

// The label beside a coder's name shows the sparkles and the assistant's name,
// says "Steered by" only in its tooltip, links the thread and renders nothing
// at all while nobody steers, so its slot stays empty. A strip row's label is
// no link, the row is one.
func TestTheSteeredLabelNamesTheAssistantAlone(t *testing.T) {
	tpl := testTemplates(t)
	escaped := "Nightly &lt;b&gt;checks&lt;/b&gt; &amp; &#34;co&#34;"
	by := &render.Steerer{Title: steererTitle, URL: "/assistants/" + steererOne}
	for name, link := range map[string]bool{"steered_by.gohtml": true, "steered_by_row.gohtml": false} {
		if label := renderTemplate(t, tpl, name, (*render.Steerer)(nil)); label != "" {
			t.Fatalf("%s renders %q for nobody", name, label)
		}
		label := renderTemplate(t, tpl, name, by)
		wants := []string{`title="Steered by ` + escaped + `"`, `ti-sparkles`, `data-steered-label`, `<span class="text-truncate">` + escaped + `</span>`}
		if link {
			wants = append(wants, `href="/assistants/`+steererOne+`"`)
		}
		for _, want := range wants {
			if !strings.Contains(label, want) {
				t.Fatalf("want %s in %s", want, label)
			}
		}
		if strings.Contains(label, "href=") != link || strings.Count(label, "Steered by") != 1 {
			t.Fatalf("%s reads %s", name, label)
		}
		unnamed := renderTemplate(t, tpl, name, &render.Steerer{URL: "/assistants/" + steererTwo})
		if !strings.Contains(unnamed, ">New assistant</span>") {
			t.Fatalf("an assistant without a title reads %s", unnamed)
		}
	}
}

// The label's own fragment answers the owner, empty for a coder nobody
// steers, and no link mirrors it.
func TestTheLabelFragmentIsTheOwnersAlone(t *testing.T) {
	s := steeringServer(t, assistant.Job{Owner: steererOne, Terminal: "t1", State: assistant.JobSteering})
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.SetHTMLTemplate(testTemplates(t))
	r.GET("/coders/:id/steered-by", s.handleCoderSteeredBy)
	for id, want := range map[string]string{"t1": "Nightly &lt;b&gt;checks&lt;/b&gt;", "t2": ""} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/coders/"+id+"/steered-by", nil))
		if rec.Code != http.StatusOK || (want == "" && rec.Body.Len() != 0) || !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("%s answered %d %q", id, rec.Code, rec.Body.String())
		}
	}
	for _, route := range guestRouteTable(t) {
		if strings.HasSuffix(route.Path, "/steered-by") {
			t.Fatalf("a link reaches %s %s", route.Method, route.Path)
		}
	}
}
