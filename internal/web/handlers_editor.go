package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/web/render"
)

type editorSaveForm struct {
	Path    string `form:"path"`
	Content string `form:"content"`
}

type editorPathForm struct {
	Path string `form:"path" binding:"required"`
}

// handleProjectEditor renders the editor page for one project.
func (s *Server) handleProjectEditor(c *gin.Context) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", "Project not found.")
		return
	}
	s.projects.Touch(p.Name)
	c.HTML(http.StatusOK, "project_editor.gohtml", render.EditorData{
		Page:       s.page(c, "Editor - "+p.Name, "projects"),
		Project:    p,
		MaxEditKiB: filesystem.MaxEditableBytes / 1024,
	})
}

// handleEditorList returns the directory listing at ?path= as JSON.
func (s *Server) handleEditorList(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	entries, err := filesystem.ListDir(p.Path, c.Query("path"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": c.Query("path"), "entries": entries})
}

// handleEditorReadFile returns a file's text content at ?path= as JSON.
func (s *Server) handleEditorReadFile(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	content, err := filesystem.ReadFileText(p.Path, c.Query("path"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": c.Query("path"), "content": content})
}

// handleEditorSaveFile writes editor content back to disk.
func (s *Server) handleEditorSaveFile(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var form editorSaveForm
	if err := c.ShouldBind(&form); err != nil || form.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A file path is required."})
		return
	}
	entry, err := filesystem.WriteFileText(p.Path, form.Path, []byte(form.Content))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

// handleEditorCreateFile creates a new empty file.
func (s *Server) handleEditorCreateFile(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var form editorPathForm
	if err := c.ShouldBind(&form); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A path is required."})
		return
	}
	entry, err := filesystem.CreateFile(p.Path, form.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

// handleEditorCreateDir creates a new directory.
func (s *Server) handleEditorCreateDir(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var form editorPathForm
	if err := c.ShouldBind(&form); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A path is required."})
		return
	}
	entry, err := filesystem.CreateDir(p.Path, form.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

// handleEditorDeletePath removes a file or directory.
func (s *Server) handleEditorDeletePath(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var form editorPathForm
	if err := c.ShouldBind(&form); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A path is required."})
		return
	}
	entry, err := filesystem.DeleteEntry(p.Path, form.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

// editorProject resolves the project for a JSON editor request, writing a JSON
// error and returning false when it cannot be found.
func (s *Server) editorProject(c *gin.Context) (project.Project, bool) {
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Project not found."})
		return project.Project{}, false
	}
	return p, true
}
