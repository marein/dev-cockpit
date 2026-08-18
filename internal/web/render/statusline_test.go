package render

import (
	"errors"
	"testing"

	"github.com/marein/dev-cockpit/internal/coder/claude/statusline"
)

func TestStatusLineStateNamesThePresetTheListIs(t *testing.T) {
	essentials := statusline.Presets[0].Entries()
	api := statusline.Presets[1].Entries()
	own := []statusline.Entry{{Kind: statusline.KindValue, Value: "model"}}
	unreadable := errors.New("A color bound is a number, \"x\" is not.")
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
