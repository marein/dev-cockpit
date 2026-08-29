package web

import (
	"bytes"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/pluginhost"
)

// coderPagePaths are a coder's scoped pages relative to their base. The
// canonical routes are registered by hand below, method by method; this list
// is what the two redirect families replay, so a page can never move without
// its old links moving with it.
var coderPagePaths = []string{
	"/instructions",
	"/agents", "/agents/new", "/agents/:id", "/agents/:id/edit", "/agents/:id/delete",
	"/skills", "/skills/new", "/skills/:id", "/skills/:id/edit", "/skills/:id/delete",
}

// registerRoutes attaches all HTTP routes to the Gin router.
func (s *Server) registerRoutes(r *gin.Engine) {
	r.NoRoute(s.handleNotFound)
	r.NoMethod(s.handleMethodNotAllowed)
	r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	s.registerStaticRoutes(r)

	// TODO(v2.0.0): drop the redirects from the pre-coder URLs. 308 keeps the
	// method, so stale forms and bookmarks replay against the new paths; both
	// old shapes map 1:1 (/sessions/X/... and /resumable/X/... -> /coders/X/...).
	legacyRedirect := func(c *gin.Context) {
		target := "/coders" + c.Param("rest")
		if q := c.Request.URL.RawQuery; q != "" {
			target += "?" + q
		}
		c.Redirect(http.StatusPermanentRedirect, target)
	}
	r.Any("/sessions/*rest", legacyRedirect)
	r.Any("/resumable/*rest", legacyRedirect)

	browser := r.Group("/", s.csrfMiddleware())
	browser.GET("/login", s.handleLoginGet)
	browser.POST("/login", s.handleLoginPost)

	auth := browser.Group("/", s.requireAuth)
	auth.GET("/", func(c *gin.Context) { c.Redirect(http.StatusSeeOther, "/projects") })
	auth.POST("/logout", s.handleLogout)
	auth.GET("/quicknav", s.handleQuickNav)
	auth.GET("/docs", s.handleDocs)
	auth.GET("/terminal-tabs", s.handleTerminalTabsFragment)
	auth.POST("/terminal-tabs/order", s.handleTerminalTabsOrder)
	auth.POST("/terminal-tabs/group", s.handleTerminalTabsGroup)
	auth.POST("/terminal-tabs/ungroup", s.handleTerminalTabsUngroup)
	auth.POST("/terminal-tabs/group/name", s.handleTerminalTabsGroupName)
	auth.GET("/splits/:id", s.handleSplitAttach)
	auth.POST("/terminal-theme", s.handleTerminalTheme)

	auth.GET("/coders/new", s.handleCoderNew)
	auth.POST("/coders/new", s.handleCoderCreate)

	// Canonical coder pages, one subtree per active coder:
	// /settings/coders/<coder>/{instructions,agents,skills}. They are the
	// settings of one coder, so they live under the settings and the sidebar
	// picks the coder like it picks any other settings section.
	for i := range s.coders {
		co := s.coders[i]
		home := s.coderBase(co) + "/instructions"
		base := auth.Group(s.coderBase(co))
		base.GET("", func(c *gin.Context) { c.Redirect(http.StatusSeeOther, home) })
		base.GET("/instructions", s.handleInstructionsEdit(co))
		base.POST("/instructions", s.handleInstructionsUpdate(co))
		base.GET("/agents", s.handleAgentsList(co))
		base.GET("/agents/new", s.handleAgentNew(co))
		base.POST("/agents", s.handleAgentCreate(co))
		base.GET("/agents/:id/edit", s.handleAgentEdit(co))
		base.POST("/agents/:id", s.handleAgentUpdate(co))
		base.POST("/agents/:id/delete", s.handleAgentDelete(co))
		base.GET("/skills", s.handleSkillsList(co))
		base.GET("/skills/new", s.handleSkillNew(co))
		base.POST("/skills", s.handleSkillCreate(co))
		base.GET("/skills/:id/edit", s.handleSkillEdit(co))
		base.POST("/skills/:id", s.handleSkillUpdate(co))
		base.POST("/skills/:id/delete", s.handleSkillDelete(co))
	}

	// TODO(v2.0.0): drop the pre-settings coder pages, canonical is
	// /settings/coders/<coder>/... . 308 keeps method and body, so a bookmark
	// and a form of a page loaded before the move replay against the canonical
	// path. Static segments win over the :id session routes below, and session
	// identifiers are UUID-shaped, so the two namespaces cannot collide.
	for i := range s.coders {
		co := s.coders[i]
		old := auth.Group("/coders/" + co.ID())
		old.Any("", s.redirectMovedCoderPath(co))
		for _, p := range coderPagePaths {
			old.Any(p, s.redirectMovedCoderPath(co))
		}
	}

	// TODO(v2.0.0): drop the legacy top-level coder pages, canonical is
	// /settings/coders/<coder>/... . 308 keeps the method, so stale forms and
	// bookmarks replay against the canonical paths.
	for _, p := range coderPagePaths {
		auth.Any(p, s.redirectLegacyCoderPath)
	}

	auth.GET("/coders/:id", s.handleCoderAttach)
	auth.GET("/coders/:id/name", s.handleCoderName)
	auth.GET("/coders/:id/activity", s.handleCoderActivity)
	auth.GET("/coders/:id/steered", s.handleCoderSteeredMark)
	auth.POST("/coders/:id/stop", s.handleCoderStop)
	auth.GET("/coders/:id/files", s.handleCoderFiles)
	auth.POST("/coders/:id/files", s.handleCoderFileUpload)
	auth.GET("/coders/:id/files/download", s.handleCoderFileDownload)
	auth.POST("/coders/:id/files/delete", s.handleCoderFileDelete)
	auth.POST("/coders/:id/input", s.handleCoderInput)
	auth.POST("/coders/:id/resize", s.handleCoderResize)
	auth.GET("/coders/:id/stream", s.handleCoderStream)
	auth.POST("/coders/:id/resume", s.handleCoderResume)
	auth.POST("/coders/:id/delete", s.handleCoderDelete)

	// The assistant has no pages of its own: every entry opens the overlay on
	// whatever page is open, and its interior comes from /assistant/panel. The
	// GET routes below serve the overlay's fragments; the bare paths redirect,
	// so an old notification link or bookmark still opens the overlay, told
	// what to show through the query (the browser keeps a #message fragment
	// across the redirect). The static segments win over :id, and conversation
	// ids are UUID shaped, so they cannot collide.
	auth.GET("/assistant", func(c *gin.Context) { c.Redirect(http.StatusSeeOther, "/projects?assistant=open") })
	auth.GET("/assistant/panel", s.handleAssistantPanel)
	auth.GET("/assistant/history", s.handleAssistantHistory)
	auth.GET("/assistant/memory", s.handleAssistantMemory)
	auth.POST("/assistant/memory", s.handleAssistantMemorySave)
	// The steered jobs belong to the assistant, not to one conversation: a job
	// outlives the conversation it was started from and reports into whichever
	// one is live when it has something to say. So they sit on a path of their
	// own, which is also the one `dev-cockpit assistant coder-steer` posts to.
	auth.GET("/assistant/jobs", s.handleAssistantJobs)
	auth.POST("/assistant/jobs", s.handleAssistantJobsAction)
	// The two conversation reads answer the assistant's own `conversation-list`
	// and `conversation-show` commands as JSON. The overlay renders its history
	// from fragments, not from these.
	auth.GET("/assistant/conversations", s.handleAssistantConversations)
	auth.GET("/assistant/conversations/:id", s.handleAssistantConversationRead)
	auth.GET("/assistant/:id", func(c *gin.Context) {
		c.Redirect(http.StatusSeeOther, "/projects?assistant="+c.Param("id"))
	})
	auth.POST("/assistant/:id", s.handleAssistantAction)
	auth.GET("/assistant/:id/stream", s.handleAssistantStream)
	auth.GET("/assistant/:id/messages/:messageId", s.handleAssistantMessage)
	auth.GET("/assistant/:id/draft", s.handleAssistantDraft)
	auth.POST("/assistant/:id/user-upload", s.handleAssistantUpload)
	// The two voice routes: a recorded clip in and its transcript out, and
	// one answer spoken, synthesized per request.
	auth.POST("/assistant/:id/stt", s.handleAssistantSTT)
	auth.GET("/assistant/:id/messages/:messageId/audio", s.handleAssistantMessageAudio)
	auth.GET("/assistant/:id/media/*path", s.handleAssistantMedia)

	auth.GET("/shells/new", s.handleShellNew)
	auth.POST("/shells/new", s.handleShellCreate)
	auth.GET("/shells/:id", s.handleShellAttach)
	auth.GET("/shells/:id/name", s.handleShellName)
	auth.POST("/shells/:id/delete", s.handleShellDelete)
	auth.POST("/shells/:id/rename", s.handleShellRename)
	auth.POST("/shells/:id/input", s.handleShellInput)
	auth.POST("/shells/:id/resize", s.handleShellResize)
	auth.GET("/shells/:id/stream", s.handleShellStream)

	auth.GET("/settings", s.handleSettings)
	// The editor settings sit behind a tab, like a coder's sections, so the page
	// can grow more of them; the bare path leads to the one there is.
	auth.GET("/settings/editor", s.handleSettingsEditor)
	auth.GET("/settings/editor/git", s.handleSettingsEditorGit)
	auth.POST("/settings/editor/git", s.handleSettingsEditorGitSave)
	auth.GET("/settings/editor/search", s.handleSettingsEditorSearch)
	auth.POST("/settings/editor/search", s.handleSettingsEditorSearchSave)
	auth.GET("/settings/editor/lsp", s.handleSettingsEditorLSP)
	auth.POST("/settings/editor/lsp", s.handleSettingsEditorLSPSave)
	auth.GET("/settings/notifications", s.handleSettingsNotifications)
	auth.POST("/settings/notifications", s.handleSettingsNotificationsSave)
	// The assistant's settings sit behind a tab like the editor's, so the page
	// can grow more of them; the bare path leads to the one there is.
	auth.GET("/settings/assistant", s.handleSettingsAssistant)
	auth.GET("/settings/assistant/voice", s.handleSettingsVoice)
	auth.POST("/settings/assistant/voice", s.handleSettingsVoiceSave)
	auth.GET("/settings/general", s.handleSettingsGeneral)
	auth.POST("/settings/general", s.handleSettingsGeneralSave)
	// Docker is a section of its own: the daemon and the compose commands.
	auth.GET("/settings/docker", s.handleSettingsDocker)
	auth.POST("/settings/docker", s.handleSettingsDockerSave)
	auth.GET("/settings/backup", s.handleSettingsBackup)
	auth.POST("/settings/backup", s.handleSettingsBackupSave)
	auth.GET("/settings/backup/new", s.handleSettingsBackupNew)
	auth.POST("/settings/backup/new", s.handleSettingsBackupCreate)
	auth.GET("/settings/backup/list", s.handleSettingsBackupList)
	auth.GET("/settings/backup/download", s.handleSettingsBackupDownload)
	auth.GET("/settings/backup/merge", s.handleSettingsBackupMerge)
	auth.POST("/settings/backup/merge", s.handleSettingsBackupMergeSave)

	// Every plugin serves its whole subtree behind one wildcard route: the
	// assets and the generated starter out of the manifest, everything else
	// through its own handler. The routes sit in the auth group, so a
	// plugin page is signed in and CSRF protected like any app route.
	for _, p := range s.plugins {
		if !pluginhost.ServesHTTP(p) {
			continue
		}
		auth.Any("/plugins/"+p.ID()+"/*path", s.servePlugin(p))
	}

	auth.GET("/notifications", s.handleNotificationsList)
	auth.POST("/notifications/read", s.handleNotificationsRead)

	// The container actions the docker chips and the editor's docker sheet
	// offer. They address the daemon's container id, which no project owns,
	// so they sit at the top level like the other JS routes; only the compose
	// actions are project scoped and live under the project below.
	// The configured compose commands belong to the install, not to a
	// container, so putting the list back sits next to them.
	auth.POST("/docker/actions/restore", s.handleDockerActionsRestore)
	auth.POST("/docker/link-rules/restore", s.handleDockerLinkRulesRestore)
	auth.POST("/docker/:id/start", s.handleDockerStart)
	auth.POST("/docker/:id/stop", s.handleDockerStop)
	auth.POST("/docker/:id/restart", s.handleDockerRestart)
	auth.POST("/docker/:id/shell", s.handleDockerShell)
	auth.POST("/docker/:id/logs-shell", s.handleDockerLogsShell)

	// /events is the app-wide server to client stream.
	auth.GET("/events", s.handleEventStream)

	// The questions ssh and git ask during a running action are app level like
	// the stream that announces them: any signed-in page shows and answers
	// them, because the page that started the action may be gone or out of
	// reach while the action still waits.
	auth.GET("/git/prompt", s.handleGitPromptList)
	auth.POST("/git/prompt", s.handleGitPromptAnswer)

	// The git proxy behind `dev-cockpit git`: one git command line, run in the
	// working copy of the project the caller stands in, so the askpass bridge
	// can ask the browser. It carries no project in its path because the
	// caller cannot name one: it sends the directory it is in and this server
	// resolves it, out of the projects root it alone is authoritative for.
	auth.POST("/git", s.handleGitProxy)

	auth.POST("/push/subscribe", s.handlePushSubscribe)
	auth.POST("/push/unsubscribe", s.handlePushUnsubscribe)
	auth.POST("/push/test", s.handlePushTest)

	auth.GET("/update/check", s.handleUpdateCheck)
	auth.POST("/update/apply", s.handleUpdateApply)

	auth.GET("/projects", s.handleProjectsList)
	auth.GET("/projects/new", s.handleProjectNew)
	auth.POST("/projects", s.handleProjectCreate)
	auth.POST("/projects/delete", s.handleProjectDelete)
	auth.GET("/projects/:name/editor", s.handleProjectEditor)
	// The navigation routes stay off the editor group below on purpose: its
	// middleware drops the quick open index after every POST (a navigation
	// request writes nothing) and counts editor action for the language
	// server lifetime, which the status poll must not.
	auth.POST("/projects/:name/editor/lsp/definition", s.handleEditorLSPDefinition)
	auth.POST("/projects/:name/editor/lsp/references", s.handleEditorLSPReferences)
	auth.POST("/projects/:name/editor/lsp/close", s.handleEditorLSPClose)
	auth.GET("/projects/:name/editor/lsp/status", s.handleEditorLSPStatus)
	// The file behind a target outside the project, read out of the source
	// directories the language servers themselves work from and nowhere
	// else. It is a read like the lookups above and belongs beside them.
	auth.GET("/projects/:name/editor/lsp/source", s.handleEditorLSPSource)
	auth.POST("/projects/:name/editor/lsp/reindex", s.handleEditorLSPReindex)
	// The switcher's rows stay off the group for the same reason: an open
	// editor pulls them whenever a project changes anywhere in the app, which
	// is the app moving and not somebody working in this project.
	auth.GET("/projects/:name/editor/projects", s.handleEditorProjects)
	auth.POST("/projects/:name/docker/compose", s.handleDockerCompose)
	auth.POST("/projects/:name/docker/logs", s.handleDockerComposeLogs)
	// A compose run outlives the request that started it, so its output is a
	// place of its own: the page reads the file the detached run writes into,
	// and the cancel goes at the hold process holding it.
	auth.GET("/projects/:name/docker/runs/:id", s.handleDockerRun)
	auth.GET("/projects/:name/docker/runs/:id/output", s.handleDockerRunOutput)
	auth.POST("/projects/:name/docker/runs/:id/stop", s.handleDockerRunStop)
	// Grouped so that every write below /editor drops the project's quick open
	// index on its way out. Putting it here rather than in each handler means a
	// route added later cannot forget to invalidate.
	editor := auth.Group("/projects/:name/editor", s.invalidateQuickOpenAfterWrite)
	editor.GET("/list", s.handleEditorList)
	editor.GET("/file", s.handleEditorReadFile)
	editor.GET("/raw", s.handleEditorRaw)
	editor.GET("/archive", s.handleEditorArchive)
	editor.POST("/file", s.handleEditorSaveFile)
	editor.POST("/create", s.handleEditorCreateFile)
	editor.POST("/mkdir", s.handleEditorCreateDir)
	editor.POST("/delete", s.handleEditorDeletePath)
	editor.POST("/rename", s.handleEditorRename)
	editor.POST("/move", s.handleEditorMove)
	editor.POST("/copy", s.handleEditorCopy)
	editor.POST("/extract", s.handleEditorExtract)
	editor.GET("/files", s.handleEditorFiles)
	editor.GET("/terminals", s.handleEditorTerminals)
	editor.GET("/docker", s.handleEditorDocker)
	editor.GET("/search", s.handleEditorSearch)
	editor.POST("/upload", s.handleEditorUpload)
	editor.POST("/preview", s.handleEditorPreview)
	editor.GET("/comments", s.handleEditorComments)
	editor.POST("/comments", s.handleEditorCommentSave)
	editor.POST("/comments/delete", s.handleEditorCommentsDelete)
	editor.POST("/comments/move", s.handleEditorCommentsMove)
	editor.GET("/git/changes", s.handleEditorGitChanges)
	editor.GET("/git/blame", s.handleEditorGitBlame)
	editor.GET("/git/file", s.handleEditorGitFile)
	editor.GET("/git/log", s.handleEditorGitLog)
	editor.GET("/git/refs", s.handleEditorGitRefs)
	editor.GET("/git/commit", s.handleEditorGitCommitInfo)
	editor.POST("/git/commit", s.handleEditorGitCommit)
	editor.GET("/git/commit-draft", s.handleEditorGitCommitDraft)
	editor.POST("/git/commit-draft", s.handleEditorGitCommitDraftSave)
	editor.POST("/git/push", s.handleEditorGitPush)
	editor.POST("/git/fetch", s.handleEditorGitFetch)
	editor.POST("/git/pull", s.handleEditorGitPull)
	editor.POST("/git/checkout", s.handleEditorGitCheckout)
	editor.POST("/git/branch", s.handleEditorGitBranch)
	editor.POST("/git/tag", s.handleEditorGitTag)
	editor.POST("/git/tag/push", s.handleEditorGitTagPush)
	editor.POST("/git/tag/delete", s.handleEditorGitTagDelete)
	editor.POST("/git/revert", s.handleEditorGitRevert)
	editor.POST("/git/clone", s.handleEditorGitClone)
	editor.POST("/git/watch", s.handleEditorGitWatch)
}

