package render

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/docker"
)

func statusLinksNavs() []ProjectNav {
	link := func(host string, port int) docker.Link { return docker.Link{Host: host, Port: port} }
	return []ProjectNav{
		{Name: "alpha", LastUsedUnix: 10, Docker: ProjectDocker{Stacks: []DockerStack{
			{Stack: docker.Stack{Running: 1}, Links: []docker.Link{link("", 8080)}},
		}}},
		{Name: "beta", LastUsedUnix: 30, Active: true, Docker: ProjectDocker{Stacks: []DockerStack{
			{Stack: docker.Stack{Running: 2}, Links: []docker.Link{link("beta.test", 0), link("", 8081)}},
			{Stack: docker.Stack{Label: "tools", Running: 1}, Links: []docker.Link{link("", 8025)}},
		}}},
		{Name: "gamma", LastUsedUnix: 20, Docker: ProjectDocker{Stacks: []DockerStack{
			{Stack: docker.Stack{Running: 0}, Links: []docker.Link{link("", 9000)}},
		}}},
		{Name: "delta", LastUsedUnix: 40},
	}
}

// Only a running stack with an address counts, a stopped one and a project
// without stacks are left out of both totals.
func TestStatusLinksCountRunningStacksOnly(t *testing.T) {
	links := NewStatusLinks(statusLinksNavs(), "")
	if links.Links != 4 || len(links.Projects) != 2 {
		t.Fatalf("totals %d links in %d projects", links.Links, len(links.Projects))
	}
	if got := links.Summary(); got != "4 links · 2 projects" {
		t.Fatalf("summary %q", got)
	}
	if !links.Any() {
		t.Fatal("not shown with links")
	}
	if NewStatusLinks(statusLinksNavs()[2:], "").Any() {
		t.Fatal("shown without a running stack")
	}
}

// The groups keep the browser's order, the page's project is the open one
// and the rest stand folded with their count and carry the sort keys.
func TestStatusLinksOpenThePagesProject(t *testing.T) {
	links := NewStatusLinks(statusLinksNavs(), "beta")
	if names(links) != "alpha,beta" || links.Projects[0].Open || !links.Projects[1].Open {
		t.Fatalf("order %s, open %v %v", names(links), links.Projects[0].Open, links.Projects[1].Open)
	}
	if links.Projects[1].Links != 3 || len(links.Projects[1].Stacks) != 2 || links.Projects[1].Stacks[1].Label != "tools" {
		t.Fatalf("beta %+v", links.Projects[1])
	}
	if links.Projects[1].LastUsedUnix != 30 || links.Projects[0].LastUsedUnix != 10 {
		t.Fatalf("sort keys %+v", links.Projects)
	}
}

// Without a page project, or with one that answers nothing, the project used
// last is the open one, and the order does not move for it.
func TestStatusLinksOpenTheLastUsedProject(t *testing.T) {
	for _, current := range []string{"", "delta"} {
		links := NewStatusLinks(statusLinksNavs(), current)
		if names(links) != "alpha,beta" || links.Projects[0].Open || !links.Projects[1].Open {
			t.Fatalf("current %q: order %s, open %v %v", current, names(links), links.Projects[0].Open, links.Projects[1].Open)
		}
	}
}

// One link in one project reads in the singular.
func TestStatusLinksSummaryIsSingularForOne(t *testing.T) {
	if got := NewStatusLinks(statusLinksNavs()[:1], "").Summary(); got != "1 link · 1 project" {
		t.Fatalf("summary %q", got)
	}
}

// The template writes a routed host protocol relative and a published port
// as //:port for the browser to complete, opens each in a new tab, and the
// whole element hides without links.
func TestStatusLinksTemplateWritesTheAddresses(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "status_links.gohtml", Page{Links: NewStatusLinks(statusLinksNavs(), "beta")}); err != nil {
		t.Fatalf("render: %v", err)
	}
	got := out.String()
	for _, want := range []string{
		`href="//beta.test"`, `href="//:8081"`, `data-links-route="beta.test"`, `target="_blank"`, `>beta.test<`, `>:8025<`,
		`4 links · 2 projects`, `data-links-project="beta"`, `aria-expanded="true"`, `aria-expanded="false"`,
		`data-links-used="30"`, `data-links-active="true"`, `class="dc-status-sep"`,
		`<div class="text-secondary small">tools</div>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	if strings.Contains(got, "<dc-status-links class=\"dropdown dropup dc-fine-only\" hidden") {
		t.Fatal("hidden with links")
	}
	if strings.Contains(got, `data-links-route=":`) {
		t.Fatal("a published port is marked as a route")
	}
	out.Reset()
	if err := tmpl.ExecuteTemplate(&out, "status_links.gohtml", Page{}); err != nil {
		t.Fatalf("render empty: %v", err)
	}
	if !strings.Contains(out.String(), ` hidden>`) {
		t.Fatalf("shown without links:\n%s", out.String())
	}
}

func names(links StatusLinks) string {
	out := make([]string, 0, len(links.Projects))
	for _, p := range links.Projects {
		out = append(out, p.Name)
	}
	return strings.Join(out, ",")
}
