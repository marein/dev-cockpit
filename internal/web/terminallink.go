package web

import (
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
)

// handleTerminalLink maps a file:// hyperlink a program printed in a terminal
// to the editor of the project holding the file, at the line and column a
// fragment like #12 or #12:3 names. A link outside every project answers 404
// and the terminal opens nothing.
func (s *Server) handleTerminalLink(c *gin.Context) {
	if href := s.terminalLinkTarget(c.Query("url")); href != "" {
		c.JSON(http.StatusOK, gin.H{"href": href})
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "Not a project file."})
}

func (s *Server) terminalLinkTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) || !filepath.IsAbs(u.Path) {
		return ""
	}
	p, ok := s.projectAt(u.Path)
	if !ok {
		return ""
	}
	rel, _, ok := filesystem.RelUnder(p.Path, u.Path)
	if !ok {
		return ""
	}
	ref := filesystem.FileRef{Path: rel}
	if m := linkLocation.FindStringSubmatch(u.Fragment); m != nil {
		ref.Line, _ = strconv.Atoi(m[1])
		ref.Col, _ = strconv.Atoi(m[2])
	}
	return editorFileURL(p.Name, ref)
}

var linkLocation = regexp.MustCompile(`^([1-9]\d{0,8})(?::([1-9]\d{0,8}))?$`)
