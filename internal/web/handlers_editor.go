package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/editorintelligence"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

type editorSaveForm struct {
	Path    string `form:"path"`
	Content string `form:"content"`
	Version string `form:"version"`
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
	// The language servers of the project's languages start with the page,
	// so their indexing runs while the reader still orients, not under the
	// first lookup; the statusbar indicator shows it meanwhile. The editor
	// page itself is editor action.
	s.intel.Touch(p.Name)
	go s.warmLSPServers(p)
	ret := s.formReturn(c)
	set := s.editorSettings()
	c.HTML(http.StatusOK, "project_editor.gohtml", render.EditorData{
		Page:         s.page(c, "Editor - "+p.Name, "projects"),
		Project:      p,
		MaxEditKiB:   filesystem.MaxEditableBytes / 1024,
		MaxEditSize:  filesystem.HumanSize(filesystem.MaxEditableBytes),
		Return:       ret,
		Projects:     s.editorSwitcher(p.Name, ret),
		DiffMaxLines: set.DiffMaxLines,
		DiffMaxKiB:   set.DiffMaxKiB,
		LSPExts:      s.lspSpec(),
		Terminal:     strings.TrimSpace(c.Query("terminal")),
	})
}

// editorSwitcher is the project list behind the tree header's switcher, one
// entry per project linking to its editor. current marks the project the page
// belongs to, and the back target rides along so a switch keeps the way out
// the reader arrived with.
func (s *Server) editorSwitcher(current, ret string) []render.EditorProject {
	list := s.projectsWithRunners()
	entries := make([]render.EditorProject, 0, len(list))
	for _, q := range list {
		entries = append(entries, render.EditorProject{
			Name:         q.Name,
			URL:          "/projects/" + url.PathEscape(q.Name) + "/editor?return=" + url.QueryEscape(ret),
			Current:      q.Name == current,
			Active:       q.Active(),
			LastUsedUnix: q.LastUsedUnix,
		})
	}
	return entries
}

// handleEditorProjects re-renders the switcher's rows alone, which is what an
// open editor pulls when a project is created, renamed or deleted somewhere
// else. It answers the same markup the page carries, so the browser never
// holds a second copy of those rows; the project this editor belongs to comes
// from the route and the back target from ?return, exactly as the page built
// them. A project that has just been deleted under this page is no error here:
// the switcher simply loses its row, and where that leaves the editor is the
// page's own business.
func (s *Server) handleEditorProjects(c *gin.Context) {
	c.HTML(http.StatusOK, "editor_projects.gohtml", render.EditorProjectsData{
		Projects: s.editorSwitcher(c.Param("name"), s.formReturn(c)),
	})
}

// lspSpec is the code navigation surface handed to the editor page:
// `ext:Label` pairs of the enabled profiles, comma joined, so the browser
// never mirrors the registry and a language switched off leaves no surface
// at all. A settings change reaches an open editor with its next load.
func (s *Server) lspSpec() string {
	pairs := make([]string, 0)
	for _, p := range editorintelligence.Profiles() {
		if s.lspProfileOff(p) {
			continue
		}
		for _, ext := range p.Extensions() {
			pairs = append(pairs, ext+":"+p.Label)
		}
	}
	return strings.Join(pairs, ",")
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
	// The signature travels with the listing so the client can hand it back on
	// its watch: it is how the server tells whether the rows a browser shows are
	// still the ones on the disk, without the browser hashing anything itself.
	c.JSON(http.StatusOK, gin.H{"path": c.Query("path"), "entries": entries, "sig": filesystem.DirSignature(entries)})
}

// handleEditorReadFile returns a file's text content at ?path= as JSON, with
// the version of exactly those bytes: the save carries it back, and that is
// what lets the write refuse to land on a file somebody else has written since
// (see filesystem.WriteFileTextIfUnchanged). Files the editor cannot edit
// (binary or too large) answer with a binary marker and their size, so the
// client can show a viewer or a download instead, and carry no version, there
// is no buffer of them to save.
func (s *Server) handleEditorReadFile(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	content, version, err := filesystem.ReadFileText(p.Path, c.Query("path"))
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
		"version":      version,
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

// handleEditorSaveFile writes editor content back to disk, and only onto the
// file the buffer was loaded from. The version the load path answered comes
// back with the save; a save whose version no longer describes the disk writes
// nothing and is refused with a 409 that names which of the two happened. The
// comparison itself is the filesystem package's, not this handler's, so the
// only place that decides whether a write may land is the one that writes.
//
// Nothing here can force the write, and there is deliberately no flag that
// could: the browser's two dialogs either leave the buffer alone, replace it
// with what is on disk, or write it as a new file where the old one is gone.
// A save without a version is the create path and writes, because a file
// created in the editor is saved before anything ever read it back.
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
	entry, version, err := filesystem.WriteFileTextIfUnchanged(p.Path, form.Path, []byte(form.Content), form.Version)
	if err != nil {
		if kind := saveConflictKind(err); kind != "" {
			c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "conflict": kind})
			return
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"entry": entry, "version": version})
}

