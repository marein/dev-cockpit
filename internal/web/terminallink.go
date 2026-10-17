package web

import (
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/share"
)

// handleTerminalLink maps a file:// hyperlink a program printed in a terminal
// to the editor of the project holding the file, at the line and column a
// fragment like #12 or #12:3 names. A link outside every project answers 404
// and the terminal opens nothing, and so does one outside the link's projects
// for a guest, whose editors of them lie under the link's address.
func (s *Server) handleTerminalLink(c *gin.Context) {
	allowed := func(string) bool { return true }
	if g, ok := guestOf(c); ok {
		on := g.share.Targets(share.KindProject)
		allowed = func(name string) bool { return slices.Contains(on, name) }
	}
	if href := s.terminalLinkTarget(c.Query("url"), allowed); href != "" {
		c.JSON(http.StatusOK, gin.H{"href": accessOf(c).Base + href})
		return
	}
	c.JSON(http.StatusNotFound, gin.H{"error": "Not a project file."})
}

// terminalLinkTarget maps the link into the editor of its project when the
// project is allowed.
func (s *Server) terminalLinkTarget(raw string, allowed func(string) bool) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "file" || (u.Host != "" && !strings.EqualFold(u.Host, "localhost")) || !filepath.IsAbs(u.Path) {
		return ""
	}
	p, ok := s.projectAt(u.Path)
	if !ok || !allowed(p.Name) {
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
