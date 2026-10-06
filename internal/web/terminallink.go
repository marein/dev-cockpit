package web

import (
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
)

// handleTerminalLink maps a file:// hyperlink a program printed in a terminal
// to the editor of the project holding the file. A link outside every project
// answers 404 and the terminal opens nothing.
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
	name := s.projects.ProjectNameFor(u.Path)
	if name == "" {
		return ""
	}
	p, err := s.projects.FindByName(name)
	if err != nil {
		return ""
	}
	rel, err := filepath.Rel(p.Path, filepath.Clean(u.Path))
	if err != nil || rel == "." {
		return ""
	}
	if _, err := filesystem.ResolveUnder(p.Path, filepath.ToSlash(rel)); err != nil {
		return ""
	}
	return "/projects/" + url.PathEscape(p.Name) + "/editor?file=" + url.QueryEscape(filepath.ToSlash(rel))
}