// saveConflictKind names the refusal in the one word the browser dispatches on,
// and answers "" for everything that is a plain error. The two are apart
// because the dialogs are: a changed file offers to be read back over the
// buffer, a deleted one offers to be written again, and offering the wrong one
// is offering nothing. It is the save route's own marker, next to the boolean
// `conflict` the move, copy and upload routes answer a taken name with
// (editorWriteError): same field name, two routes, and neither reads the
// other's answers.
func saveConflictKind(err error) string {
	switch {
	case errors.Is(err, filesystem.ErrFileDeleted):
		return "deleted"
	case errors.Is(err, filesystem.ErrFileChanged):
		return "changed"
	}
	return ""
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
	// The one moment the server knows the old and the new name: the file's
	// line comments move along instead of orphaning, folders included.
	if s.lineComments.Rename(p.Name, form.Path, entry.RelPath) {
		s.publishLineComments(p.Name)
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
	if s.lineComments.Rename(p.Name, form.Path, entry.RelPath) {
		s.publishLineComments(p.Name)
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

// editorPollKey marks a POST below /editor that writes nothing. The two watch
// renewals are exactly that, a poll that carries a body, and they run every few
// seconds for as long as an editor is open: dropping the quick open index on
// them would rebuild it around the clock and defeat the one thing the file tick
// is careful about, which is to touch that index only where a directory really
// moved.
const editorPollKey = "editorPoll"

func editorPoll(c *gin.Context) { c.Set(editorPollKey, true) }

// invalidateQuickOpenAfterWrite drops the project's quick open index after a
// request that changed the tree, so a file is findable the moment it exists
// instead of after the staleness bound expires. Reads pass straight through.
func (s *Server) invalidateQuickOpenAfterWrite(c *gin.Context) {
	// Every route below /editor is editor action for the project, which is
	// what the language server lifetime measures; the lsp routes stay off
	// this group so their status poll never counts. A watch renewal counts
	// too: the page it comes from is open in front of somebody.
	s.intel.Touch(c.Param("name"))
	c.Next()
	if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead {
		return
	}
	if c.GetBool(editorPollKey) {
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

// handleEditorFilters answers what the palette's two filters can be set to in
// this project: the folders to scope to with the number of files under each,
// and the file name patterns that actually occur, the most common first. Both
// come out of the quick open index the palette already stands on, so this is
// one pass over paths that are in memory and never a walk of its own. The
// browser holds the answer and filters it while somebody types, which is why
// there is one route here and not one request per keystroke.
func (s *Server) handleEditorFilters(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	facts, err := s.quickOpen.Facts(p.Path, s.exclusions())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, facts)
}

// handleEditorSearch greps the project for the ?q= substring, or with ?re=1
// for the regex, and returns the matching lines, feeding the find in files
// palette. ?path= keeps the walk inside one project relative folder, ?file=
// filters the file names and ?case=1 makes the match mind its case; a request
// carrying none of them searches the whole project
// the way it always did, and the paths in the answer stay relative to the
// project root either way. A pattern that does not compile, and a folder that
// is not one, answer a 400 with the message, which the palette shows like any
// other search error.
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
	opt := filesystem.SearchOptions{
		Folder:        c.Query("path"),
		Mask:          filesystem.ParseFileMask(c.Query("file")),
		CaseSensitive: c.Query("case") == "1",
	}
	matches, truncated, err := filesystem.SearchFiles(p.Path, q, c.Query("re") == "1", s.exclusions(), opt)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"matches": matches, "truncated": truncated})
}

// maxSearchDraftField bounds one stored palette field. It is far beyond any
// query somebody types and only refuses a body that cannot be one.
const maxSearchDraftField = 4 << 10

// handleEditorSearchDraft serves the palette's stored inputs, so a search
// survives a jump into a hit, a reload and the walk from a phone to a desktop.
// It is also what a device catching up after the searchdraft event pulls.
// A project that never had one answers empty with no timestamp, which is what
// tells the palette to leave what it holds alone.
func (s *Server) handleEditorSearchDraft(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	draft := s.searchDrafts.Get(p.Name)
	updated := ""
	if !draft.UpdatedAt.IsZero() {
		updated = draft.UpdatedAt.Format(time.RFC3339Nano)
	}
	c.JSON(http.StatusOK, gin.H{
		"query":     draft.Query,
		"replace":   draft.Replace,
		"folder":    draft.Folder,
		"mask":      draft.Mask,
		"regex":     draft.Regex,
		"case":      draft.Case,
		"updatedAt": updated,
	})
}

type editorSearchDraftRequest struct {
	Query   string `json:"query"`
	Replace string `json:"replace"`
	Folder  string `json:"folder"`
	Mask    string `json:"mask"`
	Regex   bool   `json:"regex"`
	Case    bool   `json:"case"`
}

// handleEditorSearchDraftSave stores what the palette holds. The client sends
// it in bundles and once more when the palette closes, never per keystroke.
// A save that moved something is published as the searchdraft event, so the
// palette standing open on another device follows along.
func (s *Server) handleEditorSearchDraftSave(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorSearchDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	for _, field := range []string{req.Query, req.Replace, req.Folder, req.Mask} {
		if len(field) > maxSearchDraftField {
			c.JSON(http.StatusBadRequest, gin.H{"error": "That is too long to keep."})
			return
		}
	}
	draft, changed := s.searchDrafts.Save(p.Name, searchDraft{
		Query:   req.Query,
		Replace: req.Replace,
		Folder:  req.Folder,
		Mask:    req.Mask,
		Regex:   req.Regex,
		Case:    req.Case,
	})
	if changed {
		s.publishSearchDraft(p.Name)
	}
	response := gin.H{"saved": true}
	if !draft.UpdatedAt.IsZero() {
		response["updatedAt"] = draft.UpdatedAt.Format(time.RFC3339Nano)
	}
	c.JSON(http.StatusOK, response)
}

// handleEditorReplace answers what a replacement would change, and with
// ?apply=1 performs it. It stands on the search's own machinery: the same
// folder through ResolveUnder, the same mask, the same regex option, so a
// replacement can never reach what the search in front of it did not show.
//
// The answer counts every occurrence in the scope, not only the ones the
// preview carries: the button says what pressing it costs, and a list capped at
// MaxSearchMatches must never be mistaken for the size of the job. A file the
// browser holds unsaved stops the whole job before anything is written, which
// is a 409 naming those files.
func (s *Server) handleEditorReplace(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	q := strings.TrimSpace(c.PostForm("q"))
	if len(q) < 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Type at least 2 characters to replace."})
		return
	}
	line, _ := strconv.Atoi(c.PostForm("line"))
	req := filesystem.ReplaceRequest{
		Query:       q,
		Replacement: c.PostForm("to"),
		UseRegex:    c.PostForm("re") == "1",
		Options: filesystem.SearchOptions{
			Folder:        c.PostForm("path"),
			Mask:          filesystem.ParseFileMask(c.PostForm("file")),
			CaseSensitive: c.PostForm("case") == "1",
		},
		OnlyPath: c.PostForm("only"),
		OnlyLine: line,
		Dirty:    strings.Split(c.PostForm("dirty"), "\n"),
	}
	if c.PostForm("apply") != "1" {
		report, err := filesystem.PreviewReplace(p.Path, req, s.exclusions())
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
			return
		}
		c.JSON(http.StatusOK, report)
		return
	}
	report, err := filesystem.ApplyReplace(p.Path, req, s.exclusions())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	if len(report.Blocked) > 0 {
		c.JSON(http.StatusConflict, gin.H{
			"error":   "Save or close these files first, nothing was replaced: " + strings.Join(report.Blocked, ", "),
			"blocked": report.Blocked,
		})
		return
	}
	c.JSON(http.StatusOK, report)
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
			Working:       t.Working,
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

