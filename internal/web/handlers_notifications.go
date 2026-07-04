package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/notify"
)

// notificationListLimit caps how many entries the center fetches at once.
const notificationListLimit = 50

func (s *Server) handleNotificationsList(c *gin.Context) {
	list := s.notifier.List(notificationListLimit)
	if list == nil {
		list = []notify.Notification{}
	}
	c.JSON(http.StatusOK, gin.H{
		"notifications": list,
		"unread":        s.notifier.UnreadCount(),
	})
}

func (s *Server) handleNotificationsRead(c *gin.Context) {
	var unread int
	switch {
	case c.PostForm("all") != "":
		unread = s.notifier.MarkAllRead()
	case c.PostForm("target") != "":
		unread = s.notifier.MarkTargetRead(c.PostForm("target"))
	case c.PostForm("id") != "":
		unread = s.notifier.MarkRead(c.PostForm("id"))
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "A notification id is required."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"unread": unread})
}

// handleNotificationsStream pushes the unread count and freshly ingested
// notifications over SSE. Every event carries the current unread count; a
// new unread notification additionally rides along as "added".
func (s *Server) handleNotificationsStream(c *gin.Context) {
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, ok := w.(http.Flusher); !ok {
		c.String(http.StatusInternalServerError, "streaming unsupported")
		return
	}
	if err := writeSSERetry(w, 2*time.Second); err != nil {
		return
	}

	events, cancel := s.notifier.Subscribe()
	defer cancel()

	if err := writeNotifyEvent(w, s.notifier.UnreadEvent()); err != nil {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if err := writeSSEKeepalive(w); err != nil {
				return
			}
		case ev := <-events:
			if err := writeNotifyEvent(w, ev); err != nil {
				return
			}
		}
	}
}

func writeNotifyEvent(w http.ResponseWriter, ev notify.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return writeSSEvent(w, "notifications", string(data))
}
