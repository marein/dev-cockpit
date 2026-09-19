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

// The header over a pushed answer holds one line, so a trigger the user named
// stands there under that name, and without a name the event that fired it
// stands there instead. Under that line stands the answer and nothing else:
// that it was answered unasked is what the icon and the name say already, the
// occasion is the headline itself, and the task is what the user wrote on the
// trigger, which is where it is read.
func TestAPushedAnswerReadsByItsHeadlineAndHoldsNothingButTheAnswer(t *testing.T) {
	const answer = "<p>It is done.</p>"
	named := renderAssistantMessage(t, AssistantMessageView{
		ID: "m1", Auto: true, Author: "Cockpit", Time: "2026-09-20T10:00:00Z", HTML: answer,
		Origin: &AssistantNoteView{Source: "event", Headline: "nightly readme"},
	})
	if !strings.Contains(named, `data-assistant-note-headline>nightly readme<`) {
		t.Fatalf("want the name as the header's one line, got %q", named)
	}

	bare := renderAssistantMessage(t, AssistantMessageView{
		ID: "m2", Auto: true, Author: "Cockpit", Time: "2026-09-20T10:00:00Z", HTML: answer,
		Origin: &AssistantNoteView{Source: "event", Headline: "Job done: readme-task in dev-cockpit"},
	})
	if !strings.Contains(bare, `data-assistant-note-headline>Job done: readme-task in dev-cockpit<`) {
		t.Fatalf("want the event as the header of a trigger nobody named, got %q", bare)
	}

	for name, out := range map[string]string{"the named one": named, "the bare one": bare} {
		for _, unwanted := range []string{"answered without you", "Event:", "Task:", "events in one window", "data-assistant-origin-task"} {
			if strings.Contains(out, unwanted) {
				t.Fatalf("%s carries %q beside the answer: %q", name, unwanted, out)
			}
		}
		if !strings.Contains(out, answer) {
			t.Fatalf("%s lost the answer itself: %q", name, out)
		}
	}
}

// A check's report and a pushed answer are the two messages nobody asked for,
// and they read the same way: a header that carries no switch, one control
// under it that is the chevron and the preview together, and the whole text
// folded below it, closed until somebody opens it. Two shapes for the same
// kind of message is what this pins away: the switch stood on the right of
// one header and under the other, and one of the two arrived open.
func TestANoteAndAPushedAnswerFoldTheSameWay(t *testing.T) {
	report := renderAssistantMessage(t, AssistantMessageView{
		ID: "m1", Author: "Cockpit", Time: "2026-09-20T10:00:00Z", HTML: "<p>It is done.</p>",
		Note: &AssistantNoteView{
			Source: "check", Headline: "DONE: readme-task", Verdict: "done", Done: true,
			Preview: "It is done, the file is there.", Rest: true,
		},
	})
	pushed := renderAssistantMessage(t, AssistantMessageView{
		ID: "m2", Auto: true, Author: "Cockpit", Time: "2026-09-20T10:00:00Z", HTML: "<p>It is done.</p>",
		Origin: &AssistantNoteView{
			Source: "event", Headline: "nightly readme",
			Preview: "It is done, the file is there.", Rest: true,
		},
	})
	for name, out := range map[string]string{"the report": report, "the pushed answer": pushed} {
		if strings.Count(out, "data-assistant-note-fold") != 1 {
			t.Fatalf("%s does not carry exactly one fold control: %q", name, out)
		}
		if !strings.Contains(out, `data-assistant-note-preview>It is done, the file is there.<`) {
			t.Fatalf("%s does not show the preview beside the chevron: %q", name, out)
		}
		if !strings.Contains(out, `aria-expanded="false"`) || !strings.Contains(out, `class="collapse" id="note-`) {
			t.Fatalf("%s does not start folded: %q", name, out)
		}
		// The header is the badge, the headline and the time, and nothing that
		// toggles: the chevron is the preview's, and it is the only one.
		start := strings.Index(out, `class="d-flex align-items-center gap-2 mb-1"`)
		head := out[start : start+strings.Index(out[start:], "</div>")]
		if strings.Contains(head, `data-bs-toggle="collapse"`) {
			t.Fatalf("%s still switches from its header: %q", name, head)
		}
	}
}

