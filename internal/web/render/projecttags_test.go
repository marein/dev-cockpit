package render

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/project"
)

var (
	tagAPI     = project.Tag{ID: "a1", Name: "api", Color: "blue"}
	tagClientA = project.Tag{ID: "c1", Name: "Client A", Color: "yellow"}
)

func renderNamed(t *testing.T, name string, data any) string {
	t.Helper()
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, name, data); err != nil {
		t.Fatalf("render %s: %v", name, err)
	}
	return out.String()
}

// A main project's row names its tags as chips that filter by them, each
// pointing at its tag by id, in a line that ends with the way into its tags
// dialog. A worktree's row shows no tags and offers no editing, its text
// filter matches its main's tag names. The bar offers every tag once with the
// projects carrying it, a count as wide as its total, and leads to the
// settings. The list column shows the disc whose slices its filter matches.
func TestProjectRowsCarryTheTags(t *testing.T) {
	data := ProjectsListData{
		Page: Page{Title: "Projects"},
		Projects: []project.Project{
			{Name: "app", Path: "/tmp/app", Label: "app", GitRepo: true, Tags: []project.Tag{tagAPI, tagClientA}, SearchTags: []string{"api", "Client A"}},
			{Name: "app-wt", Path: "/tmp/app-wt", Label: "app-wt", GitRepo: true, GitWorktree: true, GitWorktreeOf: "app", SearchTags: []string{"api", "Client A"}},
			{Name: "web", Path: "/tmp/web", Label: "web", Tags: []project.Tag{tagAPI}, SearchTags: []string{"api"}},
			{Name: "bare", Path: "/tmp/bare", Label: "bare"},
		},
	}
	out := renderProjects(t, data)
	for _, want := range []string{
		`<div class="project-chips project-tags mt-2" data-tag-line>
  <span class="project-chip-fold" data-chip-fold="tags"><button type="button" class="dc-tag dc-tag-blue" data-tag="a1" data-tag-toggle="a1" aria-pressed="false" title="Show only the projects tagged api"><span class="dc-tag-name">api</span></button><button type="button" class="dc-tag dc-tag-yellow" data-tag="c1" data-tag-toggle="c1" aria-pressed="false" title="Show only the projects tagged Client A"><span class="dc-tag-name">Client A</span></button></span>
  <a href="/projects/app/tags" class="dc-tag dc-tag-add" data-project-tags-edit aria-label="Tags of project app" title="Tags of app"><i class="ti ti-pencil" aria-hidden="true"></i></a>`,
		`<a href="/projects/bare/tags" class="dc-tag dc-tag-add" data-project-tags-edit aria-label="Tags of project bare" title="Tags of bare"><i class="ti ti-plus" aria-hidden="true"></i><span class="dc-tag-name">Tag</span></a>`,
		`data-tag-toggle="a1" data-tag-total="2" aria-pressed="false" title="Show only the projects tagged api"><span class="dc-tag-name">api</span><span class="dc-tag-count" style="--dc-tag-digits: 1" aria-label="2 projects" data-tag-count>2</span>`,
		`aria-label="1 project" data-tag-count>1</span>`,
		`<a href="/settings/projects/tags" class="btn btn-sm btn-icon btn-ghost-secondary" data-tags-manage aria-label="Manage tags" title="Manage tags">`,
		`<span class="dc-filter-tags" data-tag-picked hidden></span>`,
		`<span class="small text-secondary text-nowrap" data-filter-count hidden></span>`,
		`id="project-app-wt" data-project-name="app-wt" data-project-worktree-of="app" data-project-grouped hidden data-project-tags="api Client A"`,
		`data-index-project="app-wt"`,
		`data-index-worktree-of="app" data-index-tags="api Client A"`,
		`<span class="dc-tag-slice dc-tag-yellow" data-tag="c1" style="--dc-tag-i: 1"></span>`,
		`<span class="dc-tagged flex-fill"><span class="dc-mono dc-trunc">app</span><span class="dc-tag-pie" role="img" aria-label="Tags: api, Client A" title="api, Client A"><span class="dc-tag-disc" style="--dc-tag-n: 2">`,
		`<span class="dc-mono dc-trunc flex-fill">app-wt</span>`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the list misses %s", want)
		}
	}
	for _, gone := range []string{"/projects/app-wt/tags", "dc-tag-dot-toggle", "data-tag-clear", `class="ti ti-tags icon"`} {
		if strings.Contains(out, gone) {
			t.Fatalf("the list still carries %s", gone)
		}
	}
	if got := strings.Count(out, `data-tag="a1" data-tag-toggle="a1"`); got != 2 {
		t.Fatalf("want the tag's chip on two rows, got %d", got)
	}
	if got := strings.Count(out, `data-tag="a1"`); got != 4 {
		t.Fatalf("want the tag on two rows and two index rows, got %d", got)
	}
	data.Projects = []project.Project{{Name: "plain", Path: "/tmp/plain", Label: "plain"}}
	if out := renderProjects(t, data); !strings.Contains(out, `data-tag-bar role="group" aria-label="Filter by tag" hidden`) {
		t.Fatal("a list without tags shows the tag bar")
	}
}

