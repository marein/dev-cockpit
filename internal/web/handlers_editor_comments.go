package web

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
)

// maxLineCommentText bounds one stored note. It is far beyond any note a
// person writes and only refuses a body that cannot be one.
const maxLineCommentText = 16 << 10

// maxLineCommentQuote bounds the quoted code line. The quote is what the
// Markdown export cites, one line of code, so anything past this is cut
// rather than stored: the state file must not grow with a minified file
// somebody commented on.
const maxLineCommentQuote = 500

// clampLineQuote cuts the quoted code line to its cap, on a rune boundary so
// the stored text stays valid UTF-8.
func clampLineQuote(text string) string {
	if len(text) <= maxLineCommentQuote {
		return text
	}
	cut := maxLineCommentQuote
	for cut > 0 && !isRuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}

func isRuneStart(b byte) bool {
	return b&0xC0 != 0x80
}

// checkCommentPath verifies that a comment's path stays inside the project.
// The file itself does not have to exist: a note may outlive its file, and
// the sheet still wants to show it.
func checkCommentPath(c *gin.Context, root, rel string) bool {
	if strings.TrimSpace(rel) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A file path is required."})
		return false
	}
	if _, err := filesystem.ResolveUnder(root, rel); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return false
	}
	return true
}

// lineCommentView is one comment the way a read answers it: the stored note,
// plus whether its quote still matches the file. Outdated is judged on every
// read and never stored, so a file that comes back heals its comments by
// itself.
type lineCommentView struct {
	lineComment
	Outdated bool `json:"outdated,omitempty"`
}

// reconcileLineComments holds every stored quote against the file it points
// at, reading only the commented files. A quote that still stands where its
// line says is fine. A quote that moved and stands in the file exactly once
// is a comment to rebind, returned per file for the caller to persist; the
// view already shows the new line, so the answer never lags its own repair.
// Everything else is honestly outdated: the quote gone or ambiguous, the
// file missing, renamed or no longer readable as text. An empty quote never
// rebinds, every empty line would match it. Deliberately nothing fuzzy here:
// the quote matches exactly, or the comment is outdated.
func reconcileLineComments(root string, list []lineComment) ([]lineCommentView, map[string][]lineComment) {
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
	views := make([]lineCommentView, 0, len(list))
	rebinds := map[string][]lineComment{}
	for _, comment := range list {
		view := lineCommentView{lineComment: comment}
		lines, ok := linesOf(comment.Path)
		switch {
		case !ok:
			view.Outdated = true
		case comment.Line >= 1 && comment.Line <= len(lines) && clampLineQuote(lines[comment.Line-1]) == comment.LineText:
		case comment.LineText == "":
			view.Outdated = true
		default:
			at := 0
			hits := 0
			for i, line := range lines {
				if clampLineQuote(line) == comment.LineText {
					at = i + 1
					hits++
				}
			}
			if hits == 1 {
				view.Line = at
				rebinds[comment.Path] = append(rebinds[comment.Path], lineComment{ID: comment.ID, Line: at, LineText: comment.LineText})
			} else {
				view.Outdated = true
			}
		}
		views = append(views, view)
	}
	sort.SliceStable(views, func(i, j int) bool {
		if views[i].Path != views[j].Path {
			return views[i].Path < views[j].Path
		}
		return views[i].Line < views[j].Line
	})
	return views, rebinds
}

// reconciledLineComments is the one read every answer goes through: it judges
// the stored comments against the disk, persists what could be rebound and
// publishes the move, so every open editor follows the repair live.
func (s *Server) reconciledLineComments(p project.Project) []lineCommentView {
	views, rebinds := reconcileLineComments(p.Path, s.lineComments.List(p.Name))
	changed := false
	for path, moves := range rebinds {
		if s.lineComments.Move(p.Name, path, moves) {
			changed = true
		}
	}
	if changed {
		s.publishLineComments(p.Name)
	}
	return views
}

// handleEditorComments answers the project's line comments, which is what
// the gutter marks, the sheet and every catch-up after the linecomments
// event pull, each judged against the disk on the way out. `?path=` narrows
// the answer for the CLI's list, it may repeat and each value ors in an
// exact file, a folder, or a glob with * and **; the browser asks without
// one and gets everything.
func (s *Server) handleEditorComments(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	list := s.reconciledLineComments(p)
	if patterns := c.QueryArray("path"); len(patterns) > 0 {
		kept := list[:0:0]
		for _, comment := range list {
			if matchesAnyCommentPath(patterns, comment.Path) {
				kept = append(kept, comment)
			}
		}
		list = kept
	}
	c.JSON(http.StatusOK, gin.H{"comments": list})
}