// A pushed answer holds nothing but its answer, so a short one folds nothing:
// a fold with the whole text already in its preview is a control that opens
// onto what the reader is looking at. It is the report's own behaviour, which
// is what the two were pulled together on.
func TestAShortPushedAnswerFoldsNothing(t *testing.T) {
	for name, view := range map[string]AssistantMessageView{
		"the pushed answer": {
			ID: "m1", Auto: true, Author: "Cockpit", Time: "2026-09-20T10:00:00Z", HTML: "<p>AIRPORT</p>",
			Origin: &AssistantNoteView{Source: "event", Headline: "nightly readme", Preview: "AIRPORT"},
		},
		"the report": {
			ID: "m2", Author: "Cockpit", Time: "2026-09-20T10:00:00Z", HTML: "<p>AIRPORT</p>",
			Note: &AssistantNoteView{Source: "check", Headline: "DONE: readme-task", Verdict: "done", Done: true, Preview: "AIRPORT"},
		},
	} {
		out := renderAssistantMessage(t, view)
		if strings.Contains(out, "data-assistant-note-fold") {
			t.Fatalf("%s folds a text its preview holds whole: %q", name, out)
		}
		if strings.Contains(out, "data-assistant-note-rest") {
			t.Fatalf("%s hides its answer behind a collapse: %q", name, out)
		}
		if !strings.Contains(out, "<p>AIRPORT</p>") {
			t.Fatalf("%s lost its answer: %q", name, out)
		}
	}
}

// The speaker hangs on the audio URL alone, so it stands on a check's report
// like it stands on an answer: the three kinds of message the user did not
// write render the same button out of one template. It used to hang on the
// origin as well, which left a report with the audio behind it and no way to
// ask for it.
func TestTheSpeakerStandsOnEveryMessageThatCanBeSpoken(t *testing.T) {
	const audio = "/assistants/a1/messages/m1/audio"
	kinds := map[string]AssistantMessageView{
		"the report": {
			ID: "m1", Author: "Cockpit", AudioURL: audio, HTML: "<p>It is done.</p>",
			Note: &AssistantNoteView{Source: "check", Headline: "DONE: readme-task", Verdict: "done", Done: true},
		},
		"the pushed answer": {
			ID: "m1", Auto: true, Author: "Cockpit", AudioURL: audio, HTML: "<p>It is done.</p>",
			Origin: &AssistantNoteView{Source: "event", Headline: "nightly readme"},
		},
		"the answer": {ID: "m1", Author: "Claude", AudioURL: audio, HTML: "<p>It is done.</p>"},
	}
	button := ""
	for name, view := range kinds {
		out := renderAssistantMessage(t, view)
		start := strings.Index(out, "<button type=\"button\" class=\"btn btn-icon")
		if start < 0 {
			t.Fatalf("%s carries no speaker: %q", name, out)
		}
		one := out[start : start+strings.Index(out[start:], "</button>")]
		if !strings.Contains(one, `data-assistant-speak="`+audio+`"`) {
			t.Fatalf("%s does not speak its own audio: %q", name, one)
		}
		if button != "" && one != button {
			t.Fatalf("%s renders a different speaker: %q against %q", name, one, button)
		}
		button = one
	}

	// And a message without audio carries none: the button is the URL's.
	silent := renderAssistantMessage(t, AssistantMessageView{
		ID: "m2", Author: "Cockpit", HTML: "<p>It is done.</p>",
		Note: &AssistantNoteView{Source: "check", Headline: "DONE: readme-task", Verdict: "done", Done: true},
	})
	if strings.Contains(silent, "data-assistant-speak") {
		t.Fatalf("a message with no audio still offers a speaker: %q", silent)
	}
}
