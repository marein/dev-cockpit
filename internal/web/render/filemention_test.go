package render

import (
	"strings"
	"testing"
)

// A project reads the same in the editor's switcher and in a mention: the
// same line and the same attributes, a link there and an option here.
func TestMentionProjectRowsAreTheSwitchersRowsAsOptions(t *testing.T) {
	data := EditorProjectsData{Projects: []EditorProject{
		{Name: "app", URL: "/projects/app/editor", Repo: "app", Branch: "main", Active: true, LastUsedUnix: 42, Search: "app main"},
		{Name: "app-fix", URL: "/projects/app-fix/editor", Repo: "app", Branch: "fix", Worktree: true, WorktreeOf: "app", Search: "app-fix app fix"},
	}}
	switcher := renderNamed(t, "editor_projects.gohtml", data)
	mention := renderNamed(t, "file_mention_projects.gohtml", data)
	if strings.Count(switcher, "<a class=\"editor-palette-row\" href=") != 2 || strings.Contains(switcher, `role="option"`) {
		t.Fatalf("the switcher's rows are no links: %s", switcher)
	}
	if strings.Count(mention, `<div class="editor-palette-row" role="option" aria-selected="false"`) != 2 || strings.Contains(mention, "href=") {
		t.Fatalf("the mention's rows are no options: %s", mention)
	}
	for _, want := range []string{
		` data-project-name="app" data-project-active="true" data-project-used="42" data-project-search="app main" title="app on main"`,
		` data-project-name="app-fix" data-project-active="false" data-project-used="0" data-project-worktree-of="app" data-project-worktree data-project-search="app-fix app fix" title="app-fix, worktree of app on fix"`,
		`<i class="ti ti-git-fork" aria-hidden="true"></i>`,
		`<span class="editor-palette-sub">app-fix</span>`,
	} {
		if !strings.Contains(switcher, want) || !strings.Contains(mention, want) {
			t.Fatalf("both palettes should carry %s\nswitcher: %s\nmention: %s", want, switcher, mention)
		}
	}
}

// The popup ships hidden with the list it fills and the one file row it
// clones, and names the box it serves.
func TestMentionPopupShell(t *testing.T) {
	out := renderNamed(t, "file_mention.gohtml", nil)
	for _, want := range []string{
		`<dc-file-mention input="[data-assistant-input]"`,
		`projects-url="/mention/projects" files-url="/mention/files"`,
		`data-mention-panel hidden>`,
		`role="listbox" id="dc-mention-list"`,
		`<template data-mention-file-template>`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("the popup misses %s: %s", want, out)
		}
	}
}
