package web

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/git"
	"github.com/local/dev-cockpit/internal/project"
	"github.com/local/dev-cockpit/internal/web/render"
)

type editorSaveForm struct {
	Path    string `form:"path"`
	Content string `form:"content"`
}

type editorMoveForm struct {
	Path      string `form:"path"`
	Dir       string `form:"dir"`
	Overwrite string `form:"overwrite"`
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
	ret := s.formReturn(c)
	list := s.projectsWithRunners()
	switcher := make([]render.EditorProject, 0, len(list))
	for _, q := range list {
		switcher = append(switcher, render.EditorProject{
			Name:         q.Name,
			URL:          "/projects/" + url.PathEscape(q.Name) + "/editor?return=" + url.QueryEscape(ret),
			Current:      q.Name == p.Name,
			Active:       len(q.ActiveCoderRefs) > 0 || len(q.ShellRefs) > 0,
			LastUsedUnix: q.LastUsedUnix,
		})
	}
	set := s.editorSettings()
	c.HTML(http.StatusOK, "project_editor.gohtml", render.EditorData{
		Page:         s.page(c, "Editor - "+p.Name, "projects"),
		Project:      p,
		MaxEditKiB:   filesystem.MaxEditableBytes / 1024,
		Return:       ret,
		Projects:     switcher,
		DiffMaxLines: set.DiffMaxLines,
		DiffMaxKiB:   set.DiffMaxKiB,
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

// inlineMediaExt are the types the viewer plays in place. They render in a
// media element, never as a document, so they carry no script risk. Serving
// them inline also keeps Range requests working, which is what lets the player
// seek instead of downloading the whole file first.
var inlineMediaExt = map[string]bool{
	".mp4": true, ".m4v": true, ".webm": true, ".ogv": true, ".mov": true,
	".mp3": true, ".m4a": true, ".wav": true, ".oga": true, ".ogg": true, ".flac": true,
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
	ext := strings.ToLower(filepath.Ext(name))
	if c.Query("download") == "1" || !(inlineImageExt[ext] || inlineMediaExt[ext]) {
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

// editorWriteError answers a failed write. A name that is already taken gets a
// 409 with a conflict flag, so the browser can ask whether to overwrite instead
// of ending the drag or the upload with a plain error.
func editorWriteError(c *gin.Context, err error) {
	if errors.Is(err, filesystem.ErrExists) {
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "conflict": true})
		return
	}
	c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
}

// handleEditorMove moves a file or folder into another folder, keeping its name.
// It backs the drag and drop in the file tree.
func (s *Server) handleEditorMove(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var form editorMoveForm
	if err := c.ShouldBind(&form); err != nil || form.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A path and a target folder are required."})
		return
	}
	entry, err := filesystem.MoveEntry(p.Path, form.Path, form.Dir, form.Overwrite == "1")
	if err != nil {
		editorWriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

// handleEditorExtract unpacks a tar, tar.gz or zip into a new folder next to it.
func (s *Server) handleEditorExtract(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var form editorPathForm
	if err := c.ShouldBind(&form); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A file path is required."})
		return
	}
	entry, err := filesystem.ExtractArchive(p.Path, form.Path)
	if err != nil {
		editorWriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

// handleEditorArchive streams a folder as a tar.gz. Both server platforms write
// it from the standard library, and macOS, Linux and Windows all unpack it with
// their built in tar.
func (s *Server) handleEditorArchive(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	dir, err := filesystem.ResolveUnder(p.Path, c.Query("path"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Folder not found."})
		return
	}
	name := filepath.Base(dir)
	if dir == filepath.Clean(p.Path) {
		name = p.Name
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", "application/gzip")
	c.Header("Content-Disposition", "attachment; filename=\""+name+".tar.gz\"")
	// The size is unknown up front, so this streams; an error mid stream can only
	// truncate the download, it is logged and the connection closed.
	if err := filesystem.WriteTarGz(c.Writer, dir, name); err != nil {
		log.Printf("editor archive %s: %v", dir, err)
		c.Abort()
	}
}

// handleEditorCopy copies a file or folder into another folder. It backs the
// copy and paste entries in the tree menu, whose clipboard lives in the browser.
func (s *Server) handleEditorCopy(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var form editorMoveForm
	if err := c.ShouldBind(&form); err != nil || form.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A path and a target folder are required."})
		return
	}
	entry, err := filesystem.CopyEntry(p.Path, form.Path, form.Dir, form.Overwrite == "1")
	if err != nil {
		editorWriteError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry})
}

// handleEditorFiles answers one quick open query with the best matching paths,
// feeding the quick open palette. With ?path= it answers for that directory
// only; the paths stay relative to the project either way.
//
// It used to ship the project's whole file list and let the browser filter it,
// which meant the list was capped and everything past the cap was unreachable:
// in a large project the palette could not open a file in src/ at all. Now the
// match runs here against a cached index of every path, and only the handful of
// paths the palette renders goes over the wire.
func (s *Server) handleEditorFiles(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	res, err := s.quickOpen.Query(p.Path, c.Query("q"), c.Query("path"), s.exclusions(), filesystem.QuickOpenLimit)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"files": res.Paths,
		// truncated means "there are more matches than the ones shown", which is
		// what the palette's footnote reports.
		"truncated": res.Total > len(res.Paths),
		"total":     res.Total,
		"indexed":   res.Indexed,
	})
}

// invalidateQuickOpenAfterWrite drops the project's quick open index after a
// request that changed the tree, so a file is findable the moment it exists
// instead of after the staleness bound expires. Reads pass straight through.
func (s *Server) invalidateQuickOpenAfterWrite(c *gin.Context) {
	c.Next()
	if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
		return
	}
	if c.Writer.Status() >= http.StatusMultipleChoices {
		return
	}
	p, err := s.projects.FindByName(c.Param("name"))
	if err != nil {
		return
	}
	s.quickOpen.Invalidate(p.Path)
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
	matches, truncated, err := filesystem.SearchFiles(p.Path, q, s.exclusions())
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
	overwrite := c.PostForm("overwrite") == "1"
	createDirs := c.PostForm("dirs") == "1"
	entries := make([]filesystem.Entry, 0, len(files))
	for _, header := range files {
		src, err := header.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
			return
		}
		entry, saveErr := filesystem.SaveUpload(p.Path, dir, header.Filename, src, overwrite, createDirs)
		closeErr := src.Close()
		if saveErr != nil {
			editorWriteError(c, saveErr)
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

// handleEditorTerminals renders the editor's terminal panel fragment: the
// project's live coders and shells in tab strip order, flat like the quick nav
// (groups are strip UI). The stream, resize and input routes are the same ones
// the attach pages use, so the panel is one more client of the same session.
func (s *Server) handleEditorTerminals(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	steered, prefill := s.watcher.Marks()
	page := s.page(c, "", "projects")
	sessions := []render.EditorTerminal{}
	for _, t := range s.terminalTabs() {
		if t.Project != p.Name {
			continue
		}
		et := render.EditorTerminal{
			ID:            t.ID,
			Name:          t.Name,
			Kind:          t.Kind,
			Coder:         t.Coder,
			URL:           t.URL,
			StreamURL:     t.URL + "/stream",
			ResizeURL:     t.URL + "/resize",
			InputURL:      t.URL + "/input",
			ScrollHistory: t.Kind == "shell",
			HasNews:       t.HasNews,
			Steered:       steered[t.ID],
			SteerPrefill:  prefill[t.ID],
		}
		if t.Kind == "coder" {
			if co, running, err := s.resolveRunning(t.ID); err == nil {
				if files, err := co.Coder().SessionRepository().ListFiles(running.Identifier); err == nil {
					et.FilesData = &render.CoderFilesData{
						Page:            page,
						Identifier:      t.ID,
						Files:           files,
						MaxUploadSizeMB: maxRequestBodyMegabytes(s.cfg.MaxRequestBodySize),
					}
				}
			}
		}
		sessions = append(sessions, et)
	}
	inactive := []render.EditorInactiveCoder{}
	for _, q := range s.projectsWithRunners() {
		if q.Name != p.Name {
			continue
		}
		for _, r := range q.InactiveCoderRefs {
			inactive = append(inactive, render.EditorInactiveCoder{
				ID:      r.ID,
				Name:    r.Name,
				Coder:   r.Coder,
				URL:     "/coders/" + r.ID + "/resume",
				HasNews: r.HasNews,
			})
		}
		break
	}
	c.HTML(http.StatusOK, "editor_terminals.gohtml", render.EditorTerminalsData{
		Sessions:  sessions,
		Inactive:  inactive,
		CSRFToken: page.CSRFToken,
	})
}

// handleEditorGitChanges returns what the working copy carries on top of HEAD,
// one entry per changed path with the line counts, which is what feeds the
// marks in the editor's file tree.
func (s *Server) handleEditorGitChanges(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	changes, err := git.New(p.Path).Changes(c.Request.Context())
	if err != nil {
		log.Printf("editor git changes %s: %v", p.Path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "The changes could not be read."})
		return
	}
	c.JSON(http.StatusOK, changes)
}

// handleEditorGitFile returns a file's text at HEAD, which is the other side
// of the diff; the browser computes the diff itself, no route ever answers
// one. A path that is not in HEAD is a normal answer, that is what a new file
// looks like. Binary and too large carry the same markers the plain read route
// uses, so the client shows the same "cannot edit this" as there.
func (s *Server) handleEditorGitFile(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	rel := c.Query("path")
	// The project root resolves fine and is not a file, so an empty path has to
	// be refused here: further down it is git that says no, and a missing
	// parameter would read as a repository that could not be asked.
	if rel == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A file path is required."})
		return
	}
	if _, err := filesystem.ResolveUnder(p.Path, rel); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	content, exists, err := git.New(p.Path).FileAt(c.Request.Context(), rel)
	if err != nil {
		log.Printf("editor git file %s: %v", p.Path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "The file could not be read at HEAD."})
		return
	}
	if !exists {
		c.JSON(http.StatusOK, gin.H{"path": rel, "exists": false, "content": ""})
		return
	}
	// Binary and too large both mean "not something to diff", but they are not
	// the same sentence to read, so the reason travels with the answer.
	if err := filesystem.CheckEditableText(content); err != nil {
		reason := "binary"
		if errors.Is(err, filesystem.ErrTooLarge) {
			reason = "large"
		}
		c.JSON(http.StatusOK, gin.H{"path": rel, "exists": true, "binary": true, "reason": reason, "size": len(content)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": rel, "exists": true, "content": string(content)})
}

// handleEditorGitBlame returns who last touched each line of the file on disk,
// so lines that are not committed yet answer as pending, which is what
// somebody who is still typing should see.
func (s *Server) handleEditorGitBlame(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	rel := c.Query("path")
	if rel == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A file path is required."})
		return
	}
	if _, err := filesystem.ResolveUnder(p.Path, rel); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	blame, err := git.New(p.Path).Blame(c.Request.Context(), rel)
	if err != nil {
		log.Printf("editor git blame %s %s: %v", p.Path, rel, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "The blame could not be read."})
		return
	}
	c.JSON(http.StatusOK, blame)
}

// handleEditorGitCommitInfo returns what the commit panel shows before
// anything is committed: the branch the commit would land on and the last
// commit's message, which is what an amend starts from.
func (s *Server) handleEditorGitCommitInfo(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	info, err := git.New(p.Path).CommitInfo(c.Request.Context())
	if err != nil {
		log.Printf("editor git commit info %s: %v", p.Path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "The repository could not be read."})
		return
	}
	c.JSON(http.StatusOK, info)
}

type editorCommitRequest struct {
	Message string   `json:"message"`
	Paths   []string `json:"paths"`
	Amend   bool     `json:"amend"`
	Push    bool     `json:"push"`
}

// handleEditorGitCommit records the picked paths as a commit. What git refuses
// travels back in git's own words: a hook that said no, a missing identity, a
// merge that forbids a partial commit are all sentences the person in front of
// the panel can act on, and no wording of ours says it better. A successful
// commit publishes the git event itself, base moved, so every other open
// editor of the project follows without waiting for the poller's next round.
func (s *Server) handleEditorGitCommit(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommitRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A commit message is required."})
		return
	}
	if len(req.Paths) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Pick at least one change to commit."})
		return
	}
	for _, rel := range req.Paths {
		if _, err := filesystem.ResolveUnder(p.Path, rel); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
			return
		}
	}
	result, err := git.New(p.Path).Commit(c.Request.Context(), req.Message, req.Paths, req.Amend)
	if err != nil {
		log.Printf("editor git commit %s: %v", p.Path, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	// The push rides behind the commit and never undoes it: a commit whose
	// push is refused is a successful commit with the refusal in the answer,
	// which is also why this stays a 200.
	response := gin.H{"hash": result.Hash, "subject": result.Subject, "pushed": false}
	if req.Push {
		if err := git.New(p.Path).Push(c.Request.Context()); err != nil {
			log.Printf("editor git push %s: %v", p.Path, err)
			response["pushError"] = err.Error()
		} else {
			response["pushed"] = true
		}
	}
	s.publishGit(p.Name, true)
	c.JSON(http.StatusOK, response)
}

// handleEditorGitWatch registers one client's interest in a project for a short
// window. That window is what keeps the project's poller running, so a page
// renews it while it is open and nothing polls once the last editor is gone.
// Watching false means there is nothing to renew, either because git or because
// the poll is turned off.
func (s *Server) handleEditorGitWatch(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	watching := s.watchProjectGit(p, s.editorSettings())
	c.JSON(http.StatusOK, gin.H{"watching": watching, "seconds": int(gitWatchWindow / time.Second)})
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
