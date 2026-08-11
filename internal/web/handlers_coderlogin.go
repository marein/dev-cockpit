package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/web/render"
)

// handleCoderAccount is the coder's account section: who the CLI is logged in
// as, and the button that starts the browser login.
func (s *Server) handleCoderAccount(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		state := s.coderLogin.State(co.ID())
		c.HTML(http.StatusOK, "coder_account.gohtml", render.CoderAccountData{
			Page:        s.page(c, s.coderTitle(co, "Account"), "settings"),
			SettingsNav: s.coderSettingsNav("coder", co, "account"),
			Base:        s.coderBase(co),
			CoderID:     co.ID(),
			LoggedIn:    state.LoggedIn,
			Account:     state.Account,
			Detail:      state.Detail,
		})
	}
}

// handleCoderLoginDescribe answers the login state and the running flow as
// JSON, the read half of the dialog's poll.
func (s *Server) handleCoderLoginDescribe(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, s.coderLogin.Describe(co.ID()))
	}
}

// coderLoginRequest is the write half's body. The code is claude's pasted
// authorization code; it travels into the waiting process and is neither
// logged nor stored anywhere on the way.
type coderLoginRequest struct {
	Action string `json:"action"`
	Code   string `json:"code"`
}

func (s *Server) handleCoderLoginAction(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		var req coderLoginRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "The request could not be read."})
			return
		}
		switch req.Action {
		case "start":
			if err := s.coderLogin.Start(co.ID()); err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
		case "answer":
			if err := s.coderLogin.Answer(co.ID(), req.Code); err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
		case "cancel":
			s.coderLogin.Cancel(co.ID())
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown action."})
			return
		}
		c.JSON(http.StatusOK, s.coderLogin.Describe(co.ID()))
	}
}