// The palette shows a project's tags as a named disc after the branch, in the
// part of the row that takes only the room the name leaves, and a row with
// neither carries no such part. Every tag is a slice of the one disc, in its
// order and with its id for the filter, however many there are.
func TestTheSwitchersShowTheTags(t *testing.T) {
	out := renderNamed(t, "editor_projects.gohtml", map[string]any{"Projects": []EditorProject{
		{Name: "app", URL: "/projects/app/editor", Repo: "app", Branch: "main", Tags: []project.Tag{tagAPI, tagClientA}},
		{Name: "plain", URL: "/projects/plain/editor"},
	}})
	want := `<span class="dc-tagged editor-palette-name"><span class="editor-palette-repo">app</span><span class="editor-palette-rest"><span class="editor-palette-branch"><i class="ti ti-git-branch" aria-hidden="true"></i> main</span><span class="dc-tag-pie" role="img" aria-label="Tags: api, Client A" title="api, Client A"><span class="dc-tag-disc" style="--dc-tag-n: 2"><span class="dc-tag-slice dc-tag-blue" data-tag="a1" style="--dc-tag-i: 0"></span><span class="dc-tag-slice dc-tag-yellow" data-tag="c1" style="--dc-tag-i: 1"></span></span></span></span>`
	if !strings.Contains(out, want) {
		t.Fatalf("the palette row misses its tags: %s", out)
	}
	if strings.Count(out, "editor-palette-rest") != 1 {
		t.Fatalf("a row without branch and tags carries the rest: %s", out)
	}
	var many []project.Tag
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		many = append(many, project.Tag{ID: name + "1", Name: name, Color: "teal"})
	}
	pie := renderNamed(t, "tag_pie.gohtml", many)
	if !strings.Contains(pie, `<span class="dc-tag-disc" style="--dc-tag-n: 6"><span class="dc-tag-slice dc-tag-teal" data-tag="a1" style="--dc-tag-i: 0"></span>`) || !strings.Contains(pie, `<span class="dc-tag-slice dc-tag-teal" data-tag="f1" style="--dc-tag-i: 5"></span></span></span>`) || strings.Count(pie, "dc-tag-slice ") != 6 || !strings.Contains(pie, `aria-label="Tags: a, b, c, d, e, f"`) {
		t.Fatalf("six tags make %s", pie)
	}
	if none := renderNamed(t, "tag_pie.gohtml", []project.Tag(nil)); none != "" {
		t.Fatalf("no tags make %q", none)
	}
}

