package web

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/marein/dev-cockpit/internal/coder"
)

func (s *Server) handleCoderRename(c *gin.Context) {
	id := c.Param("id")
	co, _, err := s.resolveRunning(id)
	if err != nil {
		if _, _, stored := s.resolveResumable(id); stored == nil {
			c.String(http.StatusConflict, "Inactive coders cannot be renamed.")
			return
		}
		c.String(http.StatusGone, err.Error())
		return
	}
	name, err := co.Rename(id, c.PostForm("name"))
	if err != nil {
		switch {
		case errors.Is(err, coder.ErrNotRunning):
			c.String(http.StatusGone, err.Error())
		case errors.Is(err, coder.ErrRenameUnsupported):
			c.String(http.StatusConflict, err.Error())
		default:
			c.String(http.StatusBadRequest, err.Error())
		}
		return
	}
	c.JSON(http.StatusOK, map[string]string{"name": name})
}
