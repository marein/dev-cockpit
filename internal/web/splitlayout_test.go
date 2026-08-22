package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/web/render"
)

// members builds a group in @dc_tab_gpos order out of "<id>:<column>" pairs, a
// column of 0 meaning the member carries no @dc_tab_gcol at all.
func members(specs ...string) []render.TerminalTab {
	out := make([]render.TerminalTab, len(specs))
	for i, spec := range specs {
		id, col, _ := strings.Cut(spec, ":")
		out[i] = render.TerminalTab{ID: id, GroupPos: i + 1}
		if col != "" && col != "0" {
			fmt.Sscanf(col, "%d", &out[i].GroupCol)
		}
	}
	return out
}

// place renders one member's cell the way the page reads: column, first row,
// last row, and the visual order the keyboard steps in.
func place(cell splitCell) string {
	return fmt.Sprintf("c%d r%d-%d o%d", cell.Col, cell.Row, cell.Row+cell.RowSpan-1, cell.Order)
}

func TestSplitLayout(t *testing.T) {
	cases := []struct {
		name  string
		group []render.TerminalTab
		cols  int
		rows  int
		want  []string
	}{{
		// What every split looked like before columns existed, and what a
		// group whose members carry no @dc_tab_gcol still looks like.
		name:  "members without a column are columns of their own",
		group: members("a", "b", "c"),
		cols:  3,
		rows:  1,
		want:  []string{"c1 r1-1 o0", "c2 r1-1 o1", "c3 r1-1 o2"},
	}, {
		// The example layout: two stacked on the left, one at full height on
		// the right.
		name:  "a stacked column beside a single pane",
		group: members("a:1", "b:1", "c:2"),
		cols:  2,
		rows:  2,
		want:  []string{"c1 r1-1 o0", "c1 r2-2 o1", "c2 r1-2 o2"},
	}, {
		name:  "columns of two and three share six rows",
		group: members("a:1", "b:1", "c:2", "d:2", "e:2"),
		cols:  2,
		rows:  6,
		want:  []string{"c1 r1-3 o0", "c1 r4-6 o1", "c2 r1-2 o2", "c2 r3-4 o3", "c2 r5-6 o4"},
	}, {
		// The stacking order inside a column is @dc_tab_gpos, which is also
		// the order the members arrive in.
		name:  "the flat order stacks the column",
		group: members("a:1", "b:2", "c:1"),
		cols:  2,
		rows:  2,
		want:  []string{"c1 r1-1 o0", "c2 r1-2 o2", "c1 r2-2 o1"},
	}, {
		// A column stands where its first member stands, never by the raw
		// option value: whatever wrote those numbers, the page is defined.
		name:  "a column stands where its first member stands",
		group: members("a:7", "b:3", "c:7"),
		cols:  2,
		rows:  2,
		want:  []string{"c1 r1-1 o0", "c2 r1-2 o2", "c1 r2-2 o1"},
	}, {
		name:  "a member without a column stands between two that have one",
		group: members("a:1", "b", "c:1"),
		cols:  2,
		rows:  2,
		want:  []string{"c1 r1-1 o0", "c2 r1-2 o2", "c1 r2-2 o1"},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cols, rows, cells := splitLayout(tc.group)
			if cols != tc.cols || rows != tc.rows {
				t.Fatalf("tracks = %d cols, %d rows, want %d, %d", cols, rows, tc.cols, tc.rows)
			}
			for i, cell := range cells {
				if got := place(cell); got != tc.want[i] {
					t.Errorf("%s: %s, want %s", tc.group[i].ID, got, tc.want[i])
				}
			}
		})
	}
}

// Every column has to be covered from the first row to the last, or a pane
// leaves a hole in the page.
func TestSplitLayoutFillsEveryColumn(t *testing.T) {
	group := members("a:1", "b:1", "c:1", "d:2", "e:2", "f:3")
	_, rows, cells := splitLayout(group)
	covered := map[int]int{}
	for _, cell := range cells {
		if cell.Row < 1 || cell.RowSpan < 1 || cell.Row+cell.RowSpan-1 > rows {
			t.Fatalf("cell out of the grid: %+v of %d rows", cell, rows)
		}
		covered[cell.Col] += cell.RowSpan
	}
	for col, span := range covered {
		if span != rows {
			t.Errorf("column %d covers %d of %d rows", col, span, rows)
		}
	}
}

func TestNormalizeSplitCols(t *testing.T) {
	cases := []struct {
		name string
		in   []int
		want []int
	}{{
		name: "left to right by first appearance",
		in:   []int{4, 4, 2},
		want: []int{1, 1, 2},
	}, {
		// A member that is a column of its own stays one, it is not numbered.
		name: "zero stays zero",
		in:   []int{0, 3, 0, 3},
		want: []int{0, 1, 0, 1},
	}, {
		name: "a negative index is no column",
		in:   []int{-2, 1},
		want: []int{0, 1},
	}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeSplitCols(tc.in)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}
