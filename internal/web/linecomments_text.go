package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/plaintext"
)

// commentsText is what Copy as text puts on the clipboard: the project's line
// comments, an entry per thread. Its head line names the guest who wrote it,
// or the forge and the author there of a thread of the forge, first, then
// file, line, whether it is outdated or resolved, and the quoted code line,
// a project comment Project comment in place of file and line, the read
// lists those first, and every line of the note follows behind two spaces
// and > as written. Each
// reply follows as an entry of its own, indented by two more spaces, led by
// Reply and the guest who wrote it, or the forge and its author there. Path, quote and note show what would
// break a line, hide in it or turn it around as its escape, the way
// line-comment-list prints them, so no line leaves its entry or poses as
// another one.
func commentsText(project string, list []lineCommentView) string {
	lines := []string{"Line comments in " + plaintext.Visible(project) + ":", ""}
	for _, comment := range list {
		head := "- "
		switch {
		case comment.Forge != nil:
			head += plaintext.Visible(strings.TrimSpace(comment.Forge.Label+" "+comment.Author)) + ": "
		case comment.Author != "":
			head += "Guest " + plaintext.Visible(comment.Author) + ": "
		}
		if comment.Project {
			head += "Project comment"
		} else {
			head += plaintext.Visible(comment.Path) + ":" + strconv.Itoa(comment.Line)
		}
		if comment.Outdated {
			head += " (outdated)"
		}
		if comment.Resolved {
			head += " (resolved)"
		}
		if quote := strings.TrimSpace(comment.LineText); quote != "" {
			head += ", code: " + plaintext.Visible(quote)
		}
		lines = append(lines, head)
		lines = quoted(lines, "  ", comment.Text)
		for _, reply := range comment.Replies {
			head := "  - Reply"
			switch {
			case comment.Forge != nil:
				head += " of " + plaintext.Visible(strings.TrimSpace(comment.Forge.Label+" "+reply.Author))
			case reply.Author != "":
				head += " of Guest " + plaintext.Visible(reply.Author)
			}
			lines = quoted(append(lines, head+":"), "    ", reply.Text)
		}
	}
	return strings.Join(lines, "\n")
}

// quoted appends every line of a note behind indent and >.
func quoted(lines []string, indent, note string) []string {
	for _, line := range noteLines(note) {
		if line == "" {
			lines = append(lines, indent+">")
		} else {
			lines = append(lines, indent+"> "+plaintext.Visible(line))
		}
	}
	return lines
}

// noteLines are the lines of a note, ended by a line feed, a carriage return
// or both.
func noteLines(note string) []string {
	return strings.Split(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(note), "\n")
}

// handleEditorCommentsText answers the project's line comments as the text
// Copy as text copies, judged against the disk like every read.
func (s *Server) handleEditorCommentsText(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	w := commentWriterOf(c)
	views := s.reconciledLineComments(p)
	list := make([]lineCommentView, 0, len(views))
	for _, v := range views {
		list = append(list, s.commentView(w, v))
	}
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(commentsText(p.Name, list)))
}