// handleEditorGitFile returns a file's text at a revision, HEAD without a
// ?rev=, which is the other side of the diff; the browser computes the diff
// itself, no route ever answers one. A path that is not in the revision is a
// normal answer, that is what a new file looks like; a revision the
// repository cannot resolve is the caller's mistake and answers a 400 in the
// package's words. Binary and too large carry the same markers the plain
// read route uses, so the client shows the same "cannot edit this" as there.
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
	content, exists, err := git.New(p.Path).FileAt(c.Request.Context(), c.Query("rev"), rel)
	if errors.Is(err, git.ErrRevision) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if err != nil {
		log.Printf("editor git file %s: %v", p.Path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "The file could not be read at that revision."})
		return
	}
	if !exists {
		c.JSON(http.StatusOK, gin.H{"path": rel, "exists": false, "content": ""})
		return
	}
	// Binary and too large both mean "not something to diff", but they are not
	// the same sentence to read, so the reason travels with the answer. A blob
	// that fills git's own output cap counts as too large as well: git
	// truncates silently, and the head of a file diffed against the whole of it
	// claims everything past the cut was deleted. The cap sits below the edit
	// limit, so this is the answer for a revision between the two; a file that
	// happens to be exactly the cap is called too large with it, which is the
	// safe way to be wrong here.
	if err := filesystem.CheckEditableText(content); err != nil || len(content) >= git.MaxOutput {
		reason := "large"
		if errors.Is(err, filesystem.ErrBinary) {
			reason = "binary"
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

// maxCommitDraftMessage bounds a stored draft message. It is far beyond any
// commit message and only refuses a body that cannot be one.
const maxCommitDraftMessage = 64 << 10

// handleEditorGitCommitDraft serves the commit panel's stored draft: the
// message, the picked paths and when they last moved. It is its own read, so
// a device catching up after the commitdraft event pulls the draft and
// nothing else.
func (s *Server) handleEditorGitCommitDraft(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	draft := s.commitDrafts.Get(p.Name)
	paths := draft.Paths
	if paths == nil {
		paths = []string{}
	}
	updated := ""
	if !draft.UpdatedAt.IsZero() {
		updated = draft.UpdatedAt.Format(time.RFC3339Nano)
	}
	c.JSON(http.StatusOK, gin.H{
		"message":      draft.Message,
		"paths":        paths,
		"amend":        draft.Amend,
		"amendMessage": draft.AmendMessage,
		"updatedAt":    updated,
	})
}

type editorCommitDraftRequest struct {
	Message      string   `json:"message"`
	Paths        []string `json:"paths"`
	Amend        bool     `json:"amend"`
	AmendMessage string   `json:"amendMessage"`
}

// handleEditorGitCommitDraftSave stores what the panel holds: every pause in
// typing and every pick lands here. Only a save that changed something is
// published, so the other devices are not woken for a draft that already is
// what they hold.
func (s *Server) handleEditorGitCommitDraftSave(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCommitDraftRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if len(req.Message) > maxCommitDraftMessage || len(req.AmendMessage) > maxCommitDraftMessage {
		c.JSON(http.StatusBadRequest, gin.H{"error": "That message is too long."})
		return
	}
	for _, rel := range req.Paths {
		if _, err := filesystem.ResolveUnder(p.Path, rel); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
			return
		}
	}
	draft, changed := s.commitDrafts.Save(p.Name, commitDraft{
		Message:      req.Message,
		Paths:        req.Paths,
		Amend:        req.Amend,
		AmendMessage: req.AmendMessage,
	})
	if changed {
		s.publishCommitDraft(p.Name)
	}
	response := gin.H{"saved": true}
	if !draft.UpdatedAt.IsZero() {
		response["updatedAt"] = draft.UpdatedAt.Format(time.RFC3339Nano)
	}
	c.JSON(http.StatusOK, response)
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
	// An amend may come with no paths at all: it then rewrites the message of
	// the last commit and nothing else.
	if len(req.Paths) == 0 && !req.Amend {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Pick at least one change to commit."})
		return
	}
	for _, rel := range req.Paths {
		if _, err := filesystem.ResolveUnder(p.Path, rel); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
			return
		}
	}
	// The commit and its ride-along push are one write, so the working copy is
	// taken once and held over both: a checkout that slipped between them
	// would push a branch nobody meant.
	writeKeys, ok := s.takeGitWrite(c, p)
	if !ok {
		return
	}
	defer s.gitWrites.release(writeKeys...)
	// The bridge for the ride-along push is opened before the commit, not
	// between the two: a refused bridge has to refuse the whole request, and
	// after the commit there would be nothing left to refuse.
	var action *askpass.Action
	var prompt *git.Prompt
	if req.Push {
		var ok bool
		if action, prompt, ok = s.promptAction(p.Name, "push"); !ok {
			c.JSON(http.StatusConflict, gin.H{"error": gitInUse})
			return
		}
		if action != nil {
			defer action.End()
		}
	}
	result, err := git.New(p.Path).Commit(gitWriteContext(c), req.Message, req.Paths, req.Amend)
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
		pushRepo := git.New(p.Path)
		if prompt != nil {
			pushRepo = pushRepo.WithPrompt(prompt)
		}
		if err := pushRepo.Push(gitWriteContext(c), false); err != nil {
			log.Printf("editor git push %s: %v", p.Path, err)
			response["pushError"] = promptRefusal(action, err)
		} else {
			response["pushed"] = true
		}
	}
	// The commit spent the draft, on every device: the panel that sent it
	// empties itself, the others hear it here.
	if s.commitDrafts.Clear(p.Name) {
		s.publishCommitDraft(p.Name)
	}
	s.publishGit(p.Name, true)
	c.JSON(http.StatusOK, response)
}

