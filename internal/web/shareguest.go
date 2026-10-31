package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// A guest opens the projects and the assistants of a link: the owner's own
// pages and reads of them, or a guest face of one, under the link's address.
// The routes stand outside the browser group on purpose: its CSRF check mints
// a token into the session, and a guest gets no session at all. A guest's
// POST carries a CSRF token of its own, derived from the link's token.

const guestKey = "shareGuest"

// guest is the share a guest's request came in through, the project or the
// assistant of it the request is bound to, if any, and what the page may offer
// there. through is the assistant of the link a coder of a project not on it
// is shown through.
type guest struct {
	share      share.Share
	base       string
	csrf       string
	project    project.Project
	assistant  assistant.Instance
	through    string
	access     render.Access
	live       []project.Project
	read       bool
	coders     map[string]string
	readCoders bool
	tabs       []render.TerminalTab
	readTabs   bool
}

func guestOf(c *gin.Context) (*guest, bool) {
	value, ok := c.Get(guestKey)
	if !ok {
		return nil, false
	}
	g, ok := value.(*guest)
	return g, ok
}

// accessOf answers who a request is for, the owner unless a link opened it.
func accessOf(c *gin.Context) render.Access {
	if g, ok := guestOf(c); ok {
		return g.access
	}
	return render.Access{}
}

// bind ties the guest's request to one project of its link.
func (g *guest) bind(p project.Project) {
	g.project = p
	g.access = render.GuestAccess(g.base, g.share, p.Name)
}

// guestProjects answers the projects of the guest's link that stand right
// now, read once per request.
func (s *Server) guestProjects(g *guest) []project.Project {
	if !g.read {
		g.live, g.read = s.liveShareProjects(g.share), true
	}
	return g.live
}

// guestCoders answers the coders the guest's link opens through its
// assistants, read once per request, see shareCoders.
func (s *Server) guestCoders(g *guest) map[string]string {
	if !g.readCoders {
		g.coders, g.readCoders = s.shareCoders(s.shareHelpers(g.share)), true
	}
	return g.coders
}

// guestTabs answers the terminals the guest's link shows, read once per
// request, see linkTabs.
func (s *Server) guestTabs(g *guest) []render.TerminalTab {
	if !g.readTabs {
		g.tabs, g.readTabs = s.linkTabs(guestProjectNames(s.guestProjects(g)), s.guestCoders(g)), true
	}
	return g.tabs
}

// guestShownCoders answers the ids of the coders the guest's link shows.
func (s *Server) guestShownCoders(g *guest) []string {
	var ids []string
	for _, t := range s.guestTabs(g) {
		if t.Kind == "coder" {
			ids = append(ids, t.ID)
		}
	}
	return ids
}

