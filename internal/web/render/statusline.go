package render

import (
	"encoding/json"

	"github.com/local/dev-cockpit/internal/coder/claude/statusline"
)

// StatusLineData is the model for the claude status line settings: the switch,
// the list of entries as rows, and the two tables the preview needs client
// side. Enabled is the switch, and it is the only thing that decides whether
// the cockpit sets a status line at all; the rows stand either way, because a
// list nobody threw away is what switching back on brings back.
type StatusLineData struct {
	Page
	SettingsNav SettingsNav
	Base        string
	Enabled     bool
	Rows        []StatusLineRow
	Groups      []StatusLineGroup
	Colors      []statusline.Color
	// ValuesJSON and ColorsJSON carry the same two tables into the element,
	// so the preview resolves a value and a color the way the server does and
	// no second copy of either table lives in the client.
	ValuesJSON string
	ColorsJSON string
}

// StatusLineGroup is one heading of the value select with the values under it.
type StatusLineGroup struct {
	Label  string
	Values []statusline.Value
}

// StatusLineGroups is the value table as the select offers it.
func StatusLineGroups() []StatusLineGroup {
	groups := make([]StatusLineGroup, 0, len(statusline.Groups))
	for _, label := range statusline.Groups {
		groups = append(groups, StatusLineGroup{Label: label, Values: statusline.ValuesInGroup(label)})
	}
	return groups
}

// StatusLineRow is one entry on the form. Numeric says which half of the row
// applies, the bounds or the one color, and Text whether the row carries a
// text of its own, which a separator and the free text value do; both are
// server side so the first paint is already right. Hint is what the picked
// value is, the same sentence the element writes there when the pick changes.
type StatusLineRow struct {
	statusline.Entry
	Numeric bool
	HasText bool
	Hint    string
}

// StatusLineRowView is one row plus the tables its selects are built from, so
// the row markup is one template that the rendered rows and the blank one
// behind the add button share.
type StatusLineRowView struct {
	Row    StatusLineRow
	Groups []StatusLineGroup
	Colors []statusline.Color
}

// StatusLineBoundView is one bound plus the palette its select is built from.
type StatusLineBoundView struct {
	Bound  statusline.Threshold
	Colors []statusline.Color
}

// Bounds are the row's color bounds as their own views.
func (v StatusLineRowView) Bounds() []StatusLineBoundView {
	out := make([]StatusLineBoundView, 0, len(v.Row.Thresholds))
	for _, bound := range v.Row.Thresholds {
		out = append(out, StatusLineBoundView{Bound: bound, Colors: v.Colors})
	}
	return out
}

// RowViews are the stored entries as rows.
func (d StatusLineData) RowViews() []StatusLineRowView {
	views := make([]StatusLineRowView, 0, len(d.Rows))
	for _, row := range d.Rows {
		views = append(views, StatusLineRowView{Row: row, Groups: d.Groups, Colors: d.Colors})
	}
	return views
}

// BlankRow is what the add button clones: a value entry on the first value the
// list offers, no label, no bounds.
func (d StatusLineData) BlankRow() StatusLineRowView {
	row := StatusLineRow{Entry: statusline.Entry{Kind: statusline.KindValue}}
	if len(statusline.Values) > 0 {
		first := statusline.Values[0]
		row.Value = first.ID
		row.Numeric = first.Numeric
		row.Hint = first.Hint
	}
	return StatusLineRowView{Row: row, Groups: d.Groups, Colors: d.Colors}
}

// BlankBound is the bound behind the add button, and the one a row gets when
// it becomes a number, so the client carries no color name of its own.
func (d StatusLineData) BlankBound() StatusLineBoundView {
	return StatusLineBoundView{Bound: statusline.Threshold{At: 0, Color: "green"}, Colors: d.Colors}
}

// StatusLineRows pairs every entry with what its value is.
func StatusLineRows(entries []statusline.Entry) []StatusLineRow {
	rows := make([]StatusLineRow, 0, len(entries))
	for _, entry := range entries {
		row := StatusLineRow{Entry: entry}
		if value, ok := statusline.ValueByID(entry.Value); ok {
			row.Numeric = value.Numeric
			row.Hint = value.Hint
			row.HasText = value.ID == statusline.FreeTextValue
		}
		if entry.Kind == statusline.KindSeparator {
			row.HasText = true
			if entry.Text == "" {
				row.Text = statusline.DefaultSeparator
			}
		}
		rows = append(rows, row)
	}
	return rows
}

// statusLineValueJSON is one value as the preview reads it: what it is called,
// whether it carries bounds, whether its text is the entry's own, and what
// stands in for it before a save.
type statusLineValueJSON struct {
	ID      string  `json:"id"`
	Label   string  `json:"label"`
	Hint    string  `json:"hint"`
	Numeric bool    `json:"numeric"`
	Own     bool    `json:"own"`
	Sample  string  `json:"sample"`
	Number  float64 `json:"number"`
}

// StatusLineValuesJSON is the value table for the element.
func StatusLineValuesJSON() string {
	list := make([]statusLineValueJSON, 0, len(statusline.Values))
	for _, value := range statusline.Values {
		list = append(list, statusLineValueJSON{
			ID: value.ID, Label: value.Label, Hint: value.Hint, Numeric: value.Numeric,
			Own: value.ID == statusline.FreeTextValue, Sample: value.Sample, Number: value.Number,
		})
	}
	return encodeJSON(list)
}

// StatusLineColorsJSON is the palette for the element, name to the color the
// preview paints it with.
func StatusLineColorsJSON() string {
	table := map[string]string{}
	for _, color := range statusline.Colors {
		table[color.Name] = color.CSS
	}
	return encodeJSON(table)
}

func encodeJSON(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(raw)
}

// IsValue, IsSeparator and IsBreak keep the template free of comparisons
// against the stored strings.
func (r StatusLineRow) IsValue() bool     { return r.Kind == statusline.KindValue }
func (r StatusLineRow) IsSeparator() bool { return r.Kind == statusline.KindSeparator }
func (r StatusLineRow) IsBreak() bool     { return r.Kind == statusline.KindBreak }