// editorLogPage is one page of history: small enough for a sheet, large
// enough that paging is the exception.
const editorLogPage = 30

// editorRefsCap bounds one round of the picker's list, per kind and not
// across all of them, so a repository full of tags cannot push the branches
// out. Without a search text it caps the recently moved, with one it caps the
// matches, which is what makes it a page and not the whole world: while the
// browser did the filtering this had to be large enough to hold everything
// somebody might type, and now the typing is answered by git, so it is the
// size of a list a person reads.
const editorRefsCap = 50

// editorFetchMaxAge is how old the last fetch may be before an automatic one
// runs. Opening the git surface and listing remote branches want counts that
// are roughly current, not a network round trip on every glance.
const editorFetchMaxAge = 2 * time.Minute

// handleEditorGitLog answers one page of history, newest first: the file's
// with ?path=, the project's without one. ?skip= pages through it, and the
// answer says whether older commits exist beyond the page.
func (s *Server) handleEditorGitLog(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	rel := c.Query("path")
	if rel != "" {
		if _, err := filesystem.ResolveUnder(p.Path, rel); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
			return
		}
	}
	skip, _ := strconv.Atoi(c.Query("skip"))
	page, err := git.New(p.Path).Log(c.Request.Context(), rel, skip, editorLogPage)
	if err != nil {
		log.Printf("editor git log %s: %v", p.Path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "The history could not be read."})
		return
	}
	c.JSON(http.StatusOK, page)
}

// editorRefKinds is what `?kinds=` may name, and the answer when it names
// nothing: the three kinds of name, which is what the branch picker asks for
// and what an older page that knows no parameter at all still gets.
var editorRefKinds = map[string]bool{
	git.KindBranch: true,
	git.KindRemote: true,
	git.KindTag:    true,
	git.KindCommit: true,
}

// handleEditorGitRefs answers one round of a picker's search: the names, and
// with `kinds=…,commit` the commits too. `?q=` is what was typed, and the
// search runs here and not in the browser, because the browser only ever had
// the first `editorRefsCap` names of each kind to search through and a
// repository is not obliged to keep the interesting one among them.
//
// It only reads: a client that wants the remotes up to date first asks the
// quiet fetch for it (`POST .../git/fetch` with auto), which is the one route
// that touches the network, so nothing a GET can be talked into changes
// anything.
func (s *Server) handleEditorGitRefs(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	kinds := []string{git.KindBranch, git.KindRemote, git.KindTag}
	if asked := c.Query("kinds"); asked != "" {
		kinds = kinds[:0]
		for _, kind := range strings.Split(asked, ",") {
			kind = strings.TrimSpace(kind)
			if editorRefKinds[kind] {
				kinds = append(kinds, kind)
			}
		}
	}
	found, err := git.New(p.Path).Refs(c.Request.Context(), git.RefSearch{
		Text:  c.Query("q"),
		Kinds: kinds,
		Limit: editorRefsCap,
	})
	if err != nil {
		log.Printf("editor git refs %s: %v", p.Path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "The refs could not be read."})
		return
	}
	c.JSON(http.StatusOK, found)
}

// handleEditorGitCompare answers what differs between two revisions, the
// comparison the editor's revision diff panel lists: `?from=` and `?to=` are
// whatever git resolves (a branch, a tag, a hash, HEAD~3), both empty takes
// the repository's own suggestion, and `?mode=` picks the question, `since`
// (the default) for what the right side changed since it split from the left,
// git's three dots, `direct` for everything that differs between the two,
// git's two dots. A name git cannot resolve is the caller's mistake and
// answers a 400 naming the side; two revisions without shared history refuse
// the split question with a 409 in the package's words. It only reads.
func (s *Server) handleEditorGitCompare(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	got, err := git.New(p.Path).Compare(c.Request.Context(), git.CompareRequest{
		From:  c.Query("from"),
		To:    c.Query("to"),
		Since: c.Query("mode") != "direct",
	})
	var refused *git.RevisionError
	switch {
	case errors.As(err, &refused):
		c.JSON(http.StatusBadRequest, gin.H{"error": refused.Error(), "side": refused.Side, "from": got.From, "to": got.To})
		return
	case errors.Is(err, git.ErrNoSplit):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error(), "from": got.From, "to": got.To})
		return
	case err != nil:
		log.Printf("editor git compare %s: %v", p.Path, err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "The revisions could not be compared."})
		return
	}
	c.JSON(http.StatusOK, got)
}

// gitWriteContext is what a git write runs on: everything the request
// carries, except its cancellation. A browser that goes away — a closed tab,
// a phone that locked, a line that dropped — must never end a write halfway
// through, because ending one means SIGKILL to the whole process group: a
// checkout stopped mid working copy, or half a clone left in the project
// directory, which git then refuses to clone into ever again. What ends a
// write is its own timeout, minutes long and the package's own, and nothing
// else. A read may keep hanging on the request, there is nobody left to
// answer it anyway.
func gitWriteContext(c *gin.Context) context.Context {
	return context.WithoutCancel(c.Request.Context())
}

