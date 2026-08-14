package web

import (
	"context"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/editorintelligence"
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/project"
)

const maxLSPClientIDLength = 80

// editorLSPRequest is the JSON body of a navigation request. The content is
// the active unsaved buffer; the position counts UTF-16 units like the
// CodeMirror document.
type editorLSPRequest struct {
	Client   string `json:"client"`
	Path     string `json:"path"`
	Content  string `json:"content"`
	Position struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"position"`
}

type editorLSPCloseRequest struct {
	Client string `json:"client"`
	Path   string `json:"path"`
}

// editorLSPLocation is one target in a navigation answer: the service's
// location plus the preview line the usages panel shows.
type editorLSPLocation struct {
	editorintelligence.Location
	Preview string `json:"preview,omitempty"`
}

// lspSourceRoot finds the source root holding an absolute path, over every
// profile's roots, and the launcher that can read it. This is the one place
// the allowlist is asked: the read route serves out of it and the
// navigation routes let a path through by it, so a file the editor may open
// and a file it may look a symbol up in are the same set by construction.
func (s *Server) lspSourceRoot(project, target string) (editorintelligence.Launcher, editorintelligence.SourceRoot, bool) {
	launcher := s.lspLauncher()
	for _, profile := range editorintelligence.Profiles() {
		if root, ok := editorintelligence.FindSourceRoot(launcher.SourceRoots(project, profile), target); ok {
			return launcher, root, true
		}
	}
	return nil, editorintelligence.SourceRoot{}, false
}

// validLSPTarget checks the client id and where the path may point. A path
// of the project resolves under its root; an absolute one is a source
// outside the project and has to lie in one of the language servers' own
// source directories, which is what the cursor standing in a read only tab
// means. Everything else is refused here, before any of it reaches a
// server. The JSON error is written by this call.
func (s *Server) validLSPTarget(c *gin.Context, p project.Project, client, target string) bool {
	if client == "" || len(client) > maxLSPClientIDLength {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A client id is required."})
		return false
	}
	if target == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "A file path is required."})
		return false
	}
	if path.IsAbs(target) {
		if _, _, ok := s.lspSourceRoot(p.Name, target); !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": editorintelligence.ErrNoSourceRoot.Error()})
			return false
		}
		return true
	}
	if _, err := filesystem.ResolveUnder(p.Path, target); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return false
	}
	return true
}

// editorLSPRequest reads and validates a navigation body. A false return
// means the response is already written.
func (s *Server) editorLSPRequest(c *gin.Context) (project.Project, editorintelligence.Request, bool) {
	p, ok := s.editorProject(c)
	if !ok {
		return project.Project{}, editorintelligence.Request{}, false
	}
	var req editorLSPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body."})
		return project.Project{}, editorintelligence.Request{}, false
	}
	if !s.validLSPTarget(c, p, req.Client, req.Path) {
		return project.Project{}, editorintelligence.Request{}, false
	}
	// A language switched off in the settings answers a status instead of
	// starting a server: the page attribute already leaves the surface out,
	// this is the backstop for a page from before the settings change.
	var launcher editorintelligence.Launcher
	if profile, _, ok := editorintelligence.ProfileForPath(req.Path); ok {
		if s.lspProfileOff(profile) {
			c.JSON(http.StatusOK, gin.H{"available": false, "status": editorintelligence.StatusDisabled, "locations": []editorLSPLocation{}})
			return project.Project{}, editorintelligence.Request{}, false
		}
		launcher = s.lspProfileLauncher(profile)
	}
	if len(req.Content) > filesystem.MaxEditableBytes {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The document is too large for navigation."})
		return project.Project{}, editorintelligence.Request{}, false
	}
	return p, editorintelligence.Request{
		Client:      req.Client,
		ProjectName: p.Name,
		ProjectRoot: p.Path,
		Launcher:    launcher,
		Path:        req.Path,
		Content:     req.Content,
		Line:        req.Position.Line,
		Character:   req.Position.Character,
	}, true
}

// handleEditorLSPSource answers the text of one file a navigation pointed
// at outside the project: a dependency's downloaded sources, the standard
// library, one of a server's stubs. Which paths those are is not the
// caller's to say, it is the allowlist the launchers answer with
// (SourceRoot), and a path under none of the roots is refused here rather
// than read. The answer is text and never bytes, capped and binary checked
// like every editor buffer, and it carries no version: nothing on this
// route can be written back.
func (s *Server) handleEditorLSPSource(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	target := c.Query("path")
	launcher, root, ok := s.lspSourceRoot(p.Name, target)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": editorintelligence.ErrNoSourceRoot.Error()})
		return
	}
	content, err := launcher.ReadSource(c.Request.Context(), root, target)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"path": target, "content": content, "readOnly": true})
}

// handleEditorLSPStatus answers which of the project's language servers are
// indexing right now, feeding the statusbar indicator. Deliberately no
// Touch: the indicator's poll is not editor action and must not keep a
// server alive.
func (s *Server) handleEditorLSPStatus(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	c.JSON(http.StatusOK, gin.H{"profiles": s.intel.IndexStatus(p.Name)})
}

// handleEditorLSPReindex stops the project's running language servers the
// graceful way and warms them again over a fresh scan, the one manual
// reset when an index went stale. The project is the unit, the work runs
// in the background, and the indicator's poll shows the fresh indexing
// the way an editor open does.
func (s *Server) handleEditorLSPReindex(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	go func() {
		s.intel.CloseProject(p.Name)
		s.warmLSPServers(p)
	}()
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// warmLSPServers starts the language servers of the languages the project
// holds files for, so the indexing runs while the editor page is in front
// of the reader instead of under the first lookup. A profile's marker file
// at the root answers immediately; the bounded tree walk, staying out of
// the excluded folders, remains the fallback for the rest, its outcome
// cached per project so a page open does not pay the walk again. A
// language found nowhere starts nothing.
func (s *Server) warmLSPServers(p project.Project) {
	wanted := map[string]string{}
	wantedProfiles := map[string]bool{}
	for _, profile := range editorintelligence.Profiles() {
		if s.lspProfileOff(profile) {
			continue
		}
		wantedProfiles[profile.ID] = true
		for _, ext := range profile.Extensions() {
			wanted["."+ext] = profile.ID
		}
	}
	if len(wanted) == 0 {
		return
	}
	found := map[string]bool{}
	for _, profile := range editorintelligence.Profiles() {
		if !wantedProfiles[profile.ID] || profile.Marker == "" {
			continue
		}
		if info, err := os.Stat(filepath.Join(p.Path, profile.Marker)); err == nil && !info.IsDir() {
			found[profile.ID] = true
		}
	}
	if len(found) > 0 {
		s.intel.Warm(p.Name, p.Path, s.lspWarmModes(found))
	}
	if len(found) == len(wantedProfiles) {
		return
	}
	walked := s.lspWalkLanguages(p, wanted, wantedProfiles)
	if len(walked) > 0 {
		s.intel.Warm(p.Name, p.Path, s.lspWarmModes(walked))
	}
}

// lspWalkEntry is one cached walk outcome; wantedKey records which
// profiles the walk looked for, so a settings change recomputes.
type lspWalkEntry struct {
	wantedKey string
	found     map[string]bool
	at        time.Time
}

// lspWalkCacheTTL bounds how long a walk outcome stands in for the walk;
// which languages a project holds moves rarely, editor opens are frequent.
const lspWalkCacheTTL = 5 * time.Minute

// lspWalkLanguages answers which wanted languages the project's tree holds,
// from the per-project cache while it is fresh, else by the bounded walk.
func (s *Server) lspWalkLanguages(p project.Project, wanted map[string]string, wantedProfiles map[string]bool) map[string]bool {
	ids := make([]string, 0, len(wantedProfiles))
	for id := range wantedProfiles {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	wantedKey := strings.Join(ids, ",")
	s.lspWalksMu.Lock()
	if e, ok := s.lspWalks[p.Name]; ok && e.wantedKey == wantedKey && time.Since(e.at) < lspWalkCacheTTL {
		s.lspWalksMu.Unlock()
		return e.found
	}
	s.lspWalksMu.Unlock()

	walked := map[string]bool{}
	ex := s.exclusions()
	scanned := 0
	_ = filepath.WalkDir(p.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if scanned++; scanned > lspWarmScanCap || len(walked) == len(wantedProfiles) {
			return fs.SkipAll
		}
		if d.IsDir() {
			rel, relErr := filepath.Rel(p.Path, path)
			if relErr == nil && rel != "." && (ex.SkipDir(rel, d.Name()) || d.Name() == ".git") {
				return fs.SkipDir
			}
			return nil
		}
		if id, ok := wanted[strings.ToLower(filepath.Ext(d.Name()))]; ok {
			walked[id] = true
		}
		return nil
	})
	s.lspWalksMu.Lock()
	if s.lspWalks == nil {
		s.lspWalks = map[string]lspWalkEntry{}
	}
	s.lspWalks[p.Name] = lspWalkEntry{wantedKey: wantedKey, found: walked, at: time.Now()}
	s.lspWalksMu.Unlock()
	return walked
}

// lspWarmModes pairs the found profile ids with the way their server runs.
func (s *Server) lspWarmModes(ids map[string]bool) []editorintelligence.WarmMode {
	modes := make([]editorintelligence.WarmMode, 0, len(ids))
	for _, profile := range editorintelligence.Profiles() {
		if launcher := s.lspProfileLauncher(profile); ids[profile.ID] && launcher != nil {
			modes = append(modes, editorintelligence.WarmMode{ProfileID: profile.ID, Launcher: launcher})
		}
	}
	return modes
}

// lspWarmScanCap bounds the language detection walk; a project too large to
// finish the walk warms whatever the first stretch found.
const lspWarmScanCap = 50000

// handleEditorLSPDefinition answers where the symbol at the position is
// defined. An unavailable server answers with a status inside a 200, a
// malformed request is the only error case.
func (s *Server) handleEditorLSPDefinition(c *gin.Context) {
	s.handleEditorLSPNavigate(c, s.intel.Definition)
}

// handleEditorLSPReferences answers every location the symbol at the
// position is used at.
func (s *Server) handleEditorLSPReferences(c *gin.Context) {
	s.handleEditorLSPNavigate(c, s.intel.References)
}

func (s *Server) handleEditorLSPNavigate(c *gin.Context, run func(context.Context, editorintelligence.Request) (editorintelligence.Result, error)) {
	p, req, ok := s.editorLSPRequest(c)
	if !ok {
		return
	}
	res, err := run(c.Request.Context(), req)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
		return
	}
	s.answerLSP(c, p, req, res)
}

func (s *Server) answerLSP(c *gin.Context, p project.Project, req editorintelligence.Request, res editorintelligence.Result) {
	c.JSON(http.StatusOK, gin.H{
		"available":   res.Available,
		"status":      res.Status,
		"locations":   lspPreviews(p.Path, req.Path, req.Content, res.Locations),
		"outside":     res.Outside,
		"truncated":   res.Truncated,
		"declaration": res.Declaration,
	})
}

// lspPreviews fills each location with its target line, files read once.
// The asked document's text comes from the request, it may be unsaved;
// every other file is read from disk, and one that cannot be read (binary,
// too large, gone) leaves the preview empty rather than failing the answer.
// A location outside the project is read at its own absolute path, which
// the service only ever hands out for a file under an allowed source root;
// a tree that lives in an image and not on this host answers nothing here
// and shows its path alone, no container is started for a preview line.
func lspPreviews(root, activePath, activeContent string, locs []editorintelligence.Location) []editorLSPLocation {
	lines := map[string][]string{activePath: strings.Split(activeContent, "\n")}
	out := make([]editorLSPLocation, 0, len(locs))
	for _, loc := range locs {
		content, ok := lines[loc.Path]
		if !ok {
			readRoot, rel := root, loc.Path
			if loc.External {
				readRoot, rel = filepath.Dir(loc.Path), filepath.Base(loc.Path)
			}
			text, _, err := filesystem.ReadFileText(readRoot, rel)
			if err != nil {
				content = nil
			} else {
				content = strings.Split(text, "\n")
			}
			lines[loc.Path] = content
		}
		preview := ""
		if loc.Line >= 1 && loc.Line <= len(content) {
			// The window sits around the usage's own column, the same rule
			// the search snippets cut by, so a usage past the cap on a long
			// line still shows its symbol.
			line := content[loc.Line-1]
			preview = filesystem.SnippetAround(line, utf16ByteIndex(line, loc.Character))
		}
		out = append(out, editorLSPLocation{Location: loc, Preview: preview})
	}
	return out
}

// utf16ByteIndex maps a 0-based UTF-16 column, the coordinate the LSP
// answers carry, onto its byte offset in the line.
func utf16ByteIndex(s string, col int) int {
	units := 0
	for i, r := range s {
		if units >= col {
			return i
		}
		units++
		if r > 0xFFFF {
			units++
		}
	}
	return len(s)
}

// handleEditorLSPClose releases the document when a tab closes; the idle
// timeout would catch it anyway, this just frees it early.
func (s *Server) handleEditorLSPClose(c *gin.Context) {
	p, ok := s.editorProject(c)
	if !ok {
		return
	}
	var req editorLSPCloseRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body."})
		return
	}
	if !s.validLSPTarget(c, p, req.Client, req.Path) {
		return
	}
	s.intel.CloseDocument(req.Client, p.Name, req.Path)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

// closeProjectLSP takes a deleted project's language servers and their
// per-project caches down, the one teardown both delete paths share: the
// close is the graceful protocol, the cache removal retries in the
// background past a container still draining its exit.
func (s *Server) closeProjectLSP(project string) {
	s.intel.CloseProject(project)
	cacheRoot, host := s.lspCacheRoot(), s.lspDockerHost()
	go editorintelligence.RemoveProjectCaches(project, cacheRoot, host)
}
