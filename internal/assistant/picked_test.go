package assistant

import (
	"encoding/json"
	"strings"
	"testing"
)

func pickedCard(note string) Picked {
	return Picked{
		Note:     note,
		Title:    "Jukebox",
		Address:  "localhost:8095/cart",
		Viewport: "1448×647",
		Chips:    []PickedChip{{Kind: "element", Text: `button "Save"`}, {Kind: "error", Text: "2 errors"}},
		Sections: []PickedSection{{
			Kind:   "element",
			Title:  `1. button "Save"`,
			Rows:   []PickedRow{{Term: "Selector", Value: "#save", Code: true}},
			Blocks: []PickedBlock{{Label: "HTML", Text: `<button id="save">Save</button>`}},
		}},
	}
}

func TestAPickedPromptCarriesItsCardAndKeepsItsText(t *testing.T) {
	runner := &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}}
	svc, _, _ := newTestService(t, runner)
	created, _ := svc.create("claude")
	text := "The title sticks to the line above\n\n## Picked in the Browser\n\nOne element of `http://localhost:8095/cart`."
	if _, err := svc.PromptPicked(created.ID, " "+text+" ", pickedCard(" The title sticks to the line above "), "Laptop"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	waitIdle(t, svc, created.ID)
	if turns := runner.turns(); len(turns) != 1 || turns[0].Prompt != text {
		t.Fatalf("the assistant read %+v", turns)
	}
	thread, _ := svc.Get(created.ID)
	sent := thread.Messages[0]
	if sent.Content != text || sent.Via != "Laptop" || sent.Picked == nil || sent.Picked.Note != "The title sticks to the line above" || sent.Picked.Sections[0].Rows[0].Value != "#save" {
		t.Fatalf("the message went in as %+v with %+v", sent, sent.Picked)
	}
}

func TestAPickedMessageNamesTheAssistantAfterItsNoteElseItsPage(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}})
	long := strings.Repeat("wide ", 40)
	for _, c := range []struct{ note, page, address, want string }{
		{"Make the cart wider\nand the button red", "Jukebox", "shop.test/cart", "Make the cart wider"},
		{long, "Jukebox", "shop.test/cart", truncateRunes(oneLine(long), MaxTitleRunes)},
		{"", "Jukebox", "shop.test/cart", "Jukebox"},
		{"", " ", "shop.test/cart", "shop.test/cart"},
		{"", "", "", DefaultTitle},
	} {
		created, _ := svc.create("claude")
		text := "## Picked with the browser extension\n\nOne element."
		if c.note != "" {
			text = c.note + "\n\n" + text
		}
		card := pickedCard(c.note)
		card.Title, card.Address = c.page, c.address
		if _, err := svc.PromptPicked(created.ID, text, card, ""); err != nil {
			t.Fatalf("prompt: %v", err)
		}
		waitIdle(t, svc, created.ID)
		if got, _ := svc.Get(created.ID); got.Title != c.want {
			t.Fatalf("a note %q on the page %q at %q named it %q", c.note, c.page, c.address, got.Title)
		}
	}
}

func TestAPickedCardOutsideItsShapeIsRefused(t *testing.T) {
	svc, _, _ := newTestService(t, &fakeRunner{events: []Event{{Kind: EventDelta, Text: "ok"}}})
	created, _ := svc.create("claude")
	text := "Look here\n\n## Picked in the Browser"
	cases := map[string]func(*Picked){
		"a note the message does not begin with": func(p *Picked) { p.Note = "Something else" },
		"an unknown chip":                        func(p *Picked) { p.Chips[0].Kind = "script" },
		"an unknown section":                     func(p *Picked) { p.Sections[0].Kind = "frame" },
		"an unknown line":                        func(p *Picked) { p.Sections[0].Lines = []PickedLine{{Kind: "link", Text: "x"}} },
		"no section":                             func(p *Picked) { p.Sections = nil },
		"too many sections":                      func(p *Picked) { p.Sections = make([]PickedSection, maxPickedSections+1) },
		"a title past its bound":                 func(p *Picked) { p.Title = strings.Repeat("é", 201) },
		"a block past its bound":                 func(p *Picked) { p.Sections[0].Blocks[0].Text = strings.Repeat("x", 9001) },
		"a picture the message does not carry":   func(p *Picked) { p.Sections[0].Picture = "other.png" },
		"more pages than sections can hold":      func(p *Picked) { p.Pages = maxPickedSections + 1 },
		"a card past the whole bound": func(p *Picked) {
			for i := 0; i < 8; i++ {
				p.Sections = append(p.Sections, PickedSection{Kind: "element", Title: "x", Blocks: []PickedBlock{{Label: "HTML", Text: strings.Repeat("x", 9000)}}})
			}
		},
	}
	for name, change := range cases {
		card := pickedCard("Look here")
		change(&card)
		if _, err := svc.PromptPicked(created.ID, text, card, ""); err == nil {
			t.Fatalf("%s went in", name)
		}
	}
	if thread, _ := svc.Get(created.ID); len(thread.Messages) != 0 {
		t.Fatalf("a refused card left %+v", thread.Messages)
	}
	if _, err := svc.PromptPicked(created.ID, text, pickedCard("Look here"), "", Attachment{Name: "drawing.png", Media: "image"}); err != nil {
		t.Fatalf("a card in its shape: %v", err)
	}
}

func TestParsePickedReadsOnlyACardWithinItsBound(t *testing.T) {
	raw, _ := json.Marshal(pickedCard("Look here"))
	if p, err := ParsePicked(string(raw)); err != nil || p.Sections[0].Title != `1. button "Save"` {
		t.Fatalf("parse: %+v, %v", p, err)
	}
	for _, bad := range []string{"", "[]", "{", `{"chips": "x"}`, strings.Repeat(" ", MaxPickedBytes+1)} {
		if _, err := ParsePicked(bad); err == nil {
			t.Fatalf("%.20q parsed", bad)
		}
	}
}