func (s *Server) registerStaticRoutes(r *gin.Engine) {
	for assetURL, asset := range s.assets.byURL {
		// Plugin assets live in the manifest too, but their subtree is one
		// wildcard route in the auth group; registering them here would
		// collide with it and serve them without the session.
		if strings.HasPrefix(assetURL, "/plugins/") {
			continue
		}
		assetURL, asset := assetURL, asset
		r.GET(assetURL, func(c *gin.Context) { serveStaticAsset(c, asset) })
		r.HEAD(assetURL, func(c *gin.Context) { serveStaticAsset(c, asset) })
	}
}

// servePlugin serves one plugin's subtree: the hashed and raw asset URLs and
// the generated starter out of the asset manifest with the same cache
// headers as every other asset, everything else through the added routes.
// The manifest is asked first, so a plugin's own handler can never shadow
// its assets or the starter.
func (s *Server) servePlugin(p *pluginhost.Serve) gin.HandlerFunc {
	base := "/plugins/" + p.ID()
	var routes http.Handler
	if added := p.Handler(); added != nil {
		routes = http.StripPrefix(base, added)
	}
	return func(c *gin.Context) {
		if asset, ok := s.assets.byURL[c.Request.URL.Path]; ok {
			serveStaticAsset(c, asset)
			return
		}
		if strings.HasPrefix(c.Request.URL.Path, base+"/assets/") || routes == nil {
			c.Status(http.StatusNotFound)
			return
		}
		routes.ServeHTTP(c.Writer, c.Request)
	}
}

func serveStaticAsset(c *gin.Context, asset staticAsset) {
	if asset.immutable {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	} else {
		c.Header("Cache-Control", "no-cache")
	}
	if contentType := mime.TypeByExtension(path.Ext(asset.name)); contentType != "" {
		c.Header("Content-Type", contentType)
	}
	http.ServeContent(c.Writer, c.Request, path.Base(asset.name), time.Time{}, bytes.NewReader(asset.content))
}
