package render

import "strings"

// Picker feeds the dropdown that picks several things for a form (dc-picker),
// of every kind it offers in one list, each kind under its head when there is
// more than one. ID names the toggle a label points at, None the closed
// toggle with nothing picked.
type Picker struct {
	ID      string
	Label   string
	None    string
	Search  string
	NoMatch string
	Kinds   []PickerKind
}

// PickerKind is one kind of thing a picker offers. Projects come as the
// project palette's rows, so they order, match and name the way every palette
// does, any other kind as rows in the order given, matched by label and sub
// line. Each picked one goes with the form as one value of Field.
type PickerKind struct {
	Field    string
	Label    string
	Projects bool
	Choices  []PickerChoice
}

// PickerChoice is one thing a picker offers, picked when the page was opened
// for it. Value is what the form sends, Label what the picker names it, a
// project row reads Project, any other row its Sub line.
type PickerChoice struct {
	Value   string
	Label   string
	Sub     string
	Project EditorProject
	Picked  bool
}

// Key names a choice of the kind apart from every choice of another kind.
func (k PickerKind) Key(c PickerChoice) string {
	return k.Field + ":" + c.Value
}

// Offers reports whether the picker offers anything at all.
func (p Picker) Offers() bool {
	for _, k := range p.Kinds {
		if len(k.Choices) > 0 {
			return true
		}
	}
	return false
}

// Headed reports whether more than one kind offers something, which is when
// each stands under its head.
func (p Picker) Headed() bool {
	kinds := 0
	for _, k := range p.Kinds {
		if len(k.Choices) > 0 {
			kinds++
		}
	}
	return kinds > 1
}

// Summary names the picked ones on the closed picker.
func (p Picker) Summary() string {
	var names []string
	for _, k := range p.Kinds {
		for _, c := range k.Choices {
			if c.Picked {
				names = append(names, c.Label)
			}
		}
	}
	return strings.Join(names, ", ")
}

// Count is the number of picked ones.
func (p Picker) Count() int {
	n := 0
	for _, k := range p.Kinds {
		for _, c := range k.Choices {
			if c.Picked {
				n++
			}
		}
	}
	return n
}