// guestCSRF is the CSRF token of the pages a link opens. It is derived from
// the link's own token, which only the URL carries: the server stores no
// secret for it, and the cookie key is no secret on a default install.
func guestCSRF(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("dev-cockpit share csrf"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// openShare opens the share a guest's request names. A request naming a
// project is bound to it, one naming a terminal or an assistant is bound by
// guestTerminal or guestAssistant, and any other has to find at least one
// project or assistant of the link standing. A token that opens nothing,
// unknown, expired, revoked or of resources that are gone, answers the one 404
// of any address that does not exist, and so does a request naming a project
// that is not on the link.
func (s *Server) openShare(c *gin.Context) {
	token := c.Param("token")
	sh, err := s.shares.Lookup(token)
	if err != nil {
		s.handleNotFound(c)
		return
	}
	g := &guest{share: sh, base: sharePathPrefix + token, csrf: guestCSRF(token)}
	g.access = render.GuestAccess(g.base, sh, "")
	switch {
	case c.Param("name") != "":
		p, ok := s.liveShareProject(sh, c.Param("name"))
		if !ok {
			s.handleNotFound(c)
			return
		}
		g.bind(p)
	case c.Param("id") != "":
	case len(s.guestProjects(g)) == 0 && len(s.shareHelpers(sh)) == 0:
		s.handleNotFound(c)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Robots-Tag", "noindex, nofollow")
	if isUnsafeMethod(c.Request.Method) {
		got := c.GetHeader("X-CSRF-Token")
		if got == "" {
			got = c.PostForm(csrfFieldName)
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(g.csrf)) != 1 {
			s.renderError(c, http.StatusForbidden, "Forbidden", "The page is no longer valid. Reload it and try again.")
			return
		}
	}
	c.Set(guestKey, g)
	c.Next()
}

// guestTerminal binds a terminal route to a running coder or shell of a
// project of the link, or to a running coder an assistant of the link steers
// or once steered, whatever project it works in. Then the request belongs to
// no project of the link: the page links none and reads nothing of it. Any
// other terminal answers like one that does not exist.
func (s *Server) guestTerminal(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		g, _ := guestOf(c)
		id := c.Param("id")
		ref, ok := s.terminalSessions()[id]
		if !ok || ref.Kind != kind || g == nil {
			s.handleNotFound(c)
			return
		}
		if p, ok := s.liveShareProject(g.share, s.projects.ProjectNameFor(ref.CWD)); ok {
			g.bind(p)
			c.Next()
			return
		}
		through, ok := s.guestCoders(g)[id]
		if !ok || kind != "coder" {
			s.handleNotFound(c)
			return
		}
		g.through = through
		c.Next()
	}
}

// guestStands answers whether the link still opens what the guest's request
// is bound to: the project, or the coder through its assistant.
func (s *Server) guestStands(g *guest, sh share.Share, id string) bool {
	if g.through == "" {
		_, ok := s.liveShareProject(sh, g.project.Name)
		return ok
	}
	if !slices.Contains(s.shareHelpers(sh), g.through) {
		return false
	}
	_, ok := s.shareCoders([]string{g.through})[id]
	return ok
}

// guestPage is the page of a guest's request: the owner's page shell with
// nothing of the owner's in it, no session read and none written, the
// terminals the link shows as its terminal list, and the owner's status line
// with the host's reading and the links of the link's projects. The terminals
// wear the steered mark and the working ring the owner's icons wear, which
// name no assistant; the steer dialog's prefill stays the owner's.
func (s *Server) guestPage(c *gin.Context, g *guest, title, activeTab string) render.Page {
	steered, _ := s.watcher.Marks()
	return render.Page{
		Title:      title,
		ActiveTab:  activeTab,
		CSRFToken:  g.csrf,
		MultiCoder: s.multiCoder(),
		QuickNav:   s.guestQuickNav(g, c.Param("id")),
		Steered:    steered,
		Working:    s.activity.Working(),
		Host:       s.host.Stats(),
		Links:      s.guestStatusLinks(g, g.project.Name),
		Access:     g.access,
	}
}

// guestEditorURL is where a guest's link to a project's editor leads, under
// the link's address.
func guestEditorURL(g *guest, name string) string {
	return g.base + "/projects/" + url.PathEscape(name) + "/editor"
}

// guestRoutes registers what a guest reaches. Every route mirrors one of the
// owner's under the link's address and runs the owner's handler or a guest
// face of it. prefix is the owner's path of the group.
type guestRoutes struct {
	s      *Server
	group  *gin.RouterGroup
	prefix string
}

// read adds a route that changes nothing. A path no link may ever reach is
// refused here, so no grant added later can open it.
func (g guestRoutes) read(method, path string, handlers ...gin.HandlerFunc) {
	if !guestMayRead(method, g.prefix+path) {
		panic("share: " + method + " " + g.prefix + path + " is never a guest's")
	}
	g.group.Handle(method, path, handlers...)
}

// write adds a route that changes the project its request is bound to, which
// only a link granting p there reaches, see guestGranted. A path no link may
// ever reach is refused here like a read's.
func (g guestRoutes) write(p share.Permission, method, path string, handlers ...gin.HandlerFunc) {
	if neverForGuest(g.prefix + path) {
		panic("share: " + method + " " + g.prefix + path + " is never a guest's")
	}
	g.group.Handle(method, path, append([]gin.HandlerFunc{g.s.guestGranted(p)}, handlers...)...)
}

// guestGranted lets a guest's write through when its link grants p in the
// project the request is bound to. openShare read the link afresh for this
// request, so a permission taken away, a project gone from the link and a
// link that ended refuse the very next write. A link is a browser's, a local
// process never writes through one.
func (s *Server) guestGranted(p share.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		g, ok := guestOf(c)
		switch {
		case !ok || s.localCall(c):
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "A link writes only from the browser, never over the local socket."})
		case !g.access.Can(p):
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "The link does not allow that."})
		default:
			c.Next()
		}
	}
}