// The tags form shows the project's tags first, each with its cross, then the
// field and the tags used on other projects, and leads to the settings for
// names and colors. A tag in use posts its id and no color, a new one its
// name and the color it shows.
func TestTheTagsFormStandsAsPageAndDialog(t *testing.T) {
	data := ProjectTagsData{
		Page:       Page{Title: "Tags of app", CSRFToken: "tok"},
		Project:    "app",
		Tags:       []project.Tag{tagAPI, {Name: "fresh", Color: "teal"}},
		InUse:      []project.TagUse{{Tag: tagAPI, Projects: 1}, {Tag: tagClientA, Projects: 2}},
		Colors:     project.TagColors,
		NextColors: []string{"teal", "lime"},
		Return:     "/projects",
	}
	page := renderNamed(t, "project_tags.gohtml", data)
	for _, want := range []string{
		`action="/projects/app/tags"`,
		`data-next-colors="teal lime"`,
		`<div class="form-label" id="project-tags-on">On app</div>`,
		`<span class="dc-tag dc-tag-edit dc-tag-blue" data-tag="a1" data-tag-color="blue" data-tag-on>`,
		`<input type="hidden" name="tag" value="a1" data-tag-field>
  <input type="hidden" data-tag-color-field>`,
		`<input type="hidden" name="new" value="fresh" data-tag-field>
  <input type="hidden" name="new-color:fresh" value="teal" data-tag-color-field>`,
		`<button type="button" class="dc-tag-x" data-tag-remove aria-label="Remove api" title="Remove api">`,
		`<label class="form-label mt-3" for="project-tag-input">Add a tag</label>`,
		`id="project-tags-offered" data-tags-offered-label>Other tags</div>`,
		`<button type="button" class="dc-tag dc-tag-offer dc-tag-yellow" data-tag-offer="Client A" data-tag="c1" data-tag-color="yellow"`,
		`<a href="/settings/projects/tags">Settings › Projects › Tags</a>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page misses %s", want)
		}
	}
	if strings.Index(page, `data-tags-on`) > strings.Index(page, `id="project-tag-input"`) {
		t.Fatal("the project's tags do not stand first")
	}
	for _, gone := range []string{`data-tag-offer="api"`, "data-form-fixed", "data-tag-swatch", "data-tag-pick", `name="color:`} {
		if strings.Contains(page, gone) {
			t.Fatalf("the page carries %s", gone)
		}
	}
	data.Tags = []project.Tag{tagAPI, tagClientA}
	if all := renderNamed(t, "project_tags.gohtml", data); !strings.Contains(all, `data-tags-offered-label hidden>`) {
		t.Fatal("the label stands over no offers")
	}
	data.Modal = true
	dialog := renderNamed(t, "project_tags_form.gohtml", data)
	if !strings.Contains(dialog, `action="/projects/app/tags?modal=1"`) || !strings.Contains(dialog, `data-form-scrollable data-form-fixed data-form-title="Tags of app"`) {
		t.Fatalf("the dialog form is not marked: %s", dialog)
	}
}

