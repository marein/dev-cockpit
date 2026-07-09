package web

import (
	"errors"
	"net/http"
	"path/filepath"
	"strings"

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

type editorRenameForm struct {
	Path    string `form:"path" binding:"required"`
	NewName string `form:"newName" binding:"required"`
}

type editorPreviewForm struct {
	Content string `form:"content"`
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
		Return:     s.formReturn(c),
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

// handleEditorReadFile returns a file's text content at ?path= as JSON. Files
// the editor cannot edit (binary or too large) answer with a binary marker and
// their size, so the client can show a viewer or a download instead.
func (s *Server) handleEditorReadFile(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	content, err := filesystem.ReadFileText(p.Path, c.Query("path"))
	if err != nil {
		if errors.Is(err, filesystem.ErrBinary) || errors.Is(err, filesystem.ErrTooLarge) {
			if _, info, statErr := filesystem.ResolveExistingFile(p.Path, c.Query("path")); statErr == nil {
				c.JSON(http.StatusOK, gin.H{"path": c.Query("path"), "binary": true, "size": info.Size()})
				return
			}
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"path":         c.Query("path"),
		"content":      content,
		"editorConfig": filesystem.EditorConfigForFile(p.Path, c.Query("path")),
	})
}

// inlineImageExt lists the raster image types the editor's viewer may load
// inline. Everything else is only served as an attachment; notably SVG, which
// could run scripts when navigated to directly.
var inlineImageExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".gif": true,
	".webp": true, ".avif": true, ".bmp": true, ".ico": true,
}

// handleEditorRaw streams the file at ?path= as bytes. With ?download=1, or
// for any type not safe to render inline, it is sent as an attachment.
func (s *Server) handleEditorRaw(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	target, _, err := filesystem.ResolveExistingFile(p.Path, c.Query("path"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	name := filepath.Base(target)
	if c.Query("download") == "1" || !inlineImageExt[strings.ToLower(filepath.Ext(name))] {
		c.FileAttachment(target, name)
		return
	}
	c.File(target)
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

// handleEditorRename renames a file or directory within its parent directory.
func (s *Server) handleEditorRename(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var form editorRenameForm
	if err := c.ShouldBind(&form); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A path and a new name are required."})
		return
	}
	entry, err := filesystem.RenameEntry(p.Path, form.Path, form.NewName)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

// handleEditorFiles returns every file path in the project as JSON, feeding the
// quick open palette.
func (s *Server) handleEditorFiles(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	files, truncated, err := filesystem.ListFilesRecursive(p.Path)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"files": files, "truncated": truncated})
}

// handleEditorSearch greps the project for the ?q= substring and returns the
// matching lines, feeding the find in files palette.
func (s *Server) handleEditorSearch(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	q := strings.TrimSpace(c.Query("q"))
	if len(q) < 2 {
		c.JSON(http.StatusOK, gin.H{"matches": []filesystem.SearchMatch{}, "truncated": false})
		return
	}
	matches, truncated, err := filesystem.SearchFiles(p.Path, q)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"matches": matches, "truncated": truncated})
}

// handleEditorUpload stores multipart file uploads into the directory at the
// dir form field. The global request body limit caps the upload size.
func (s *Server) handleEditorUpload(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Please choose a file to upload."})
		return
	}
	files := form.File["files"]
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Please choose a file to upload."})
		return
	}
	dir := c.PostForm("dir")
	entries := make([]filesystem.Entry, 0, len(files))
	for _, header := range files {
		src, err := header.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
			return
		}
		entry, saveErr := filesystem.SaveUpload(p.Path, dir, header.Filename, src)
		closeErr := src.Close()
		if saveErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, saveErr)})
			return
		}
		if closeErr != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, closeErr)})
			return
		}
		entries = append(entries, entry)
	}
	c.JSON(http.StatusOK, gin.H{"entries": entries})
}

// handleEditorPreview renders posted markdown to safe HTML for the editor's
// preview pane.
func (s *Server) handleEditorPreview(c *gin.Context) {
	if _, ok := s.editorProject(c); !ok {
		return
	}
	var form editorPreviewForm
	if err := c.ShouldBind(&form); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Preview content is required."})
		return
	}
	html, err := renderMarkdownPreview(form.Content)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Markdown could not be rendered."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"html": html})
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
