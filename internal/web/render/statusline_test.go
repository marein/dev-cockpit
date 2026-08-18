package render

import (
	"errors"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder/claude/statusline"
)

func TestStatusLineStateNamesThePresetTheListIs(t *testing.T) {
	essentials := statusline.Presets[0].Entries()
	api := statusline.Presets[1].Entries()
	own := []statusline.Entry{{Kind: statusline.KindValue, Value: "model"}}
	unreadable := errors.New("Bound \"x\" is no number.")
	for _, tc := range []struct {
		name        string
		saved, list []statusline.Entry
		err         error
		picked      string
		isSaved     bool
		current     string
	}{
		{"the saved preset", essentials, essentials, nil, "essentials", true, "essentials"},
		{"the saved preset picked again", essentials, statusline.Presets[0].Entries(), nil, "essentials", true, "essentials"},
		{"another preset, not saved", essentials, api, nil, "api", false, "essentials"},
		{"an own line, not saved", essentials, own, nil, "", false, "essentials"},
		{"a preset over a saved own line", own, api, nil, "api", false, ""},
		{"the saved own line", own, own, nil, "", true, ""},
		{"a list the form cannot read", essentials, nil, unreadable, "", false, "essentials"},
		{"a list the form cannot read over an empty saved line", []statusline.Entry{}, nil, unreadable, "", false, ""},
	} {
		state := NewStatusLineState("/s", tc.saved, tc.list, tc.err)
		picked := ""
		if state.Picked != nil {
			picked = state.Picked.ID
		}
		current, matched := "", 0
		for _, row := range state.Presets {
			if row.Current {
				current = row.ID
			}
			if row.Matched {
				matched++
			}
		}
		if picked != tc.picked || state.Saved != tc.isSaved || current != tc.current {
			t.Errorf("%s: picked %q saved %v saved preset %q, want %q %v %q", tc.name, picked, state.Saved, current, tc.picked, tc.isSaved, tc.current)
		}
		if picked == "" && matched != 0 || picked != "" && matched != 1 {
			t.Errorf("%s: %d rows are marked as the list for the pick %q", tc.name, matched, picked)
		}
		if (tc.err != nil) != (state.Error != "") {
			t.Errorf("%s: the error reads %q", tc.name, state.Error)
		}
	}
}

// The bar under the body holds the preview and Save and nothing else, every
// row carries a head naming it with the grip in it, and a refusal of the state
// answer says which row it is about.
func TestStatusLinePageBarAndFoldingRows(t *testing.T) {
	entries := statusline.Presets[0].Entries()
	data := StatusLineData{
		Page:        Page{Title: "Status line", CSRFToken: "tok"},
		SettingsNav: SettingsNav{Selected: "claude", Section: "statusline", StatusLine: true},
		Base:        "/settings/coders/claude",
		Modes:       StatusLineModes(statusline.ModeFallback),
		State:       NewStatusLineState("/s", entries, entries, nil),
		Rows:        StatusLineRows(entries),
		Groups:      StatusLineGroups(),
		Colors:      statusline.Colors,
	}
	page := renderNamed(t, "statusline_form.gohtml", data)
	body := strings.Index(page, `class="dc-work-body`)
	foot := strings.Index(page, `<div class="dc-work-foot dc-statusline-foot" data-statusline-bar>`)
	if body < 0 || foot < body {
		t.Fatalf("the bar does not follow the body: body %d, bar %d", body, foot)
	}
	bar := page[foot : strings.Index(page[foot:], "</div>")+foot]
	for _, want := range []string{"data-statusline-preview", `form="statusline-form" data-statusline-save>Save</button>`} {
		if !strings.Contains(bar, want) {
			t.Fatalf("the bar misses %s: %s", want, bar)
		}
	}
	if strings.Count(bar, "<button") != 1 {
		t.Fatalf("the bar carries more than Save: %s", bar)
	}
	if got := strings.Count(page, "data-entry-toggle"); got != len(entries)+1 {
		t.Fatalf("want a head per row and the blank one, got %d", got)
	}
	for _, want := range []string{`data-entry-name>Model</span>`, `data-entry-name>Separator</span> <span class="text-secondary" data-entry-detail>·</span>`, `data-entry-name>Context used</span> <span class="text-secondary" data-entry-detail>c</span>`} {
		if !strings.Contains(page, want) {
			t.Fatalf("the rows miss %s", want)
		}
	}

	refused := NewStatusLineState("/s", entries, nil, rowRefusal{errors.New("Bound \"x\" is no number."), 3})
	if answer := renderNamed(t, "statusline_state.gohtml", refused); !strings.Contains(answer, `data-statusline-error data-error-row="3">`) {
		t.Fatalf("the refusal names no row: %s", answer)
	}
	whole := NewStatusLineState("/s", entries, nil, errors.New("At most 100 entries."))
	if answer := renderNamed(t, "statusline_state.gohtml", whole); !strings.Contains(answer, "data-statusline-error>") || strings.Contains(answer, "data-error-row") {
		t.Fatalf("a refusal of the whole list names a row: %s", answer)
	}
}

type rowRefusal struct {
	error
	row int
}

func (r rowRefusal) EntryRow() int { return r.row }
