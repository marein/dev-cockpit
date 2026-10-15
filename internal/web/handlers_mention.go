package web

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const (
	// mentionFileLimit bounds one answer of the file step. The popup stands
	// over a phone keyboard and shows a handful of rows, the quick open's
	// hundred would travel for nothing.
	mentionFileLimit = 50
	// maxMentionQuery bounds the typed part of a mention. Nobody types a path
	// longer than this, and the query is matched against every indexed path.
	maxMentionQuery = 256
	// maxProjectName is the longest directory name the file systems take.
	maxProjectName = 255
)

// handleMentionProjects answers the project step's rows: the editor palette's
// own entries, so both palettes order, match and name projects the same.
func (s *Server) handleMentionProjects(c *gin.Context) {
	c.HTML(http.StatusOK, "file_mention_projects.gohtml", render.EditorProjectsData{
		Projects: s.editorSwitcher(""),
	})
}

type mentionFile struct {
	Path string `json:"path"`
	Ref  string `json:"ref"`
}

// handleMentionFiles answers the file step from the quick open index, the one
// the editor's palette stands on: the same ranking, the same exclusions and a
// warm cache for both. Every hit is placed on the disk again, so what the
// browser inserts is a file that stands under the project right now.
func (s *Server) handleMentionFiles(c *gin.Context) {
	query := c.Query("q")
	if len(query) > maxMentionQuery {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The search is too long."})
		return
	}
	name := c.Query("project")
	if name == "" || len(name) > maxProjectName {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found."})
		return
	}
	p, err := s.projects.FindByName(name)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found."})
		return
	}
	res, err := s.quickOpen.Query(p.Path, query, "", s.exclusions(), mentionFileLimit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	placed := filesystem.FilesUnder(p.Path, res.Paths)
	files := make([]mentionFile, 0, len(placed))
	for _, f := range placed {
		files = append(files, mentionFile{Path: f.Rel, Ref: mentionRef(f.Abs)})
	}
	c.JSON(http.StatusOK, gin.H{
		"project":   p.Name,
		"root":      mentionRef(p.Path),
		"files":     files,
		"total":     res.Total,
		"indexed":   res.Indexed,
		"truncated": res.Total > len(res.Paths),
	})
}

// mentionRef is how a path reads in a message. The absolute path is what
// names a file without a guess, whichever project root it lies in and
// wherever the reader runs. A path with a blank or a quote in it goes in
// double quotes, so the reader sees where it ends.
func mentionRef(path string) string {
	if strings.ContainsAny(path, " \t\"'`") {
		return strconv.Quote(path)
	}
	return path
}
