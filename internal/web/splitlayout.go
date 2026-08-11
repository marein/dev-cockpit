package web

import (
	"sort"

	"github.com/local/dev-cockpit/internal/web/render"
)

// maxSplitRows bounds the grid's row tracks. The count is the least common
// multiple of the column depths, which stays tiny for real splits (two columns
// of two and three panes need six rows); the cap is the backstop for an absurd
// one, and the placement below stays correct without being perfectly even.
const maxSplitRows = 512

// splitCell is one pane's place in the split page's grid, 1 based like CSS
// grid lines: the column, the row its stack starts on and how many rows it
// covers. Order is the pane's place reading the columns left to right and
// each column top to bottom, 0 based: the page's visual order, which the
// keyboard stepping walks and which can differ from the flat @dc_tab_gpos
// order (a member created into a mid page column is last in the flat order
// but steps where it stands).
type splitCell struct {
	Col     int
	Row     int
	RowSpan int
	Order   int
}

// splitLayout folds a group's members into the columns the split page renders.
// The members arrive in @dc_tab_gpos order, which stays the group's one global
// order: it drives the strip label, the quick nav, the mobile swipe and the
// stacking inside a column. @dc_tab_gcol only says which members share a
// column; a member without one is a column of its own, which is exactly how
// every group rendered before columns existed.
//
// Which column stands where is the first appearance of its members in that
// order, never the raw option value: with no column set anywhere the panes
// come out left to right in gpos order (today's behavior), and a group whose
// options were written by an older or newer client still renders something
// defined. The writers number the columns left to right anyway, so the index
// reads as a position too.
//
// The panes share one grid instead of sitting in column containers, because
// moving a pane between columns has to be a style change: a DOM move would
// take the terminal island with it and reconnect its stream. Every column
// therefore divides the same row tracks, one pane spanning as many of them as
// its column's depth allows.
func splitLayout(members []render.TerminalTab) (cols, rows int, cells []splitCell) {
	cells = make([]splitCell, len(members))
	if len(members) == 0 {
		return 0, 0, cells
	}
	column := make([]int, len(members)) // member index -> column index, 0 based
	byOption := map[int]int{}           // @dc_tab_gcol -> column index
	var depth []int
	for i, m := range members {
		idx, ok := -1, false
		if m.GroupCol > 0 {
			idx, ok = byOption[m.GroupCol]
		}
		if !ok {
			idx = len(depth)
			depth = append(depth, 0)
			if m.GroupCol > 0 {
				byOption[m.GroupCol] = idx
			}
		}
		column[i] = idx
		depth[idx]++
	}
	rows = 1
	for _, d := range depth {
		if next := lcm(rows, d); next <= maxSplitRows {
			rows = next
		}
	}
	filled := make([]int, len(depth)) // column index -> panes placed so far
	for i := range members {
		c := column[i]
		span := rows / depth[c]
		if span < 1 {
			span = 1
		}
		row := filled[c]*span + 1
		filled[c]++
		if filled[c] == depth[c] || row+span > rows+1 {
			// The last pane of a column takes whatever is left, so a depth the
			// row count does not divide evenly still fills its column.
			span = rows + 1 - row
			if span < 1 {
				span = 1
			}
		}
		cells[i] = splitCell{Col: c + 1, Row: row, RowSpan: span}
	}
	byPlace := make([]int, len(cells))
	for i := range byPlace {
		byPlace[i] = i
	}
	sort.Slice(byPlace, func(a, b int) bool {
		if cells[byPlace[a]].Col != cells[byPlace[b]].Col {
			return cells[byPlace[a]].Col < cells[byPlace[b]].Col
		}
		return cells[byPlace[a]].Row < cells[byPlace[b]].Row
	})
	for rank, i := range byPlace {
		cells[i].Order = rank
	}
	return len(depth), rows, cells
}

// normalizeSplitCols renumbers a written column layout left to right, in the
// member order it is written in: whatever a client sent (a raw index, a value
// a member has carried since an older layout, a negative one), what lands on
// the sessions counts 1, 2, 3 by first appearance, and 0 stays 0, the member
// that is a column of its own. Members that share a value keep sharing a
// column, so the layout is preserved and only its numbering is tidied.
func normalizeSplitCols(cols []int) []int {
	out := make([]int, len(cols))
	seen := map[int]int{}
	next := 1
	for i, col := range cols {
		if col < 1 {
			continue
		}
		idx, ok := seen[col]
		if !ok {
			idx = next
			next++
			seen[col] = idx
		}
		out[i] = idx
	}
	return out
}

func lcm(a, b int) int {
	if a < 1 || b < 1 {
		return 1
	}
	return a / gcd(a, b) * b
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
