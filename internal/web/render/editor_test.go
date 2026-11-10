package render

import (
	"regexp"
	"strings"
	"testing"
)

// The picker's terminals are the running ones with their marks; a stopped
// coder stands apart with its resume, for the owner alone.
func TestEditorTerminalSessionsAreTheRunningOnes(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	sessions := []EditorTerminal{{ID: "sh-1", Name: "alone", Kind: "shell", URL: "/shells/sh-1"}}
	inactive := []EditorInactiveCoder{{ID: "co-9", Name: "sleeper", Coder: "claude", URL: "/coders/co-9/resume"}}
	for _, tc := range []struct {
		name   string
		access Access
		want   bool
	}{
		{"owner", Access{}, true},
		{"guest", Access{Guest: true, Base: "/share/t"}, false},
	} {
		var out strings.Builder
		if err := tmpl.ExecuteTemplate(&out, "editor_terminals.gohtml", EditorTerminalsData{Sessions: sessions, Inactive: inactive, Access: tc.access, CSRFToken: "tok"}); err != nil {
			t.Fatalf("render editor terminals: %v", err)
		}
		html := out.String()
		running := regexp.MustCompile(`(?s)data-term-session="sh-1"[^>]*>\s*<span class="dc-term-icon running`).MatchString(html)
		stopped := strings.Contains(html, `data-term-session="co-9"`)
		resume := strings.Contains(html, `data-resume-id="co-9"`)
		if !running || stopped || resume != tc.want {
			t.Errorf("%s: running session %v, stopped session %v, resume %v, want the resume %v", tc.name, running, stopped, resume, tc.want)
		}
	}
}

// Every view of a terminal tab is a stage carrying the copy view of its
// session, the same surface the attach pages render, with the copy button in
// its footer: a shell on its text, a coder on its conversation as well.
func TestEditorTerminalViewsCarryTheCopyView(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	data := EditorTerminalsData{Sessions: []EditorTerminal{
		{ID: "sh-1", Name: "shell", Kind: "shell", URL: "/shells/sh-1", TextURL: "/shells/sh-1/copy", ScrollHistory: true},
		{ID: "co-1", Name: "coder", Kind: "coder", Coder: "claude", URL: "/coders/co-1", TextURL: "/coders/co-1/copy", ConversationURL: "/coders/co-1/conversation"},
	}}
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "editor_terminals.gohtml", data); err != nil {
		t.Fatalf("render editor terminals: %v", err)
	}
	html := out.String()
	for _, want := range []string{
		`<terminal-copy class="dc-copy" terminal-id="sh-1" text-url="/shells/sh-1/copy" hidden>`,
		`<terminal-copy class="dc-copy" terminal-id="co-1" text-url="/coders/co-1/copy" conversation-url="/coders/co-1/conversation" hidden>`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the views miss %q", want)
		}
	}
	for _, id := range []string{"sh-1", "co-1"} {
		stage := regexp.MustCompile(`class="attach-stage min-w-0 w-100" data-term-stage="` + id + `"`)
		if !stage.MatchString(html) {
			t.Errorf("the view of %s is no stage", id)
		}
		foot := regexp.MustCompile(`(?s)data-terminal-footer="` + id + `">.*?data-terminal-copy`)
		if !foot.MatchString(html) {
			t.Errorf("the footer of %s carries no copy button", id)
		}
	}
	if strings.Contains(html, "data-term-pane") || strings.Contains(html, "data-term-foot=") {
		t.Error("the fragment still renders the terminal panel's panes or head blocks")
	}
}