// promptAction opens the askpass bridge for one user-triggered action, named
// by the project it runs in and the action's own name, which is what the
// dialog shows as this server's truth above ssh's or git's line. Whether a
// call may ask at all is the route's decision alone: only the handlers of the
// actions somebody started call this, a status poll or the quiet fetch never
// does, and everything without a bridge keeps failing prompts fast.
//
// The third value is false only when the project already runs a bridged
// action. The write lock in front of every caller makes that unreachable, so
// it is an invariant guard, but it may not be worked past: running the action
// anyway would leave it without a bridge, and every question would come back
// as an authentication failure with nothing saying that it was never asked.
// It opens the bridge through Begin and not BeginCommand, and that is the
// whole difference to the proxy's helper below: somebody started this on a
// page and is looking at it, so the question stays inside the app. Routing
// both through one constructor with empty strings would tie that decision to
// what the dialog renders again, which is exactly what askpass.Question.
// External exists to keep apart.
func (s *Server) promptAction(project, name string) (*askpass.Action, *git.Prompt, bool) {
	return s.beginPrompt(func() *askpass.Action { return s.askpassBroker.Begin(project, name) })
}

// promptActionCommand is promptAction for the git proxy, which asks on behalf
// of somebody who is not in the app: the command line and the working copy it
// runs in travel with the question, so the dialog can show what is about to
// run and where, and the question is the kind that leaves the app as news.
// See askpass.Question.External.
func (s *Server) promptActionCommand(project, name, command, dir string) (*askpass.Action, *git.Prompt, bool) {
	return s.beginPrompt(func() *askpass.Action { return s.askpassBroker.BeginCommand(project, name, command, dir) })
}

// beginPrompt is what the two share: no bridge wired means every prompt keeps
// failing fast and the call runs anyway, an open action carries the helper
// environment into the call.
func (s *Server) beginPrompt(begin func() *askpass.Action) (*askpass.Action, *git.Prompt, bool) {
	if s.askpassBroker == nil {
		return nil, nil, true
	}
	action := begin()
	if action == nil {
		return nil, nil, false
	}
	env := append(action.Env(),
		"SSH_ASKPASS="+s.askpassScript,
		"GIT_ASKPASS="+s.askpassScript,
	)
	return action, &git.Prompt{Env: env, Asked: action.Asked(), Answered: action.Answered()}, true
}

// gitInUse is what a second write on the same working copy reads, whichever
// page it came from. It names the repository and not the page, because that
// is what is busy, and not the project either: two projects can share one.
const gitInUse = "Another git action is already running in this repository. Wait for it to finish and try again."

// gitUnknownCopy is what a write reads when the working copy could not even
// be named. It is not the busy refusal: nothing is running, nothing was
// started, and git is what did not answer.
const gitUnknownCopy = "The repository could not be read, so this action was not started. Try again."

// gitWriteKeys names everything one write holds, and it is deliberately more
// than one name.
//
// The working copy is the name that makes two projects in one checkout meet,
// a project below the repository root included: keyed by the project alone
// they would take a lock each and let a checkout and a commit run at each
// other, which is the one thing this lock exists for.
//
// The project path is the name that exists before any repository does, and it
// is the clone's. A clone starts in an empty directory and git creates the
// `.git` in it within the first moments, so the working copy resolves long
// before the clone is through: taken alone, the clone would hold the project
// path while every write arriving a moment later resolves the fresh git
// directory and walks straight past it, for the minutes the clone still runs.
// Both names together close that, and they cost nothing extra, the project
// path is already in hand.
//
// A git that could not be asked ends the write here instead of guessing
// (`git.ErrNoAnswer`, kept apart from "no repository" in `WorkingCopy`): a
// guessed name is a name another write may not take, and two names for one
// working copy are no lock. Resolving costs the one rev-parse every git read
// starts with, milliseconds in front of a write that runs for seconds or
// minutes.
func gitWriteKeys(c *gin.Context, p project.Project) ([]string, bool) {
	key, inRepo, err := git.New(p.Path).WorkingCopy(c.Request.Context())
	if err != nil {
		return nil, false
	}
	if !inRepo {
		return []string{p.Path}, true
	}
	return []string{p.Path, key}, true
}

// takeGitWrite claims the working copy for one write and answers the refusal
// itself: a 409 when somebody else holds it, a 502 when git could not say
// what is being written at all. Every write route starts with it, and the
// caller releases the keys it hands back when the handler returns.
//
// A try and not a wait: the second request would otherwise sit behind a
// checkout for as long as that checkout takes, with a spinner and no way to
// tell the two apart. Refusing says which of the two happened.
func (s *Server) takeGitWrite(c *gin.Context, p project.Project) ([]string, bool) {
	keys, ok := gitWriteKeys(c, p)
	if !ok {
		log.Printf("editor git write %s: the working copy could not be named", p.Path)
		c.JSON(http.StatusBadGateway, gin.H{"error": gitUnknownCopy})
		return nil, false
	}
	if s.gitWrites.try(keys...) {
		return keys, true
	}
	c.JSON(http.StatusConflict, gin.H{"error": gitInUse})
	return nil, false
}

// promptRefusal is the error a bridged action answers with: git's words, and
// when the person pressed cancel, the honest reason the words alone would
// hide behind an authentication failure.
func promptRefusal(action *askpass.Action, err error) string {
	if action != nil && action.Cancelled() {
		return err.Error() + " — the question was cancelled."
	}
	return err.Error()
}

