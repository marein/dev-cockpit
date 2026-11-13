package render

import (
	"fmt"
	"strings"
	"time"
)

// LineNote is one note of a line comment thread as every place that shows the
// thread reads it: who wrote it in the reader's words, when, the note as
// written, and whether the reader may edit or delete it. ID is the reply's,
// empty for the note that opens the thread.
type LineNote struct {
	ID   string
	Name string
	At   time.Time
	Text string
	Edit bool
	Drop bool
}

// When is the note's time for the time element, empty where none is known.
func (n LineNote) When() string {
	if n.At.IsZero() || n.At.Unix() <= 0 {
		return ""
	}
	return n.At.UTC().Format(time.RFC3339)
}

// EditLabel names the edit of the note.
func (n LineNote) EditLabel() string {
	if n.ID == "" {
		return "Edit comment"
	}
	return "Edit reply"
}

// Required is what the edit of the note says when its field is left empty.
func (n LineNote) Required() string {
	if n.ID == "" {
		return "A comment is required."
	}
	return "A reply is required."
}

// LineThread is one line comment thread: where it stands, on a line or with
// Project on the whole project, the code line it quotes, whether that line is
// gone from the file, whether the thread is resolved, its notes and what the
// reader may do with it. A thread of the forge carries the forge's name and
// icon, its page there, whether the forge resolves it and its id there in
// Thread, empty for a guest and where the forge keeps no thread to answer.
// Local marks a thread that stands in the cockpit alone, Answers says the
// reader replies to it there, Writes that it resolves and reopens it there
// too, Deletes that the reader deletes it as the editor does. FileURL opens
// the editor at its line, empty for a project comment and for a guest whose
// link does not grant the project. Handoff is the draft the hand off of the
// assistant's project view puts into the composer.
type LineThread struct {
	ID           string
	Thread       string
	Project      bool
	Path         string
	Line         int
	Quote        string
	Outdated     bool
	Resolved     bool
	FileURL      string
	ForgeURL     string
	ForgeLabel   string
	ForgeIcon    string
	Unresolvable bool
	Local        bool
	Answers      bool
	Writes       bool
	Deletes      bool
	Notes        []LineNote
	Handoff      string
}

// Place is where the thread stands, file and line.
func (t LineThread) Place() string {
	return fmt.Sprintf("%s:%d", t.Path, t.Line)
}

// Origin says where the thread lives, the title of its local or forge mark.
func (t LineThread) Origin() string {
	switch {
	case t.Local:
		return "In the cockpit only"
	case t.Project:
		return "A general comment on " + t.ForgeLabel
	}
	return "A review thread on " + t.ForgeLabel
}

// LinePart is a piece of a value that stands on one line, an escape where the
// value holds a control character or a line or paragraph separator.
type LinePart struct {
	Text   string
	Escape bool
}

// VisibleLine cuts a path, a quoted code line or a name into the parts that
// keep it on one line, each control character and separator an escape of its
// own, the way the editor's visibleLine shows them.
func VisibleLine(value string) []LinePart {
	var parts []LinePart
	var run strings.Builder
	for _, r := range value {
		if !lineBreaking(r) {
			run.WriteRune(r)
			continue
		}
		if run.Len() > 0 {
			parts = append(parts, LinePart{Text: run.String()})
			run.Reset()
		}
		parts = append(parts, LinePart{Text: lineEscape(r), Escape: true})
	}
	if run.Len() > 0 {
		parts = append(parts, LinePart{Text: run.String()})
	}
	return parts
}

func lineBreaking(r rune) bool {
	return r <= 0x1f || (r >= 0x7f && r <= 0x9f) || r == ' ' || r == ' '
}

func lineEscape(r rune) string {
	switch r {
	case '\t':
		return `\t`
	case '\n':
		return `\n`
	case '\r':
		return `\r`
	case '\f':
		return `\f`
	case '\v':
		return `\v`
	case '\b':
		return `\b`
	case '\a':
		return `\a`
	}
	if r < 0x100 {
		return fmt.Sprintf(`\x%02x`, r)
	}
	return fmt.Sprintf(`\u%04x`, r)
}
