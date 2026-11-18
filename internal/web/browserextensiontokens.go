package web

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/browserextension"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// A token opens the cockpit to a browser, no assistant makes or reads one.
const browserExtensionTokensLocalRefusal = "Only the user manages extension tokens, in the browser. Ask the user."

func (s *Server) refuseLocalBrowserExtensionTokens(c *gin.Context) bool {
	if !s.localCall(c) {
		return false
	}
	c.JSON(http.StatusForbidden, gin.H{"error": browserExtensionTokensLocalRefusal})
	return true
}

func (s *Server) browserExtensionTokenRows() []render.BrowserExtensionToken {
	var rows []render.BrowserExtensionToken
	for _, t := range s.browserExtensionTokens.List() {
		rows = append(rows, render.BrowserExtensionToken{ID: t.ID, Name: t.Name, Created: machineTime(t.CreatedAt), Used: machineTime(t.UsedAt)})
	}
	return rows
}

// Only the hash is stored, so this answer is the one place the token ever
// stands, and nothing may keep it.
func (s *Server) createBrowserExtensionToken(c *gin.Context) {
	created, token, err := s.browserExtensionTokens.Create(c.PostForm("name"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"name": created.Name, "token": token})
}

func (s *Server) revokeBrowserExtensionToken(c *gin.Context) {
	if err := s.browserExtensionTokens.Revoke(c.PostForm("id")); err != nil {
		code := http.StatusInternalServerError
		message := "The token could not be revoked. Try again."
		if errors.Is(err, browserextension.ErrNotFound) {
			code, message = http.StatusBadRequest, "That token is gone already."
		}
		s.renderSettingsBrowser(c, code, message)
		return
	}
	s.redirectWithAnchoredFlash(c, settingsBrowserPath, "settings-browser-tokens", "Token revoked.", "")
}
