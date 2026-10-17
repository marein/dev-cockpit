package web

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// A guest opens the projects of a link: the owner's own pages and reads of
// them, under the link's address. The routes stand outside the browser group
// on purpose: its CSRF check mints a token into the session, and a guest gets
// no session at all. A guest's POST carries a CSRF token of its own, derived
// from the link's token.

const guestKey = "shareGuest"

// guest is the share a guest's request came in through, the project of it the
// request is bound to, if any, and what the page may offer there.
type guest struct {
	share   share.Share
	base    string
	csrf    string
	project project.Project
	access  render.Access
	live    []project.Project
	read    bool
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

// guestCSRF is the CSRF token of the pages a link opens. It is derived from
// the link's own token, which only the URL carries: the server stores no
// secret for it, and the cookie key is no secret on a default install.
func guestCSRF(token string) string {
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte("dev-cockpit share csrf"))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// openShare opens the share a guest's request names. A request naming a
// project is bound to it, one naming a terminal is bound by guestTerminal,
// and any other has to find at least one project of the link standing. A
// token that opens nothing, unknown, expired, revoked or of projects that are
// gone, answers the one 404 of any address that does not exist, and so does a
// request naming a project that is not on the link.
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
	case len(s.guestProjects(g)) == 0:
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
// project of the link. A terminal of any other project answers like one that
// does not exist.
func (s *Server) guestTerminal(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		g, _ := guestOf(c)
		ref, ok := s.terminalSessions()[c.Param("id")]
		if !ok || ref.Kind != kind || g == nil {
			s.handleNotFound(c)
			return
		}
		p, ok := s.liveShareProject(g.share, s.projects.ProjectNameFor(ref.CWD))
		if !ok {
			s.handleNotFound(c)
			return
		}
		g.bind(p)
		c.Next()
	}
}

// guestPage is the page of a guest's request: the owner's page shell with
// nothing of the owner's in it, no session read and none written, and the
// terminals of the link's projects as its terminal list.
func (s *Server) guestPage(c *gin.Context, g *guest, title, activeTab string) render.Page {
	return render.Page{
		Title:      title,
		ActiveTab:  activeTab,
		CSRFToken:  g.csrf,
		MultiCoder: s.multiCoder(),
		QuickNav:   s.guestQuickNav(g, c.Param("id")),
		Working:    s.activity.Working(),
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
	group  *gin.RouterGroup
	prefix string
}

// read adds a route that changes nothing. A path no link may ever reach is
// refused here, so no grant added later can open it.
func (g guestRoutes) read(method, path string, handlers ...gin.HandlerFunc) {
	if neverForGuest(g.prefix + path) {
		panic("share: " + g.prefix + path + " is never a guest's")
	}
	g.group.Handle(method, path, handlers...)
}

// neverTrees are the owner's addresses no link reaches, each with everything
// below it, whatever the link grants: the settings, the assistants, a
// project's deletion, tags and new worktrees, the tab order and groups, the
// terminal theme, the notifications and the questions git asks.
var neverTrees = []string{
	"/settings", "/assistants", "/assistant", "/mention", "/projects/new", "/projects/delete",
	"/splits", "/terminal-theme", "/notifications", "/git", "/push", "/update", "/plugins",
	"/docker/actions", "/docker/link-rules", "/docker/links", "/coders/new", "/coders/delete",
	"/terminal-tabs/order", "/terminal-tabs/group", "/terminal-tabs/ungroup",
	"/projects/:name/tags", "/projects/:name/branches", "/projects/:name/fetch",
	"/projects/:name/editor/lsp/source",
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

// registerShareRoutes adds the guest's routes. Only reads stand here: a link
// grants reading, and what else it grants is offered by no route yet. The
// project switcher's rows are a guest face of the owner's, the link's
// projects alone.
func (s *Server) registerShareRoutes(r *gin.Engine) {
	g := guestRoutes{group: r.Group(sharePathPrefix+":token", s.openShare)}
	g.read(http.MethodGet, "", s.handleShareEntry)
	g.read(http.MethodGet, "/events", s.handleShareEvents)
	g.read(http.MethodGet, "/time", s.handleServerTime)
	g.read(http.MethodGet, "/ctx/:area", s.handleShareCtx)
	g.read(http.MethodGet, "/terminals", s.handleShareTerminals)
	g.read(http.MethodGet, "/terminal-tabs", s.handleShareTerminalTabs)
	g.read(http.MethodGet, "/terminal-link", s.handleTerminalLink)

	g.read(http.MethodGet, "/projects/:name/editor", s.handleShareEditor)
	g.read(http.MethodGet, "/projects/:name/docker/logs", s.handleDockerLogsPage)
	g.read(http.MethodGet, "/projects/:name/docker/logs/output", s.handleDockerLogsOutput)
	lsp := "/projects/:name/editor/lsp"
	g.read(http.MethodPost, lsp+"/definition", s.handleEditorLSPDefinition)
	g.read(http.MethodPost, lsp+"/references", s.handleEditorLSPReferences)
	g.read(http.MethodPost, lsp+"/class-name", s.handleEditorLSPClassName)
	g.read(http.MethodPost, lsp+"/close", s.handleEditorLSPClose)
	g.read(http.MethodGet, lsp+"/status", s.handleEditorLSPStatus)

	const editorPath = "/projects/:name/editor"
	editor := guestRoutes{group: g.group.Group(editorPath, s.invalidateQuickOpenAfterWrite), prefix: editorPath}
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
	editor.read(http.MethodGet, "/comments", s.handleEditorComments)
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
	g.read(http.MethodGet, "/coders/:id/stream", coder, s.handleShareStream)
	g.read(http.MethodGet, "/coders/:id/copy", coder, s.handleTerminalCopy)
	g.read(http.MethodGet, "/coders/:id/conversation", coder, s.handleCoderConversation)
	g.read(http.MethodPost, "/coders/:id/file-links", coder, s.handleFileLinks)
	shell := s.guestTerminal("shell")
	g.read(http.MethodGet, "/shells/:id", shell, s.handleShellAttach)
	g.read(http.MethodGet, "/shells/:id/name", shell, s.handleShellName)
	g.read(http.MethodGet, "/shells/:id/stream", shell, s.handleShareStream)
	g.read(http.MethodGet, "/shells/:id/copy", shell, s.handleTerminalCopy)
	g.read(http.MethodPost, "/shells/:id/file-links", shell, s.handleFileLinks)
}

// handleShareEntry answers the link itself with the editor of its first
// project that stands. Like the area entries it is a See Other nobody may
// cache.
func (s *Server) handleShareEntry(c *gin.Context) {
	g, _ := guestOf(c)
	c.Redirect(http.StatusSeeOther, guestEditorURL(g, s.guestProjects(g)[0].Name))
}
