package web

import (
	"bytes"
	"mime"
	"net/http"
	"path"
	"time"

	"github.com/gin-gonic/gin"
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
	auth.GET("/settings/notifications", s.handleSettingsNotifications)
	auth.POST("/settings/notifications", s.handleSettingsNotificationsSave)
	auth.GET("/settings/general", s.handleSettingsGeneral)
	auth.POST("/settings/general", s.handleSettingsGeneralSave)
	auth.GET("/settings/backup", s.handleSettingsBackup)
	auth.POST("/settings/backup", s.handleSettingsBackupSave)
	auth.GET("/settings/backup/new", s.handleSettingsBackupNew)
	auth.POST("/settings/backup/new", s.handleSettingsBackupCreate)
	auth.GET("/settings/backup/list", s.handleSettingsBackupList)
	auth.GET("/settings/backup/download", s.handleSettingsBackupDownload)
	auth.GET("/settings/backup/merge", s.handleSettingsBackupMerge)
	auth.POST("/settings/backup/merge", s.handleSettingsBackupMergeSave)

	auth.GET("/notifications", s.handleNotificationsList)
	auth.POST("/notifications/read", s.handleNotificationsRead)

	// /events is the app-wide server to client stream.
	auth.GET("/events", s.handleEventStream)

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
	auth.GET("/projects/:name/editor/list", s.handleEditorList)
	auth.GET("/projects/:name/editor/file", s.handleEditorReadFile)
	auth.GET("/projects/:name/editor/raw", s.handleEditorRaw)
	auth.GET("/projects/:name/editor/archive", s.handleEditorArchive)
	auth.POST("/projects/:name/editor/file", s.handleEditorSaveFile)
	auth.POST("/projects/:name/editor/create", s.handleEditorCreateFile)
	auth.POST("/projects/:name/editor/mkdir", s.handleEditorCreateDir)
	auth.POST("/projects/:name/editor/delete", s.handleEditorDeletePath)
	auth.POST("/projects/:name/editor/rename", s.handleEditorRename)
	auth.POST("/projects/:name/editor/move", s.handleEditorMove)
	auth.POST("/projects/:name/editor/copy", s.handleEditorCopy)
	auth.POST("/projects/:name/editor/extract", s.handleEditorExtract)
	auth.GET("/projects/:name/editor/files", s.handleEditorFiles)
	auth.GET("/projects/:name/editor/search", s.handleEditorSearch)
	auth.POST("/projects/:name/editor/upload", s.handleEditorUpload)
	auth.POST("/projects/:name/editor/preview", s.handleEditorPreview)
	auth.GET("/projects/:name/editor/git/changes", s.handleEditorGitChanges)
	auth.GET("/projects/:name/editor/git/blame", s.handleEditorGitBlame)
	auth.GET("/projects/:name/editor/git/file", s.handleEditorGitFile)
	auth.POST("/projects/:name/editor/git/watch", s.handleEditorGitWatch)
}

func (s *Server) registerStaticRoutes(r *gin.Engine) {
	for assetURL, asset := range s.assets.byURL {
		assetURL, asset := assetURL, asset
		r.GET(assetURL, func(c *gin.Context) { serveStaticAsset(c, asset) })
		r.HEAD(assetURL, func(c *gin.Context) { serveStaticAsset(c, asset) })
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