// neverTrees are the owner's addresses no link reaches, each with everything
// below it, whatever the link grants: the settings, the assistants, a
// project's deletion, tags and new worktrees, the tab order and groups, the
// terminal theme, the notifications and the questions git asks.
var neverTrees = []string{
	"/settings", "/assistants", "/assistant", "/mention", "/projects/new", "/projects/delete",
	"/splits", "/terminal-theme", "/notifications", "/git", "/push", "/update", "/plugins",
	"/docker/actions", "/docker/link-rules", "/coders/new", "/coders/delete",
	"/terminal-tabs/order", "/terminal-tabs/group", "/terminal-tabs/ungroup",
	"/projects/:name/tags", "/projects/:name/branches", "/projects/:name/fetch",
}

// guestAssistantReads are the reads of the assistants' tree a link reaches,
// each a GET: the area's entry, the conversation, its stream, the files the
// messages link and the lists of its jobs and triggers. Everything else of
// it, the memory, the trigger forms, the drafts, the uploads, the model picks
// and the speech, stays the owner's like the rest of the trees above.
var guestAssistantReads = []string{
	"/assistants", "/assistants/:id", "/assistants/:id/stream", "/assistants/:id/media/*path",
	assistantJobsPath, assistantTriggersPath,
}

// guestMayRead answers whether a guest route may mirror an owner's route.
func guestMayRead(method, path string) bool {
	return !neverForGuest(path) || (method == http.MethodGet && slices.Contains(guestAssistantReads, path))
}

// neverForGuest answers whether an owner's route is one no link may ever
// reach. Besides the trees above that is deleting a coder or a shell,
// resizing a pane, the projects page and the editor's entry, which answers
// from the owner's memory.
func neverForGuest(path string) bool {
	for _, tree := range neverTrees {
		if path == tree || strings.HasPrefix(path, tree+"/") {
			return true
		}
	}
	terminal := strings.HasPrefix(path, "/coders/") || strings.HasPrefix(path, "/shells/")
	if terminal && (strings.HasSuffix(path, "/delete") || strings.HasSuffix(path, "/resize")) {
		return true
	}
	return path == "/projects" || path == "/editor"
}

