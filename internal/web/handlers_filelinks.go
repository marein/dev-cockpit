package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// The live terminal marks the files of its project in xterm's own buffer
// (@dc/termlinks) and asks here once for each word it shows whether it names
// one.
type fileLinksRequest struct {
	Words []string `json:"words"`
}

// fileLinksMax caps the words of one request, more than a screen holds.
const fileLinksMax = 500

func (s *Server) handleFileLinks(c *gin.Context) {
	ref, ok := s.terminalSessions()[c.Param("id")]
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "This terminal is not running."})
		return
	}
	var req fileLinksRequest
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Words) > fileLinksMax {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The words could not be read."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"links": s.fileLinker(ref.CWD).wordLinks(req.Words)})
}
