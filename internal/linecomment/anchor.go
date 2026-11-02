package linecomment

import (
	"sort"
	"strings"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

// clampQuote cuts the quoted code line to its cap.
func clampQuote(text string) string {
	return clamp(text, maxQuote)
}

// clamp cuts text to at most max bytes, on a rune boundary so the stored text
// stays valid UTF-8.
func clamp(text string, max int) string {
	if len(text) <= max {
		return text
	}
	cut := max
	for cut > 0 && !isRuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

func isRuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

// FileLine is one line of a file the way a quote holds it. A file with
// Windows line endings ends every line with a carriage return, which belongs
// to the ending, not to the code.
func FileLine(line string) string {
	return clampQuote(strings.TrimSuffix(line, "\r"))
}

// DiskQuote reads the code line a comment anchors to from the file on disk,
// empty when the file does not read as text or the line is past its end.
func DiskQuote(root, rel string, line int) string {
	content, _, err := filesystem.ReadFileText(root, rel)
	if err != nil {
		return ""
	}
	lines := strings.Split(content, "\n")
	if line < 1 || line > len(lines) {
		return ""
	}
	return lines[line-1]
}

// View is one comment the way a read answers it: the stored note, plus
// whether its quote still matches the file. Outdated is judged on every read
// and never stored, so a file that comes back heals its comments by itself.
type View struct {
	Comment
	Outdated bool
}

// Reconcile holds every stored quote against the file it points at, reading
// only the commented files under root. A quote that still stands where its
// line says is fine. A quote that moved and stands in the file exactly once
// is a comment to rebind, returned per file for the caller to persist; the
// view already shows the new line, so the answer never lags its own repair.
// Everything else is honestly outdated: the quote gone or ambiguous, the
// file missing, renamed or no longer readable as text. An empty quote never
// rebinds, every empty line would match it. Deliberately nothing fuzzy here:
// the quote matches exactly, or the comment is outdated. A project comment
// stands on no file, it is never outdated and leads the answer.
func Reconcile(root string, list []Comment) ([]View, map[string][]Comment) {
	files := map[string][]string{}
	readable := map[string]bool{}
	linesOf := func(rel string) ([]string, bool) {
		if ok, seen := readable[rel]; seen {
			return files[rel], ok
		}
		content, _, err := filesystem.ReadFileText(root, rel)
		readable[rel] = err == nil
		if err == nil {
			files[rel] = strings.Split(content, "\n")
		}
		return files[rel], readable[rel]
	}
	views := make([]View, 0, len(list))
	rebinds := map[string][]Comment{}
	for _, comment := range list {
		view := View{Comment: comment}
		if comment.Project {
			views = append(views, view)
			continue
		}
		lines, ok := linesOf(comment.Path)
		// TODO(v2.0.0): a released build quoted the disk with the carriage
		// return of a Windows line ending, FileLine cuts it on both sides.
		quote := FileLine(comment.LineText)
		at, found := Locate(lines, comment.Line, quote)
		switch {
		case !ok || !found:
			view.Outdated = true
		case at != comment.Line:
			view.Line = at
			rebinds[comment.Path] = append(rebinds[comment.Path], Comment{ID: comment.ID, Line: at, LineText: quote})
		}
		views = append(views, view)
	}
	sort.SliceStable(views, func(i, j int) bool { return before(views[i].Comment, views[j].Comment) })
	return views, rebinds
}

// Locate answers the line of lines a comment at line quoting quote stands on:
// that line where it still holds the quote, else the one line holding it,
// false where none or several do. An empty quote is found only where it
// stands, every empty line would hold it.
func Locate(lines []string, line int, quote string) (int, bool) {
	if line >= 1 && line <= len(lines) && FileLine(lines[line-1]) == quote {
		return line, true
	}
	if quote == "" {
		return 0, false
	}
	at, hits := 0, 0
	for i, l := range lines {
		if FileLine(l) == quote {
			at = i + 1
			hits++
		}
	}
	if hits != 1 {
		return 0, false
	}
	return at, true
}

// Reconciled is the one read every answer goes through: it judges the
// project's comments against its files under root, persists what could be
// rebound and reports whether it did, so the caller can tell every open
// editor to follow the repair.
func (s *Service) Reconciled(project, root string) ([]View, bool) {
	views, rebinds := Reconcile(root, s.List(project))
	changed := false
	for path, moves := range rebinds {
		if s.Move(project, path, moves, Writer{}) {
			changed = true
		}
	}
	return views, changed
}
