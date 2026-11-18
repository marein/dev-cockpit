package web

import (
	"net/http"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/browserextension"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const settingsBrowserPath = "/settings/browser"

// The page lists the tokens, which the local socket never reads.
func (s *Server) handleSettingsBrowser(c *gin.Context) {
	if s.refuseLocalBrowserExtensionTokens(c) {
		return
	}
	s.renderSettingsBrowser(c, http.StatusOK, "")
}

func (s *Server) handleSettingsBrowserSave(c *gin.Context) {
	if s.refuseLocalBrowserExtensionTokens(c) {
		return
	}
	switch c.PostForm("form") {
	case "new-token":
		s.createBrowserExtensionToken(c)
	case "revoke-token":
		s.revokeBrowserExtensionToken(c)
	default:
		s.renderSettingsBrowser(c, http.StatusBadRequest, "Unknown form.")
	}
}

func cutSent(sent string, limit int) string {
	if len(sent) <= limit {
		return sent
	}
	for limit > 0 && !utf8.RuneStart(sent[limit]) {
		limit--
	}
	return sent[:limit]
}

func (s *Server) renderSettingsBrowser(c *gin.Context, code int, tokensRefusal string) {
	c.HTML(code, "settings_browser.gohtml", render.SettingsBrowserData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("browser"),
		Tokens:      s.browserExtensionTokenRows(),
		TokensError: tokensRefusal,
		NameMax:     browserextension.MaxNameLength,
		Version:     browserextension.Version,
	})
}
