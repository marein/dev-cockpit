package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/plaintext"
)

// commentsText is what Copy as text puts on the clipboard: the project's line
// comments, an entry per comment. Its head line names the guest who wrote it
// first, then file, line and the quoted code line, and every line of the note
// follows behind two spaces and > as written. Path, quote and note show what
// would break a line, hide in it or turn it around as its escape, the way
// line-comment-list prints them, so no line leaves its entry or poses as
// another one.
func commentsText(project string, list []lineCommentView) string {
	lines := []string{"Line comments in " + plaintext.Visible(project) + ":", ""}
	for _, comment := range list {
		head := "- "
		if comment.Author != "" {
			head += "Guest " + plaintext.Visible(comment.Author) + ": "
		}
		head += plaintext.Visible(comment.Path) + ":" + strconv.Itoa(comment.Line)
		if comment.Outdated {
			head += " (outdated)"
		}
		if quote := strings.TrimSpace(comment.LineText); quote != "" {
			head += ", code: " + plaintext.Visible(quote)
		}
		lines = append(lines, head)
		for _, line := range noteLines(comment.Text) {
			if line == "" {
				lines = append(lines, "  >")
			} else {
				lines = append(lines, "  > "+plaintext.Visible(line))
			}
		}
	}
	return strings.Join(lines, "\n")
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
	c.Header("Cache-Control", "no-store")
	c.Data(http.StatusOK, "text/plain; charset=utf-8", []byte(commentsText(p.Name, s.reconciledLineComments(p))))
}
