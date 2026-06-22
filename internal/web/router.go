package web

import (
	"bytes"
	"mime"
	"net/http"
	"path"
	"time"

	"github.com/gin-gonic/gin"
)

// registerRoutes attaches all HTTP routes to the Gin router.
func (s *Server) registerRoutes(r *gin.Engine) {
	r.NoRoute(s.handleNotFound)
	r.NoMethod(s.handleMethodNotAllowed)
	r.GET("/health", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	s.registerStaticRoutes(r)

	browser := r.Group("/", s.csrfMiddleware())
	browser.GET("/login", s.handleLoginGet)
	browser.POST("/login", s.handleLoginPost)

	auth := browser.Group("/", s.requireAuth)
	auth.GET("/", func(c *gin.Context) { c.Redirect(http.StatusSeeOther, "/projects") })
	auth.POST("/logout", s.handleLogout)

	auth.GET("/sessions/new", s.handleSessionNew)
	auth.POST("/sessions/new", s.handleSessionCreate)
	auth.GET("/sessions/:id", s.handleSessionAttach)
	auth.POST("/sessions/:id/stop", s.handleSessionStop)
	auth.GET("/sessions/:id/files", s.handleSessionFiles)
	auth.POST("/sessions/:id/files", s.handleSessionFileUpload)
	auth.GET("/sessions/:id/files/download", s.handleSessionFileDownload)
	auth.POST("/sessions/:id/files/delete", s.handleSessionFileDelete)
	auth.POST("/sessions/:id/input", s.handleSessionInput)
	auth.POST("/sessions/:id/resize", s.handleSessionResize)
	auth.GET("/sessions/:id/stream", s.handleSessionStream)

	auth.POST("/resumable/:id/resume", s.handleResumableResume)
	auth.POST("/resumable/:id/delete", s.handleResumableDelete)

	auth.GET("/shells/new", s.handleShellNew)
	auth.POST("/shells/new", s.handleShellCreate)
	auth.GET("/shells/:id", s.handleShellAttach)
	auth.POST("/shells/:id/delete", s.handleShellDelete)
	auth.POST("/shells/:id/rename", s.handleShellRename)
	auth.POST("/shells/:id/input", s.handleShellInput)
	auth.POST("/shells/:id/resize", s.handleShellResize)
	auth.GET("/shells/:id/stream", s.handleShellStream)

	auth.GET("/agents", s.handleAgentsList)
	auth.GET("/agents/new", s.handleAgentNew)
	auth.GET("/agents/:id/edit", s.handleAgentEdit)
	auth.POST("/agents", s.handleAgentCreate)
	auth.POST("/agents/:id", s.handleAgentUpdate)
	auth.POST("/agents/:id/delete", s.handleAgentDelete)

	auth.GET("/skills", s.handleSkillsList)
	auth.GET("/skills/new", s.handleSkillNew)
	auth.GET("/skills/:id/edit", s.handleSkillEdit)
	auth.POST("/skills", s.handleSkillCreate)
	auth.POST("/skills/:id", s.handleSkillUpdate)
	auth.POST("/skills/:id/delete", s.handleSkillDelete)

	auth.GET("/instructions", s.handleInstructionsEdit)
	auth.POST("/instructions", s.handleInstructionsUpdate)

	auth.GET("/update/check", s.handleUpdateCheck)
	auth.POST("/update/apply", s.handleUpdateApply)

	auth.GET("/projects", s.handleProjectsList)
	auth.GET("/projects/new", s.handleProjectNew)
	auth.POST("/projects", s.handleProjectCreate)
	auth.POST("/projects/delete", s.handleProjectDelete)
	auth.GET("/projects/:name/editor", s.handleProjectEditor)
	auth.GET("/projects/:name/editor/list", s.handleEditorList)
	auth.GET("/projects/:name/editor/file", s.handleEditorReadFile)
	auth.POST("/projects/:name/editor/file", s.handleEditorSaveFile)
	auth.POST("/projects/:name/editor/create", s.handleEditorCreateFile)
	auth.POST("/projects/:name/editor/mkdir", s.handleEditorCreateDir)
	auth.POST("/projects/:name/editor/delete", s.handleEditorDeletePath)
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