// registerShareRoutes adds the guest's routes. Every link reads, and a write
// stands under the permission a link grants it with: the line comments. The
// project switcher's rows and the status line's links are a guest face of the
// owner's, the link's projects alone, and an assistant's pages are a guest
// face of the owner's, its conversation, its jobs and its triggers alone.
func (s *Server) registerShareRoutes(r *gin.Engine) {
	g := guestRoutes{s: s, group: r.Group(sharePathPrefix+":token", s.openShare)}
	g.read(http.MethodGet, "", s.handleShareEntry)
	g.read(http.MethodGet, "/events", s.handleShareEvents)
	g.read(http.MethodGet, "/time", s.handleServerTime)
	g.read(http.MethodGet, "/ctx/:area", s.handleShareCtx)
	g.read(http.MethodGet, "/terminals", s.handleShareTerminals)
	g.read(http.MethodGet, "/terminal-tabs", s.handleShareTerminalTabs)
	g.read(http.MethodGet, "/terminal-link", s.handleTerminalLink)
	g.read(http.MethodGet, "/docker/links", s.handleStatusLinks)

	g.read(http.MethodGet, "/projects/:name/editor", s.handleShareEditor)
	g.read(http.MethodGet, "/projects/:name/docker/logs", s.handleDockerLogsPage)
	g.read(http.MethodGet, "/projects/:name/docker/logs/output", s.handleDockerLogsOutput)
	lsp := "/projects/:name/editor/lsp"
	g.read(http.MethodPost, lsp+"/definition", s.handleEditorLSPDefinition)
	g.read(http.MethodPost, lsp+"/references", s.handleEditorLSPReferences)
	g.read(http.MethodPost, lsp+"/class-name", s.handleEditorLSPClassName)
	g.read(http.MethodPost, lsp+"/structure", s.handleEditorLSPStructure)
	g.read(http.MethodPost, lsp+"/close", s.handleEditorLSPClose)
	g.read(http.MethodGet, lsp+"/status", s.handleEditorLSPStatus)
	g.read(http.MethodGet, lsp+"/types", s.handleEditorLSPTypes)
	g.read(http.MethodGet, lsp+"/source", s.handleEditorLSPSource)

	const editorPath = "/projects/:name/editor"
	editor := guestRoutes{s: s, group: g.group.Group(editorPath, s.invalidateQuickOpenAfterWrite), prefix: editorPath}
	editor.read(http.MethodGet, "/projects", s.handleShareEditorProjects)
	editor.read(http.MethodGet, "/list", s.handleEditorList)
	editor.read(http.MethodGet, "/file", s.handleEditorReadFile)
	editor.read(http.MethodGet, "/raw", s.handleEditorRaw)
	editor.read(http.MethodGet, "/archive", s.handleEditorArchive)
	editor.read(http.MethodGet, "/files", s.handleEditorFiles)
	editor.read(http.MethodGet, "/terminals", s.handleEditorTerminals)
	editor.read(http.MethodGet, "/docker", s.handleEditorDocker)
	editor.read(http.MethodGet, "/search", s.handleEditorSearch)
	editor.read(http.MethodGet, "/filters", s.handleEditorFilters)
	editor.read(http.MethodPost, "/preview", s.handleEditorPreview)
	editor.read(http.MethodPost, "/headings", editorPoll, s.handleEditorHeadings)
	editor.read(http.MethodGet, "/comments", s.handleEditorComments)
	editor.read(http.MethodGet, "/comments/text", s.handleEditorCommentsText)
	editor.write(share.Comments, http.MethodPost, "/comments", s.handleEditorCommentSave)
	editor.write(share.Comments, http.MethodPost, "/comments/delete", s.handleEditorCommentsDelete)
	editor.write(share.Comments, http.MethodPost, "/comments/move", s.handleEditorCommentsMove)
	editor.read(http.MethodGet, "/git/changes", s.handleEditorGitChanges)
	editor.read(http.MethodGet, "/git/blame", s.handleEditorGitBlame)
	editor.read(http.MethodGet, "/git/file", s.handleEditorGitFile)
	editor.read(http.MethodGet, "/git/log", s.handleEditorGitLog)
	editor.read(http.MethodGet, "/git/refs", s.handleEditorGitRefs)
	editor.read(http.MethodGet, "/git/compare", s.handleEditorGitCompare)
	editor.read(http.MethodGet, "/git/commit", s.handleEditorGitCommitInfo)
	editor.read(http.MethodPost, "/git/watch", editorPoll, s.handleEditorGitWatch)
	editor.read(http.MethodPost, "/watch", editorPoll, s.handleEditorWatch)

	coder := s.guestTerminal("coder")
	g.read(http.MethodGet, "/coders/:id", coder, s.handleCoderAttach)
	g.read(http.MethodGet, "/coders/:id/name", coder, s.handleCoderName)
	g.read(http.MethodGet, "/coders/:id/steered", coder, s.handleCoderSteeredMark)
	g.read(http.MethodGet, "/coders/:id/stream", coder, s.handleShareStream)
	g.read(http.MethodGet, "/coders/:id/copy", coder, s.handleTerminalCopy)
	g.read(http.MethodGet, "/coders/:id/conversation", coder, s.handleCoderConversation)
	g.read(http.MethodPost, "/coders/:id/file-links", coder, s.handleFileLinks)
	g.read(http.MethodGet, "/assistants", s.handleShareAssistantsEntry)
	g.read(http.MethodGet, assistantJobsPath, s.guestAssistant, s.handleShareAssistantJobs)
	g.read(http.MethodGet, assistantTriggersPath, s.guestAssistant, s.handleShareAssistantTriggers)
	g.read(http.MethodGet, "/assistants/:id", s.guestAssistant, s.handleShareAssistant)
	g.read(http.MethodGet, "/assistants/:id/stream", s.guestAssistant, s.handleShareAssistantStream)
	g.read(http.MethodGet, "/assistants/:id/media/*path", s.guestAssistant, s.handleShareAssistantMedia)

	shell := s.guestTerminal("shell")
	g.read(http.MethodGet, "/shells/:id", shell, s.handleShellAttach)
	g.read(http.MethodGet, "/shells/:id/name", shell, s.handleShellName)
	g.read(http.MethodGet, "/shells/:id/stream", shell, s.handleShareStream)
	g.read(http.MethodGet, "/shells/:id/copy", shell, s.handleTerminalCopy)
	g.read(http.MethodPost, "/shells/:id/file-links", shell, s.handleFileLinks)
}

// handleShareEntry answers the link itself with the editor of its first
// project that stands, or the conversation of its first assistant on a link
// of assistants alone. Like the area entries it is a See Other nobody may
// cache.
func (s *Server) handleShareEntry(c *gin.Context) {
	g, _ := guestOf(c)
	if projects := s.guestProjects(g); len(projects) > 0 {
		c.Redirect(http.StatusSeeOther, guestEditorURL(g, projects[0].Name))
		return
	}
	c.Redirect(http.StatusSeeOther, guestAssistantURL(g, s.shareHelpers(g.share)[0]))
}