// A terminal opened as an editor tab shows the fragment's view of it: a stage
// the island mounts into with the session's addresses, and the attach pages'
// footer for that session alone, with the settings and the editor's shortcuts.
// A session watched without input gets the watch footer and no controls.
func TestEditorTerminalViewsCarryTheAttachFooter(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	data := EditorTerminalsData{Sessions: []EditorTerminal{
		{ID: "sh-1", Name: "shell", Kind: "shell", URL: "/shells/sh-1", StreamURL: "/shells/sh-1/stream", InputURL: "/shells/sh-1/input", ScrollHistory: true},
		{ID: "co-1", Name: "coder", Kind: "coder", Coder: "claude", URL: "/coders/co-1", StreamURL: "/coders/co-1/stream", InputURL: "/coders/co-1/input"},
		{ID: "sh-2", Name: "watched", Kind: "shell", URL: "/share/t/shells/sh-2", StreamURL: "/share/t/shells/sh-2/stream"},
	}}
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "editor_terminals.gohtml", data); err != nil {
		t.Fatalf("render editor terminals: %v", err)
	}
	html := out.String()
	view := func(id string) string {
		start := strings.Index(html, `data-terminal-view="`+id+`"`)
		if start < 0 {
			t.Fatalf("no view of %s", id)
		}
		end := strings.Index(html[start+1:], "data-terminal-view=")
		if end < 0 {
			return html[start:]
		}
		return html[start : start+1+end]
	}
	shell := view("sh-1")
	for _, want := range []string{`data-term-stage="sh-1"`, `data-input-url="/shells/sh-1/input"`, `data-scroll-history`, `data-terminal-footer="sh-1"`, `data-terminal-control="enter"`, `data-terminal-settings`, `data-bs-target="#editor-shortcuts-modal"`} {
		if !strings.Contains(shell, want) {
			t.Errorf("the shell's view misses %q", want)
		}
	}
	if strings.Contains(shell, "coder-file-upload") {
		t.Error("the shell's view offers a coder's upload")
	}
	coder := view("co-1")
	for _, want := range []string{`data-bs-target="#coder-files-modal-co-1"`, "<coder-file-upload", `data-terminal-footer="co-1"`} {
		if !strings.Contains(coder, want) {
			t.Errorf("the coder's view misses %q", want)
		}
	}
	watched := view("sh-2")
	if strings.Contains(watched, "data-terminal-control") || strings.Contains(watched, "data-terminal-paste") {
		t.Error("a view without input carries input controls")
	}
	if !strings.Contains(watched, `data-input-url=""`) || !strings.Contains(watched, "data-terminal-copy") {
		t.Error("a view without input is no watching view")
	}
}

// A stopped coder's tab offers its resume to the owner alone.
func TestEditorTerminalStoppedViewsAreTheOwners(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	inactive := []EditorInactiveCoder{{ID: "co-9", Name: "sleeper", Coder: "claude", URL: "/coders/co-9/resume"}}
	for _, tc := range []struct {
		name   string
		access Access
		want   bool
	}{
		{"owner", Access{}, true},
		{"guest", Access{Guest: true, Base: "/share/t"}, false},
	} {
		var out strings.Builder
		if err := tmpl.ExecuteTemplate(&out, "editor_terminals.gohtml", EditorTerminalsData{Inactive: inactive, Access: tc.access, CSRFToken: "tok"}); err != nil {
			t.Fatalf("render editor terminals: %v", err)
		}
		got := regexp.MustCompile(`data-term-stopped="co-9"[^>]*data-term-name="sleeper"(?s:.*)action="/coders/co-9/resume" data-term-resume`).MatchString(out.String())
		if got != tc.want {
			t.Errorf("%s: a stopped view with its resume is %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The create form the picker's New coder opens carries the
// panel marker through its POST, so the coder comes back as an editor tab.
func TestCoderFormCarriesThePanelMarker(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "coders_new_form.gohtml", CoderNewData{Return: "/projects/p/editor", Panel: true}); err != nil {
		t.Fatalf("render coder form: %v", err)
	}
	if !strings.Contains(out.String(), `return=%2Fprojects%2Fp%2Feditor&amp;panel=1"`) {
		t.Errorf("the form's action carries no panel marker: %s", regexp.MustCompile(`action="[^"]*"`).FindString(out.String()))
	}
}
