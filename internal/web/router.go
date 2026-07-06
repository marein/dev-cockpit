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

	auth.GET("/coders/new", s.handleCoderNew)
	auth.POST("/coders/new", s.handleCoderCreate)
	auth.GET("/coders/:id", s.handleCoderAttach)
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
