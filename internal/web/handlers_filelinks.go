package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

// The live terminal marks the files of its project in xterm's own buffer
// (@dc/termlinks) and asks here once for each word it shows whether it names
// one. A bare word it never asks, it asks for the root's bare file names with
// their links instead, and sends the version of those it holds back.
type fileLinksRequest struct {
	Words   []string `json:"words"`
	Names   bool     `json:"names"`
	Version string   `json:"version"`
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
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Words) > fileLinksMax || len(req.Version) > namesVersionLen {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The words could not be read."})
		return
	}
	linker := s.fileLinker(ref.CWD)
	answer := gin.H{"links": linker.wordLinks(req.Words)}
	if req.Names {
		names, version := linker.bareNames(req.Version)
		answer["version"] = version
		if names != nil {
			answer["names"] = names
		}
	}
	c.JSON(http.StatusOK, answer)
}
