package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/web/render"
)

// jingleSettingKey holds the cross-device notification jingle selection in
// the settings store.
const jingleSettingKey = "notification-jingle"

const defaultJingle = "arpeggio"

var jingleOptions = []render.JingleOption{
	{ID: "arpeggio", Label: "Arpeggio"},
	{ID: "doorbell", Label: "Doorbell"},
	{ID: "starlight", Label: "Starlight"},
	{ID: "retro", Label: "Retro"},
	{ID: "calm", Label: "Calm"},
}

func validJingle(id string) bool {
	for _, option := range jingleOptions {
		if option.ID == id {
			return true
		}
	}
	return false
}

// selectedJingle returns the stored jingle, falling back to the default when
// nothing valid is stored yet.
func (s *Server) selectedJingle() string {
	if id := s.settings.Get(jingleSettingKey); validJingle(id) {
		return id
	}
	return defaultJingle
}

func (s *Server) handleSettings(c *gin.Context) {
	c.Redirect(http.StatusSeeOther, "/settings/notifications")
}

func (s *Server) handleSettingsNotifications(c *gin.Context) {
	c.HTML(http.StatusOK, "settings_notifications.gohtml", render.SettingsNotificationsData{
		Page:     s.page(c, "Settings", "settings"),
		Jingles:  jingleOptions,
		Selected: s.selectedJingle(),
	})
}

func (s *Server) handleSettingsNotificationsSave(c *gin.Context) {
	jingle := c.PostForm("jingle")
	if !validJingle(jingle) {
		s.redirectWithFlash(c, "/settings/notifications", "", "Unknown jingle.")
		return
	}
	s.settings.Set(jingleSettingKey, jingle)
	s.redirectWithFlash(c, "/settings/notifications", "Settings saved.", "")
}
