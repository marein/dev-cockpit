package web

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/push"
)

// maxURLLength bounds every user supplied URL: the push endpoint, a webhook,
// and the base URL. Real values stay well below it.
const maxURLLength = 2048

// parseHTTPURL validates a user supplied absolute URL: http(s), a host, and
// the length bound. httpsOnly further restricts the scheme.
func parseHTTPURL(raw string, httpsOnly bool) (*url.URL, bool) {
	if raw == "" || len(raw) > maxURLLength {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return nil, false
	}
	if u.Scheme == "https" || (!httpsOnly && u.Scheme == "http") {
		return u, true
	}
	return nil, false
}

// validSubscriptionKey reports whether value is base64url of exactly size
// bytes, the shape PushManager produces (65 byte uncompressed P-256 point
// for p256dh, 16 byte auth secret).
func validSubscriptionKey(value string, size int) bool {
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(value, "="))
	return err == nil && len(raw) == size
}

type pushSubscribeRequest struct {
	Endpoint string `json:"endpoint" binding:"required"`
	P256dh   string `json:"p256dh" binding:"required"`
	Auth     string `json:"auth" binding:"required"`
	Label    string `json:"label"`
}

func (s *Server) handlePushSubscribe(c *gin.Context) {
	var req pushSubscribeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The subscription is incomplete."})
		return
	}
	if _, ok := parseHTTPURL(req.Endpoint, true); !ok {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The subscription endpoint is not a valid https URL."})
		return
	}
	if !validSubscriptionKey(req.P256dh, 65) || !validSubscriptionKey(req.Auth, 16) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The subscription keys are invalid."})
		return
	}
	label := strings.TrimSpace(req.Label)
	if label == "" {
		label = "Device"
	}
	if runes := []rune(label); len(runes) > 80 {
		label = string(runes[:80])
	}
	if err := s.pusher.WebPush.Register(push.Subscription{
		Endpoint: req.Endpoint,
		P256dh:   req.P256dh,
		Auth:     req.Auth,
		Label:    label,
	}); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handlePushUnsubscribe(c *gin.Context) {
	endpoint := c.PostForm("endpoint")
	if endpoint == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "An endpoint is required."})
		return
	}
	s.pusher.WebPush.RemoveEndpoint(endpoint)
	c.JSON(http.StatusOK, gin.H{"ok": true})
}

func (s *Server) handlePushTest(c *gin.Context) {
	msg := push.TestMessage()
	switch c.PostForm("channel") {
	case "webpush":
		if s.pusher.WebPush.LiveCount() == 0 {
			errMsg := "No devices are registered."
			if len(s.pusher.WebPush.Devices()) > 0 {
				errMsg = "The registered devices are bound to old keys, enable each device again."
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": errMsg})
			return
		}
		if err := s.pusher.WebPush.Deliver(msg); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "Sending failed: " + err.Error()})
			return
		}
	case "webhook":
		if err := s.pusher.Webhooks.Test(c.PostForm("id"), msg); err != nil {
			if errors.Is(err, push.ErrUnknownWebhook) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "This webhook no longer exists, reload the page."})
				return
			}
			c.JSON(http.StatusBadGateway, gin.H{"error": "Sending failed: " + err.Error()})
			return
		}
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown channel."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"ok": true})
}
