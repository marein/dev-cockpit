package web

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// handleServerTime answers the server's clock in unix milliseconds. A working
// mark runs from the moment the server raised it, so a page measures its
// offset to this clock once, corrected by half the round trip, and reads the
// time since a mark went up through it: a client clock minutes behind would
// put every mark before its start, where each redraw restarts it. The offset
// then stays for the page, one that moved would move every mark with it.
func (s *Server) handleServerTime(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"now": time.Now().UnixMilli()})
}
