package render

import (
	"strings"
	"testing"
)

// renderAssistantCtx executes the real assistant list column.
func renderAssistantCtx(t *testing.T, data AssistantCtxData) string {
	t.Helper()
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "assistant_ctx.gohtml", data); err != nil {
		t.Fatalf("render assistant column: %v", err)
	}
	return out.String()
}

func assistantCtxData(cards ...AssistantCard) AssistantCtxData {
	return AssistantCtxData{
		Page:       Page{Title: "Assistant"},
		Path:       "/assistants/" + cards[0].ID,
		Assistants: cards,
		ActiveID:   cards[0].ID,
		Available:  true,
		Coders:     []AssistantCoderOption{{ID: "claude", Label: "Claude"}},
		PostURL:    "/assistants/new",
		IDPrefix:   "test",
	}
}

// Working is not a fault: while the turn runs the row shows the ring around
// the assistant's own round icon and says nothing about an unfinished turn.
func TestAnsweringAssistantRunsTheRingWithoutTheBadge(t *testing.T) {
	out := renderAssistantCtx(t, assistantCtxData(
		AssistantCard{ID: "c1", Title: "Live one", URL: "/assistants/c1", Running: true},
		AssistantCard{ID: "c2", Title: "Other one", URL: "/assistants/c2"},
	))
	if !strings.Contains(out, "dc-term-icon assistant working") {
		t.Fatal("the answering row does not run the ring")
	}
	if strings.Contains(out, "Unfinished") {
		t.Fatal("a running turn is shown as unfinished")
	}
}

// A turn that stopped before it was done is the only thing the badge stands
// for, and such a row never runs the ring next to it.
func TestUnfinishedAssistantWearsTheBadgeWithoutTheRing(t *testing.T) {
	out := renderAssistantCtx(t, assistantCtxData(
		AssistantCard{ID: "c1", Title: "Live one", URL: "/assistants/c1"},
		AssistantCard{ID: "c2", Title: "Other one", URL: "/assistants/c2", Unfinished: true},
	))
	if !strings.Contains(out, "Unfinished") {
		t.Fatal("the stopped row does not wear the badge")
	}
	if strings.Contains(out, "assistant working") {
		t.Fatal("a row that is not running still runs the ring")
	}
}

// Every assistant is a row of its own, all of them live: what the column says
// about one of them is what it holds and what it is doing, never that it is
// the current one or an earlier one.
func TestTheColumnListsEveryAssistantWithWhatItHolds(t *testing.T) {
	out := renderAssistantCtx(t, assistantCtxData(
		AssistantCard{ID: "c1", Title: "Release work", URL: "/assistants/c1", OpenJobs: 2},
		AssistantCard{ID: "c2", Title: "Side quest", URL: "/assistants/c2", News: true},
	))
	for _, want := range []string{"Release work", "Side quest", `data-assistant-instance="c1"`, `data-assistant-instance="c2"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %q in the column", want)
		}
	}
	if !strings.Contains(out, "ti-steering-wheel") {
		t.Fatal("want the open jobs on the row that holds them")
	}
	if !strings.Contains(out, "dc-term-icon assistant news") {
		t.Fatal("want the news mark on the row that has it")
	}
	for _, gone := range []string{"Earlier", "Current"} {
		if strings.Contains(out, gone) {
			t.Fatalf("the column still splits the assistants into %q", gone)
		}
	}
}

// renderAssistantMessage executes the real message fragment.
func renderAssistantMessage(t *testing.T, view AssistantMessageView) string {
	t.Helper()
	tmpl := HTMLTemplate(func(p string) string { return p }, "test", "test", nil)
	var out strings.Builder
	if err := tmpl.ExecuteTemplate(&out, "assistant_message.gohtml", AssistantMessageData{Message: view}); err != nil {
		t.Fatalf("render message: %v", err)
	}
	return out.String()
}

// A picture carries its size twice: as the attributes that hold its box open
// while it loads, and as the ratio the stylesheet turns the height cap into a
// width with. The custom property has to survive the CSS context of the
// template escaper, which is what this pins.
func TestAssistantImageCarriesItsSizeAndItsRatio(t *testing.T) {
	out := renderAssistantMessage(t, AssistantMessageView{
		ID: "m1", User: true, Author: "You",
		Attachments: []AssistantAttachmentView{{Name: "shot.png", URL: "/media/shot.png", Media: "image", Width: 1206, Height: 2622}},
	})
	for _, want := range []string{`width="1206"`, `height="2622"`, `style="--dc-media-ratio:1206/2622"`} {
		if !strings.Contains(out, want) {
			t.Fatalf("want %s in %q", want, out)
		}
	}
}

// A picture nobody could measure says nothing: no attributes, no ratio, no
// invented box. Width and height stay auto, and a browser keeps the ratio of
// such an image by itself.
func TestAssistantImageWithoutASizeSaysNothing(t *testing.T) {
	out := renderAssistantMessage(t, AssistantMessageView{
		ID: "m1", User: true, Author: "You",
		Attachments: []AssistantAttachmentView{{Name: "shot.webp", URL: "/media/shot.webp", Media: "image"}},
	})
	for _, unwanted := range []string{"width=", "height=", "--dc-media-ratio"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("unexpected %s in %q", unwanted, out)
		}
	}
}