type editorCommentSaveRequest struct {
	ID       string  `json:"id"`
	Path     string  `json:"path"`
	Line     int     `json:"line"`
	LineText *string `json:"lineText"`
	Text     string  `json:"text"`
}

// handleEditorCommentSave stores one note: a new one without an id, an edited
// one under its id, and a renamed file's note under its id with the new path.
// Only a save that changed something is published, so the other devices are
// not woken for a note they already hold.
func (s *Server) handleEditorCommentSave(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommentSaveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if !checkCommentPath(c, p.Path, req.Path) {
		return
	}
	if req.Line < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A line number is required."})
		return
	}
	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A comment is required."})
		return
	}
	if len(req.Text) > maxLineCommentText {
		c.JSON(http.StatusBadRequest, gin.H{"error": "That comment is too long."})
		return
	}
	// The quote is the sender's buffer line, stored untouched even when it
	// is empty: a note born in a dirty buffer talks about content the disk
	// does not know yet, other readers see it outdated until the save. A
	// request without the field is a caller without a buffer, the CLI's add,
	// and there the anchor is read from the disk, where it lives; a file
	// that cannot be read as text or a line past its end answer empty, and
	// the comment stands without a quote.
	quote := ""
	if req.LineText != nil {
		quote = *req.LineText
	} else {
		quote = diskLineQuote(p.Path, req.Path, req.Line)
	}
	comment, changed, err := s.lineComments.Save(p.Name, lineComment{
		ID:       req.ID,
		Path:     req.Path,
		Line:     req.Line,
		LineText: clampLineQuote(quote),
		Text:     req.Text,
	})
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if changed {
		s.publishLineComments(p.Name)
	}
	// An id nobody holds is a note deleted on another device meanwhile; the
	// answer says so instead of echoing the request back as stored.
	if comment.ID == "" {
		c.JSON(http.StatusOK, gin.H{"gone": true})
		return
	}
	c.JSON(http.StatusOK, gin.H{"comment": comment})
}

// diskLineQuote reads the code line a comment anchors to from the file on
// disk, empty when the file does not read as text or the line is past its
// end.
func diskLineQuote(root, rel string, line int) string {
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

type editorCommentsDeleteRequest struct {
	IDs      []string `json:"ids"`
	Paths    []string `json:"paths"`
	Outdated bool     `json:"outdated"`
	All      bool     `json:"all"`
}

// handleEditorCommentsDelete takes notes away: the named ones, whole files or
// folders through paths (each value an exact file, a folder, or a glob with
// * and **, ored together), the outdated ones — judged in the same read that
// answers the list, so a quote that can still be rebound is repaired first
// and never counts as an orphan; paths narrow which orphans fall — or with
// all everything the project holds, which is what the sheet's delete-all
// spends. The paths are match expressions against stored comments, never
// filesystem access, so they are not resolved against the disk. The answer
// counts what fell, which is what the CLI reports.
func (s *Server) handleEditorCommentsDelete(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommentsDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	removed := 0
	switch {
	case req.All:
		removed = s.lineComments.Clear(p.Name)
	case req.Outdated:
		ids := []string{}
		for _, view := range s.reconciledLineComments(p) {
			if !view.Outdated {
				continue
			}
			if len(req.Paths) > 0 && !matchesAnyCommentPath(req.Paths, view.Path) {
				continue
			}
			ids = append(ids, view.ID)
		}
		removed = s.lineComments.Remove(p.Name, ids)
	case len(req.Paths) > 0:
		removed = s.lineComments.RemoveByPaths(p.Name, req.Paths)
	default:
		removed = s.lineComments.Remove(p.Name, req.IDs)
	}
	if removed > 0 {
		s.publishLineComments(p.Name)
	}
	c.JSON(http.StatusOK, gin.H{"deleted": removed > 0, "removed": removed})
}

type editorCommentsMoveRequest struct {
	Path     string                   `json:"path"`
	Comments []editorCommentMoveEntry `json:"comments"`
}

type editorCommentMoveEntry struct {
	ID       string `json:"id"`
	Line     int    `json:"line"`
	LineText string `json:"lineText"`
}

// handleEditorCommentsMove syncs where one file's notes stand after the open
// buffer edited around them: the client maps the lines through its changes
// and lands every pause here, debounced like the commit draft.
func (s *Server) handleEditorCommentsMove(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommentsMoveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if !checkCommentPath(c, p.Path, req.Path) {
		return
	}
	positions := make([]lineComment, 0, len(req.Comments))
	for _, entry := range req.Comments {
		if entry.ID == "" || entry.Line < 1 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
			return
		}
		positions = append(positions, lineComment{
			ID:       entry.ID,
			Line:     entry.Line,
			LineText: clampLineQuote(entry.LineText),
		})
	}
	if s.lineComments.Move(p.Name, req.Path, positions) {
		s.publishLineComments(p.Name)
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}
