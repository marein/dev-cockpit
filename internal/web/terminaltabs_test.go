package web

import (
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/web/render"
)

// strip builds a current strip from ids, left to right, the way terminalTabs
// hands it to applyTabOrder.
func strip(ids ...string) []render.TerminalTab {
	tabs := make([]render.TerminalTab, len(ids))
	for i, id := range ids {
		tabs[i] = render.TerminalTab{ID: id}
	}
	return tabs
}

func TestApplyTabOrder(t *testing.T) {
	cases := []struct {
		name    string
		current []string
		posted  []string
		want    []string
	}{{
		// The tab strip and the quick nav post everything they show, which has
		// to keep meaning exactly what it says.
		name:    "a full post is the order",
		current: []string{"a1", "b1", "a2", "b2", "a3"},
		posted:  []string{"a3", "b2", "a2", "b1", "a1"},
		want:    []string{"a3", "b2", "a2", "b1", "a1"},
	}, {
		// The editor panel of project A shows a1, a2, a3 and posts only those.
		// They hold the slots 1, 3 and 5, so those three slots get reshuffled
		// and b1, b2 keep the seats 2 and 4 they had.
		name:    "a subset permutes its own slots",
		current: []string{"a1", "b1", "a2", "b2", "a3"},
		posted:  []string{"a3", "a1", "a2"},
		want:    []string{"a3", "b1", "a1", "b2", "a2"},
	}, {
		// Same in the shape where the posting project sits at the far end:
		// nothing may be pulled to the front.
		name:    "a subset at the end stays at the end",
		current: []string{"b1", "b2", "b3", "a1", "a2"},
		posted:  []string{"a2", "a1"},
		want:    []string{"b1", "b2", "b3", "a2", "a1"},
	}, {
		name:    "the posted order is what counts, not the order it arrives sorted in",
		current: []string{"a1", "b1", "a2"},
		posted:  []string{"a2", "a1"},
		want:    []string{"a2", "b1", "a1"},
	}, {
		// A session that ended while the drag was in flight.
		name:    "an unknown id is dropped and its slot is not invented",
		current: []string{"a1", "b1", "a2"},
		posted:  []string{"a2", "gone", "a1"},
		want:    []string{"a2", "b1", "a1"},
	}, {
		name:    "a duplicate counts once",
		current: []string{"a1", "b1", "a2"},
		posted:  []string{"a2", "a2", "a1"},
		want:    []string{"a2", "b1", "a1"},
	}, {
		name:    "one moved session is a no-op on the order",
		current: []string{"a1", "b1", "a2"},
		posted:  []string{"a1"},
		want:    []string{"a1", "b1", "a2"},
	}, {
		// Nothing resolved, so nothing is written: the caller turns an empty
		// answer into a tmux no-op instead of renumbering the whole strip.
		name:    "a post that resolves to nothing answers nothing",
		current: []string{"a1", "b1"},
		posted:  []string{"gone", "also-gone"},
		want:    nil,
	}, {
		name:    "an empty post answers nothing",
		current: []string{"a1", "b1"},
		posted:  nil,
		want:    nil,
	}}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := applyTabOrder(strip(tc.current...), tc.posted)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("applyTabOrder(%v, %v) = %v, want %v", tc.current, tc.posted, got, tc.want)
			}
		})
	}
}

// The write covers every live session, so two sessions can never end up on the
// same @dc_tab_pos. That is what a partial post used to produce, and what the
// strip then had to break with the start time.
func TestApplyTabOrderCoversEverySession(t *testing.T) {
	current := []string{"a1", "b1", "a2", "b2", "a3"}
	got := applyTabOrder(strip(current...), []string{"a3", "a1", "a2"})
	if len(got) != len(current) {
		t.Fatalf("got %d entries, want %d: %v", len(got), len(current), got)
	}
	seen := map[string]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("%q appears twice in %v", id, got)
		}
		seen[id] = true
	}
	for _, id := range current {
		if !seen[id] {
			t.Fatalf("%q went missing from %v", id, got)
		}
	}
}
