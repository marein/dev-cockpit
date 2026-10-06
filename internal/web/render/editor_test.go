package render

import (
	"regexp"
	"strings"
	"testing"
)

// Every pane of the editor's terminal panel is a stage carrying the copy view
// of its session, the same surface the attach pages render, and every session
// gets the copy button in its head block: a shell on its text, a coder on its
// conversation as well.
func TestEditorTerminalPanesCarryTheCopyView(t *testing.T) {
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
			t.Errorf("the panel misses %q", want)
		}
	}
	for _, id := range []string{"sh-1", "co-1"} {
		pane := regexp.MustCompile(`class="editor-term-pane attach-stage" data-term-pane="` + id + `"`)
		if !pane.MatchString(html) {
			t.Errorf("pane %s is no stage", id)
		}
		foot := regexp.MustCompile(`(?s)data-term-foot="` + id + `" data-terminal-footer="` + id + `" hidden>\s*<button[^>]*data-terminal-copy`)
		if !foot.MatchString(html) {
			t.Errorf("the head block of %s carries no copy button", id)
		}
	}
}
