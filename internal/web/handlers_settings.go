package web

import (
	"net/http"
	"strings"

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
	devices := make([]render.PushDevice, 0)
	staleDevices := false
	for _, sub := range s.pusher.WebPush.Devices() {
		stale := s.pusher.WebPush.Stale(sub)
		staleDevices = staleDevices || stale
		icon := "ti-device-desktop"
		if strings.Contains(sub.Label, "iPhone") || strings.Contains(sub.Label, "iPad") || strings.Contains(sub.Label, "Android") {
			icon = "ti-device-mobile"
		}
		devices = append(devices, render.PushDevice{
			ID:       sub.ID,
			Label:    sub.Label,
			Endpoint: sub.Endpoint,
			Added:    sub.CreatedAt.Format("2006-01-02"),
			Icon:     icon,
			Stale:    stale,
		})
	}
	webhooks := make([]render.PushWebhook, 0)
	for _, hook := range s.pusher.Webhooks.List() {
		webhooks = append(webhooks, render.PushWebhook{ID: hook.ID, URL: hook.URL})
	}
	c.HTML(http.StatusOK, "settings_notifications.gohtml", render.SettingsNotificationsData{
		Page:           s.page(c, "Settings", "settings"),
		Jingles:        jingleOptions,
		Selected:       s.selectedJingle(),
		VAPIDPublicKey: s.pusher.WebPush.PublicKey(),
		Devices:        devices,
		StaleDevices:   staleDevices,
		Webhooks:       webhooks,
		BaseURL:        s.pusher.BaseURL(),
	})
}

// handleSettingsNotificationsSave dispatches the settings page forms on
// their hidden form field, so every form POSTs to the path that renders it.
// The empty value stays routed to the jingle form, it predates the marker.
func (s *Server) handleSettingsNotificationsSave(c *gin.Context) {
	switch c.PostForm("form") {
	case "base-url":
		s.saveBaseURL(c)
	case "webhook-add":
		s.addWebhook(c)
	case "webhook-remove":
		if s.pusher.Webhooks.Remove(c.PostForm("id")) {
			s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webhooks", "Webhook removed.", "")
			return
		}
		s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webhooks", "", "The webhook was already gone.")
	case "push-remove":
		if s.pusher.WebPush.RemoveDevice(c.PostForm("id")) {
			s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webpush", "Device removed.", "")
			return
		}
		s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webpush", "", "The device was already gone.")
	case "", "jingle":
		s.saveJingleSetting(c)
	default:
		s.redirectWithFlash(c, "/settings/notifications", "", "Unknown form.")
	}
}

func (s *Server) saveJingleSetting(c *gin.Context) {
	jingle := c.PostForm("jingle")
	if !validJingle(jingle) {
		s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-jingle", "", "Unknown jingle.")
		return
	}
	s.settings.Set(jingleSettingKey, jingle)
	s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-jingle", "Settings saved.", "")
}

func (s *Server) saveBaseURL(c *gin.Context) {
	base := strings.TrimSpace(c.PostForm("base_url"))
	if base != "" {
		u, ok := parseHTTPURL(base, false)
		if !ok || u.RawQuery != "" || u.Fragment != "" || u.ForceQuery {
			s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-base-url", "", "The base URL must be a plain http(s) address without query or fragment.")
			return
		}
		base = strings.TrimRight(base, "/")
	}
	s.pusher.SetBaseURL(base)
	s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-base-url", "Settings saved.", "")
}

func (s *Server) addWebhook(c *gin.Context) {
	webhook := strings.TrimSpace(c.PostForm("url"))
	if _, ok := parseHTTPURL(webhook, false); !ok {
		s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webhooks", "", "The webhook must be an http(s) URL.")
		return
	}
	if err := s.pusher.Webhooks.Add(webhook); err != nil {
		s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webhooks", "", err.Error())
		return
	}
	s.redirectWithAnchoredFlash(c, "/settings/notifications", "settings-webhooks", "Webhook added.", "")
}
