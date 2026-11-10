package render

import (
	"strings"
	"testing"
)

func renderAssistantWork(t *testing.T, data AssistantData) string {
	t.Helper()
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "assistant_work.gohtml", data); err != nil {
		t.Fatalf("render assistant work: %v", err)
	}
	return out.String()
}

// As an editor tab the surface pulls its own fragment, its ids carry the
// assistant so a second tab keeps its own, and its aside waits hidden for the
// editor's sheet instead of standing beside the thread; on its page it keeps
// the page's ids and the aside the eye opens below xl.
func TestAnAssistantTabCarriesItsOwnIdsAndAHiddenAside(t *testing.T) {
	tab := renderAssistantWork(t, AssistantData{ID: "a1", PostURL: "/assistants/a1", TabURL: "/assistants/a1/tab"})
	for _, want := range []string{
		` tab page-url="/assistants/a1/tab"`, `id="assistant-a1-aside"`, `data-assistant-aside-home`,
		`data-bs-target="#assistant-a1-projects"`, `id="assistant-a1-triggers"`, `scope="assistant-a1-aside"`,
		`id="assistant-a1-new-model"`, `<h2 class="dc-work-title`,
	} {
		if !strings.Contains(tab, want) {
			t.Errorf("the tab misses %s", want)
		}
	}
	for _, never := range []string{`offcanvas-xl`, `data-bs-toggle="offcanvas"`, `id="assistant-aside"`, `id="assistant-projects"`, `data-ctx-resize`, `<h1`} {
		if strings.Contains(tab, never) {
			t.Errorf("the tab carries %s", never)
		}
	}
	page := renderAssistantWork(t, AssistantData{ID: "a1", PostURL: "/assistants/a1"})
	for _, want := range []string{`id="assistant-aside"`, `offcanvas-xl`, `data-bs-target="#assistant-aside"`, `id="assistant-new-model"`, `<h1 class="dc-work-title`} {
		if !strings.Contains(page, want) {
			t.Errorf("the page misses %s", want)
		}
	}
	if strings.Contains(page, "page-url=") || strings.Contains(page, "data-assistant-aside-home") {
		t.Error("the page renders as a tab")
	}
}

// The editor's list carries an entry per assistant with the list column's icon,
// red for an unread failure alone, and a New assistant per coder that names
// the coder only when there is more than one.
func TestTheEditorListsAssistantsWithTheirMarks(t *testing.T) {
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil)
	render := func(data EditorAssistantsData) string {
		var out strings.Builder
		if err := tmpl.ExecuteTemplate(&out, "editor_assistants.gohtml", data); err != nil {
			t.Fatalf("render editor assistants: %v", err)
		}
		return out.String()
	}
	html := render(EditorAssistantsData{
		Cards: []AssistantCard{
			{ID: "a1", Title: "Shop helper", CoderLabel: "Claude", URL: "/assistants/a1", News: true, Running: 5},
			{ID: "a2", CoderLabel: "Claude", URL: "/assistants/a2", News: true},
		},
		Failed: map[string]bool{"a2": true},
		Coders: []AssistantCoderOption{{ID: "claude", Label: "Claude"}, {ID: "copilot", Label: "Copilot"}},
	})
	first, second, _ := strings.Cut(html, `data-assistant-entry="a2"`)
	if !strings.Contains(first, `data-assistant-name="Shop helper"`) || !strings.Contains(first, "working news") || strings.Contains(first, "text-danger") {
		t.Errorf("the running assistant's entry reads %s", first)
	}
	if !strings.Contains(second, `data-assistant-name="New assistant"`) || !strings.Contains(second, "news text-danger") {
		t.Errorf("the failed assistant's entry reads %s", second)
	}
	if !strings.Contains(html, `data-assistant-new-label="New assistant with Copilot"`) {
		t.Error("several coders do not name theirs on New assistant")
	}
	one := render(EditorAssistantsData{Coders: []AssistantCoderOption{{ID: "claude", Label: "Claude"}}})
	if !strings.Contains(one, `data-assistant-new-label="New assistant"`) || strings.Contains(one, "with Claude") {
		t.Error("one coder still names itself on New assistant")
	}
}
