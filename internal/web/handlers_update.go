package web

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/local/dev-cockpit/internal/update"
)

func updateStatusJSON(st update.Status) gin.H {
	return gin.H{
		"supported": true,
		"current":   st.Current,
		"latest":    st.Latest,
		"available": st.Available,
		"writable":  st.Writable,
		"releases":  st.Releases,
	}
}

func (s *Server) handleUpdateCheck(c *gin.Context) {
	if s.updater == nil {
		c.JSON(http.StatusOK, gin.H{"current": s.version, "supported": false})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	c.JSON(http.StatusOK, updateStatusJSON(s.updater.Status(ctx, c.Query("force") == "1")))
}

// handleUpdateApply installs the version pinned by the request body, so the
// user only ever gets the release whose notes the dialog showed. A superseded
// or already installed version yields 409 with a fresh status payload. An
// empty body means newest pending, it keeps stale tabs from before the pin
// updating across one version boundary, see the update surface convention in
// AGENTS.md. TODO(v2.0.0): require the version field.
func (s *Server) handleUpdateApply(c *gin.Context) {
	if s.updater == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "Self-update is not available for this build."})
		return
	}
	var req struct {
		Version string `json:"version"`
	}
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body."})
			return
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	err := s.updater.Apply(ctx, req.Version)
	switch {
	case errors.Is(err, update.ErrUpToDate) || errors.Is(err, update.ErrSuperseded):
		c.JSON(http.StatusConflict, gin.H{
			"error":  err.Error(),
			"status": updateStatusJSON(s.updater.Status(ctx, false)),
		})
		return
	case err != nil:
		c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"restarting": true})
	c.Writer.Flush()
	go func() {
		time.Sleep(300 * time.Millisecond)
		log.Printf("self-update applied, restarting %s", s.updater.ExePath())
		if err := s.updater.Restart(); err != nil {
			log.Printf("self-restart failed: %v", err)
		}
	}()
}