// The tags settings list every tag as a row with the fields its save posts,
// the tag as loaded and as it stands, its projects for the merge line and a
// link to the board filtered by it, then the new tags a refused save sent,
// and the way to add one. A name is text wherever it stands. A file that
// could not be read shows no list, an empty one says so and offers to add.
func TestTheTagSettingsStandAsRows(t *testing.T) {
	odd := project.TagState{Tag: project.Tag{ID: "o1", Name: `a"b<c>`, Color: "teal"}, Version: "v3"}
	data := SettingsTagsData{
		Page: Page{Title: "Settings", CSRFToken: "tok"},
		Rows: []TagRow{
			{TagState: project.TagState{Tag: tagAPI, Projects: []string{"app", "web"}, Version: "v1"}, Field: "gateway", Pick: "pink"},
			{TagState: project.TagState{Tag: tagClientA, Projects: []string{"app"}, Version: "v2"}, Field: "Client A", Pick: "yellow", Remove: true},
			{TagState: odd, Field: odd.Name, Pick: odd.Color},
		},
		Colors: project.TagColors,
	}
	page := renderNamed(t, "settings_projects_tags.gohtml", data)
	for _, want := range []string{
		`<a class="nav-link active" href="/settings/projects/tags"><i class="ti ti-tag me-1 dc-hide-xs"></i>Tags</a>`,
		`<form method="post" action="/settings/projects/tags" id="settings-projects-tags" autocomplete="off" data-tags-form>`,
		`data-tag-row="a1" data-loaded-name="api" data-loaded-color="blue" data-carriers="app/web">`,
		`<input type="hidden" name="tag" value="a1">`,
		`<input type="hidden" name="version:a1" value="v1">`,
		`<input type="hidden" name="was:a1" value="api">`,
		`<input type="hidden" name="was-color:a1" value="blue">`,
		`<input type="hidden" name="color:a1" value="pink" data-tag-color-value>`,
		`name="name:a1" value="gateway"`,
		`<span class="dc-tag-dot dc-tag-pink" data-tag-color-dot></span>`,
		`href="/projects?tag=a1" title="Show the projects tagged api">2 projects</a>`,
		`<input type="hidden" name="remove:c1" value="1" data-tag-remove-value>`,
		`href="/projects?tag=c1" title="Show the projects tagged Client A">1 project</a>`,
		`<span class="dc-tag-row-count text-secondary small text-nowrap">0 projects</span>`,
		`data-loaded-name="a&#34;b&lt;c&gt;"`,
		`>3 tags</span>`,
		`<button type="submit" class="btn btn-primary" form="settings-projects-tags" data-tags-save>Save</button>`,
		`<button type="button" class="btn btn-primary" data-tags-merge-apply>Merge</button>`,
		`<button type="button" class="dc-tag-swatch dc-tag-cyan" data-tag-swatch="cyan" aria-pressed="false" aria-label="cyan" title="cyan"></button>`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the page misses %s", want)
		}
	}
	if got := strings.Count(page, "data-tags-merge-color="); got != len(project.TagColors) {
		t.Fatalf("want a merge swatch per color, got %d", got)
	}
	empty := renderNamed(t, "settings_projects_tags.gohtml", SettingsTagsData{Page: data.Page, Colors: project.TagColors, NextColors: []string{"teal", "lime"}})
	for _, want := range []string{"No tags yet.", "data-tags-form", "data-tags-foot", `<button type="button" class="btn btn-sm btn-ghost-secondary mt-2" data-tags-add>`, `data-next-colors="teal lime"`, `<template data-tag-new-template>`} {
		if !strings.Contains(empty, want) {
			t.Fatalf("no tags render without %s: %s", want, empty)
		}
	}
	data.New = []project.Tag{{Name: `new<b>`, Color: "teal"}, {Name: "plain"}}
	fresh := renderNamed(t, "settings_projects_tags.gohtml", data)
	for _, want := range []string{
		`<input type="hidden" name="new-color" value="teal" data-tag-color-value>`,
		`<span class="dc-tag-dot dc-tag-teal" data-tag-color-dot></span>`,
		`name="new-name" value="new&lt;b&gt;"`,
		`<input type="hidden" name="new-color" value="" data-tag-color-value>`,
		`<span class="dc-tag-dot" data-tag-color-dot></span>`,
		`name="new-name" value="plain"`,
	} {
		if !strings.Contains(fresh, want) {
			t.Fatalf("the new rows miss %s", want)
		}
	}
	if strings.Contains(fresh, "No tags yet.") {
		t.Fatal("a page with tags says it has none")
	}
	unread := renderNamed(t, "settings_projects_tags.gohtml", SettingsTagsData{Page: data.Page, Colors: project.TagColors, Unread: true})
	if strings.Contains(unread, "No tags yet.") || strings.Contains(unread, "data-tags-form") {
		t.Fatalf("an unread file renders %s", unread)
	}
}
