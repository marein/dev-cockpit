package web

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

func (s *Server) handleUpdateCheck(c *gin.Context) {
	if s.updater == nil {
		c.JSON(http.StatusOK, gin.H{"current": s.version, "supported": false})
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	st := s.updater.Status(ctx, c.Query("force") == "1")
	c.JSON(http.StatusOK, gin.H{
		"supported": true,
		"current":   st.Current,
		"latest":    st.Latest,
		"available": st.Available,
		"writable":  st.Writable,
		"releases":  st.Releases,
	})
}

func (s *Server) handleUpdateApply(c *gin.Context) {
	if s.updater == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "Self-update is not available for this build."})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := s.updater.Apply(ctx); err != nil {
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