// gitWrite is the one way through for the sheet's write actions. Push, the
// explicit fetch, pull, checkout and clone each differ in a single line, the
// call they make on the repository, and everything around that line is the
// same in the same order: take the working copy, open the askpass bridge,
// hand the repository the bridge, and turn a refusal into a 409 in git's
// words. Written out five times, that order is five places to keep in step.
//
// What is left to a handler is what actually differs: bind its body, name the
// action, and answer. The bookkeeping ends when it returns, so a caller may
// only publish and answer while the answer is true, and must return at once
// when it is false: the refusal has been written by then, and the lock and
// the bridge are already on their way out.
//
// Three writes are deliberately not on this path, because they are not this
// shape. The commit opens its ride-along push's bridge before the commit and
// holds one lock across two calls; the created branch touches nothing that
// could ask and opens no bridge at all; and the quiet fetch holds a lock of
// its own that a commit or a push must never meet.
func (s *Server) gitWrite(c *gin.Context, p project.Project, name string, write func(*git.Repo) error) bool {
	if err := s.runGitWrite(c, p, name, write); err != nil {
		status := http.StatusConflict
		if errors.Is(err, errGitUnknownCopy) {
			status = http.StatusBadGateway
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return false
	}
	return true
}

// errGitUnknownCopy is the one refusal a caller has to tell apart from the
// others: git could not say what is being written, which is the surfaces'
// 502 and not the 409 every decided refusal is.
var errGitUnknownCopy = errors.New(gitUnknownCopy)

// runGitWrite is that same order without an answer: every refusal comes back
// as an error, so a surface that does not speak JSON, the create form's
// clone, can put it in a flash instead. gitWrite is this plus the status
// codes.
func (s *Server) runGitWrite(c *gin.Context, p project.Project, name string, write func(*git.Repo) error) error {
	keys, ok := gitWriteKeys(c, p)
	if !ok {
		log.Printf("git write %s: the working copy could not be named", p.Path)
		return errGitUnknownCopy
	}
	if !s.gitWrites.try(keys...) {
		return errors.New(gitInUse)
	}
	defer s.gitWrites.release(keys...)
	action, prompt, open := s.promptAction(p.Name, name)
	if !open {
		return errors.New(gitInUse)
	}
	if action != nil {
		defer action.End()
	}
	repo := git.New(p.Path)
	if prompt != nil {
		repo = repo.WithPrompt(prompt)
	}
	if err := write(repo); err != nil {
		log.Printf("git %s %s: %v", name, p.Path, err)
		return errors.New(promptRefusal(action, err))
	}
	return nil
}

type editorPushRequest struct {
	Force bool `json:"force"`
}

// handleEditorGitPush pushes the current branch to its upstream, on its own,
// without a commit in front of it. force is force-with-lease and sits behind
// the client's explicit confirmation; what git refuses comes back in git's
// words, like the commit's refusals do.
func (s *Server) handleEditorGitPush(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorPushRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if !s.gitWrite(c, p, "push", func(repo *git.Repo) error {
		return repo.Push(gitWriteContext(c), req.Force)
	}) {
		return
	}
	// The push moved the remote tracking ref, so ahead is stale everywhere;
	// the base did not move, no open diff has to refetch anything.
	s.publishGit(p.Name, false)
	c.JSON(http.StatusOK, gin.H{"pushed": true})
}

type editorFetchRequest struct {
	Auto bool `json:"auto"`
}

// handleEditorGitFetch fetches the remotes. auto is the quiet path the git
// surface takes when it opens: it only runs when the last fetch is old, so a
// glance costs no network. The explicit action always runs.
func (s *Server) handleEditorGitFetch(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorFetchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	// fetched is what the answer and the event hang on, so it starts false:
	// a repository without a remote fetches nothing, and telling every open
	// editor that something moved would be a round for nothing.
	fetched := false
	if req.Auto {
		fetched = s.quietFetch(c, p)
		if fetched {
			s.publishGit(p.Name, false)
		}
	} else {
		var ok bool
		if fetched, ok = s.fetchRemotes(c, p); !ok {
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"fetched": fetched})
}

// fetchRemotes is the fetch somebody started: the write lock, the askpass
// bridge and git's own words on a refusal, plus the event a fetch that
// brought something owes every open page. It answers whether one ran and
// whether the request is still the caller's to answer, a refusal has written
// its own status by then.
//
// It is a function rather than one handler's body because the create form's
// resync is the same act on another page (handleProjectFetch): one fetch,
// asked for by a person, wherever the button stands.
func (s *Server) fetchRemotes(c *gin.Context, p project.Project) (bool, bool) {
	fetched := false
	if !s.gitWrite(c, p, "fetch", func(repo *git.Repo) error {
		var err error
		fetched, err = repo.Fetch(gitWriteContext(c))
		return err
	}) {
		return false, false
	}
	if fetched {
		s.publishGit(p.Name, false)
	}
	return fetched, true
}

// quietFetchKeys names what the fetch nobody asked for holds. They are the
// working copy's names with a mark of their own in front, so two quiet
// fetches on one working copy still meet — FETCH_HEAD only ages once the
// running one is through, and a sheet plus a branch list on a slow line would
// otherwise start two that fight over the same refs — while a commit, a push,
// a checkout or a clone never meet them at all.
//
// Sharing the write lock is what this used to do, and it was the wrong
// question asked twice: that lock is for "somebody is rewriting the working
// copy", and a fetch writes remote refs and touches no file on disk. On a
// remote that does not answer, the quiet fetch behind an opening sheet held
// it for as long as its budget lasted, and every commit and push of those
// minutes was refused as busy with nothing on the page running.
func quietFetchKeys(keys []string) []string {
	quiet := make([]string, 0, len(keys))
	for _, key := range keys {
		quiet = append(quiet, "quiet-fetch\x00"+key)
	}
	return quiet
}

// quietFetch is the fetch the git surface runs on its way in. It stays quiet
// in both directions: it runs uninvited, so it may never open a dialog, and
// it refuses nothing either. A lock it cannot take, a git that cannot say
// what it would be writing, a remote that is not there and a fetch that
// failed all end the same way, with false and no word, because there is
// nothing on the page that said this was happening.
func (s *Server) quietFetch(c *gin.Context, p project.Project) bool {
	keys, named := gitWriteKeys(c, p)
	if !named {
		return false
	}
	quiet := quietFetchKeys(keys)
	if !s.gitWrites.try(quiet...) {
		return false
	}
	defer s.gitWrites.release(quiet...)
	fetched, err := git.New(p.Path).FetchIfStale(gitWriteContext(c), editorFetchMaxAge)
	if err != nil {
		log.Printf("editor git quiet fetch %s: %v", p.Path, err)
		return false
	}
	return fetched
}

// handleEditorGitPull brings the current branch up to its upstream, fast
// forward only: a branch that drifted apart is refused in git's words and the
// working copy stands untouched, there is no merge and no stash behind this
// route. There is nothing to bind, the pull carries no parameters.
func (s *Server) handleEditorGitPull(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	if !s.gitWrite(c, p, "pull", func(repo *git.Repo) error {
		return repo.Pull(gitWriteContext(c))
	}) {
		return
	}
	s.publishGit(p.Name, true)
	c.JSON(http.StatusOK, gin.H{"pulled": true})
}

type editorCheckoutRequest struct {
	Branch string `json:"branch"`
}

// handleEditorGitCheckout switches the working copy to a branch, a remote one
// included, which creates the local tracking branch on the way. Local changes
// the switch would overwrite are git's refusal to make, and it travels back
// in git's words with the working copy untouched.
func (s *Server) handleEditorGitCheckout(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCheckoutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if !s.gitWrite(c, p, "checkout", func(repo *git.Repo) error {
		return repo.Checkout(gitWriteContext(c), req.Branch)
	}) {
		return
	}
	s.publishGit(p.Name, true)
	c.JSON(http.StatusOK, gin.H{"branch": req.Branch})
}

type editorCloneRequest struct {
	URL string `json:"url"`
}

// handleEditorGitClone fills a project that is not a repository yet, straight
// into the project directory. git's refusals travel back in its words: a
// directory that already holds anything, and a remote that wants credentials
// this host cannot produce on its own.
func (s *Server) handleEditorGitClone(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorCloneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	// This is the write the project path key exists for: the directory holds no
	// repository at the start and a `.git` moments later, so the name the clone
	// took would stop being the name a second write resolves. The project path
	// is the same at both ends and holds for the clone's whole run.
	if !s.gitWrite(c, p, "clone", func(repo *git.Repo) error {
		return repo.Clone(gitWriteContext(c), req.URL)
	}) {
		return
	}
	s.publishGit(p.Name, true)
	c.JSON(http.StatusOK, gin.H{"cloned": true})
}

type editorBranchRequest struct {
	Branch string `json:"branch"`
}

// handleEditorGitBranch creates a branch at the current HEAD and switches to
// it. The commit under HEAD does not move, so the event says the base stood.
// Nothing on this path can ask anything, so no bridge is opened here and the
// request carries no prompt key to open one with; the client agrees, it runs
// this action with `ask: false` and has no key to send.
func (s *Server) handleEditorGitBranch(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorBranchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	writeKeys, ok := s.takeGitWrite(c, p)
	if !ok {
		return
	}
	defer s.gitWrites.release(writeKeys...)
	if err := git.New(p.Path).CreateBranch(gitWriteContext(c), req.Branch); err != nil {
		log.Printf("editor git branch %s: %v", p.Path, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	s.publishGit(p.Name, false)
	c.JSON(http.StatusOK, gin.H{"branch": req.Branch})
}

type editorTagRequest struct {
	SHA     string `json:"sha"`
	Tag     string `json:"tag"`
	Message string `json:"message"`
	Push    bool   `json:"push"`
}

// handleEditorGitTag names a commit and, when asked, sends that tag to the
// remote. The two are one write and one bridge, like the commit and its
// ride-along push: the bridge is opened before the tag is created, because a
// refused bridge has to refuse the whole request, and a tag whose push is
// refused stands as a tag, so the answer stays a 200 and carries `pushed`
// with `pushError` beside it. Nothing here moves the working copy or HEAD, so
// the event says the base stood; what moved is a name every open editor shows
// in its history.
func (s *Server) handleEditorGitTag(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	writeKeys, ok := s.takeGitWrite(c, p)
	if !ok {
		return
	}
	defer s.gitWrites.release(writeKeys...)
	var action *askpass.Action
	var prompt *git.Prompt
	if req.Push {
		var open bool
		if action, prompt, open = s.promptAction(p.Name, "push tag"); !open {
			c.JSON(http.StatusConflict, gin.H{"error": gitInUse})
			return
		}
		if action != nil {
			defer action.End()
		}
	}
	if err := git.New(p.Path).Tag(gitWriteContext(c), req.Tag, req.SHA, req.Message); err != nil {
		log.Printf("editor git tag %s: %v", p.Path, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	response := gin.H{"tag": req.Tag, "pushed": false}
	if req.Push {
		repo := git.New(p.Path)
		if prompt != nil {
			repo = repo.WithPrompt(prompt)
		}
		if err := repo.PushTag(gitWriteContext(c), req.Tag); err != nil {
			log.Printf("editor git push tag %s: %v", p.Path, err)
			response["pushError"] = promptRefusal(action, err)
		} else {
			response["pushed"] = true
		}
	}
	s.publishGit(p.Name, false)
	c.JSON(http.StatusOK, response)
}

type editorTagNameRequest struct {
	Tag string `json:"tag"`
	// Remote says whether the deletion reaches past this repository. It is
	// never implied: what a remote holds is what everybody else sees.
	Remote bool `json:"remote"`
}

// handleEditorGitTagPush sends a tag that already exists, which is the half
// the create dialog leaves behind when its box stays unticked, and the only
// way to publish a tag a coder made on the command line.
func (s *Server) handleEditorGitTagPush(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorTagNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if !s.gitWrite(c, p, "push tag", func(repo *git.Repo) error {
		return repo.PushTag(gitWriteContext(c), req.Tag)
	}) {
		return
	}
	c.JSON(http.StatusOK, gin.H{"tag": req.Tag, "pushed": true})
}

// handleEditorGitTagDelete takes a tag away here, and on the remote when that
// was asked for. The two are one write and one bridge: the local deletion
// stands whatever the remote answers, so a refused remote deletion comes back
// beside a 200 like the commit's push does, and the client says both halves.
func (s *Server) handleEditorGitTagDelete(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorTagNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	writeKeys, ok := s.takeGitWrite(c, p)
	if !ok {
		return
	}
	defer s.gitWrites.release(writeKeys...)
	var action *askpass.Action
	var prompt *git.Prompt
	if req.Remote {
		var open bool
		if action, prompt, open = s.promptAction(p.Name, "delete tag"); !open {
			c.JSON(http.StatusConflict, gin.H{"error": gitInUse})
			return
		}
		if action != nil {
			defer action.End()
		}
	}
	if err := git.New(p.Path).DeleteTag(gitWriteContext(c), req.Tag); err != nil {
		log.Printf("editor git tag delete %s: %v", p.Path, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	response := gin.H{"tag": req.Tag, "remote": false}
	if req.Remote {
		repo := git.New(p.Path)
		if prompt != nil {
			repo = repo.WithPrompt(prompt)
		}
		if err := repo.DeleteRemoteTag(gitWriteContext(c), req.Tag); err != nil {
			log.Printf("editor git tag delete remote %s: %v", p.Path, err)
			response["remoteError"] = promptRefusal(action, err)
		} else {
			response["remote"] = true
		}
	}
	s.publishGit(p.Name, false)
	c.JSON(http.StatusOK, response)
}

type editorRevertRequest struct {
	Path string `json:"path"`
}

// handleEditorGitRevert discards what the working copy carries under one path,
// a file or a directory, back to HEAD: tracked changes are restored, staged
// edits included, and what has no state in HEAD is deleted, which the client's
// confirmation says before this is called. A local action like the created
// branch: nothing on this path can ask anything, so no bridge is opened and
// the client runs it with ask false. What git refuses travels back in git's
// words; the base does not move, so the event says the worktree did.
func (s *Server) handleEditorGitRevert(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorRevertRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if req.Path == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A path is required."})
		return
	}
	if _, err := filesystem.ResolveUnder(p.Path, req.Path); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	writeKeys, ok := s.takeGitWrite(c, p)
	if !ok {
		return
	}
	defer s.gitWrites.release(writeKeys...)
	if err := git.New(p.Path).Revert(gitWriteContext(c), req.Path); err != nil {
		log.Printf("editor git revert %s: %v", p.Path, err)
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		return
	}
	s.publishGit(p.Name, false)
	c.JSON(http.StatusOK, gin.H{"reverted": true})
}

// handleGitPromptList answers the standing questions of every running git
// action, oldest first. It sits at the app level and not under a project,
// because the dialog does too: the page that started an action may be
// reloaded, updated away, or lying on a desk while another device answers, so
// every signed-in page shows what is being asked. The session is the whole
// authorization, single user by design.
func (s *Server) handleGitPromptList(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"questions": s.gitPromptViews()})
}

// gitPromptView is one standing question the way the dialog needs it: the
// broker's question, plus the notification entry it holds while it stands, or
// nothing when it holds none.
//
// The target is worked out here and not in the browser. Which questions become
// news is this server's rule (reconcileGitPromptNews) and the prefix is this
// server's spelling (notify.GitPromptTarget); a client deciding it again would
// be the same rule written a second time in another language, and a rename
// here would quietly stop the dialog from ever reading its entry.
type gitPromptView struct {
	askpass.Question
	Target string `json:"target,omitempty"`
}

func (s *Server) gitPromptViews() []gitPromptView {
	if s.askpassBroker == nil {
		return []gitPromptView{}
	}
	questions := s.askpassBroker.Questions()
	views := make([]gitPromptView, 0, len(questions))
	for _, q := range questions {
		view := gitPromptView{Question: q}
		if q.External {
			view.Target = notify.GitPromptTarget(q.Project)
		}
		views = append(views, view)
	}
	return views
}

type gitPromptAnswer struct {
	Project string `json:"project"`
	ID      string `json:"id"`
	Answer  string `json:"answer"`
	Cancel  bool   `json:"cancel"`
}

// handleGitPromptAnswer carries the typed answer, or the cancel, back to the
// helper that asked. The answer lives in this request and the broker's
// channel and nowhere else: not in a log line, not in a state file. ok says
// whether the broker took it; false is a question that was already answered
// on another device or whose action already ended, which the dialog swallows,
// its close travels on the gitprompt event.
func (s *Server) handleGitPromptAnswer(c *gin.Context) {
	var req gitPromptAnswer
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
		return
	}
	if s.askpassBroker == nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	action := s.askpassBroker.Find(req.Project)
	if action == nil {
		c.JSON(http.StatusOK, gin.H{"ok": false})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": action.Answer(req.ID, req.Answer, req.Cancel)})
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

// editorWatchRequest is what one client says it has on the screen: the open
// tabs and the unfolded folders, each with the token this client holds for it,
// plus the id of that screen. The id is what makes the union a union: without
// it two browsers on one project would overwrite each other's scope and each
// would only ever be watched for as long as it was the last one to renew. The
// tokens are the server's own answers handed back, a file's version and a
// folder's listing signature, and they are what lets the first round compare
// the disk against the screen instead of against itself.
type editorWatchRequest struct {
	Client string        `json:"client"`
	Files  []watchedPath `json:"files"`
	Dirs   []watchedPath `json:"dirs"`
}

// handleEditorWatch registers one client's scope for a short window, the way
// the git watch registers its interest. That window is what keeps the project's
// file tick running, so a page renews it while it is open and the tick ends
// once the last editor is gone. Watching false means there is nothing to renew,
// because the poll is turned off.
func (s *Server) handleEditorWatch(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorWatchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The watch could not be read."})
		return
	}
	watching := s.watchProjectFiles(p, req.Client, req.Files, req.Dirs, s.editorSettings())
	c.JSON(http.StatusOK, gin.H{"watching": watching, "seconds": int(fileWatchWindow / time.Second)})
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
