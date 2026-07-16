package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/eventbus"
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

// handleEventStream is the single server to client push channel, served at /events.
// It carries every server event, not only notifications:
// each SSE frame is a {type, data} envelope sent under the event name "dc", which
// the @dc/events client re-dispatches as a dc:<type> DOM event so any custom
// element can subscribe. On connect, including every EventSource reconnect, it
// pushes a snapshot of the current state (unread notifications plus a terminals
// signal), so a freshly attached or a woken background page catches up in one shot.
func (s *Server) handleEventStream(c *gin.Context) {
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

	notifyEvents, cancelNotify := s.notifier.Subscribe()
	defer cancelNotify()
	busEvents, cancelBus := s.bus.Subscribe()
	defer cancelBus()

	// Snapshot: current unread state plus a bare terminals signal (no project) so
	// the tab strip and quick nav pull their fragment and the projects page
	// reconciles all its sections, catching a page up after connect or reconnect.
	if err := writeEnvelope(w, eventbus.Event{Type: "notifications", Data: s.notifier.UnreadEvent()}); err != nil {
		return
	}
	if err := writeEnvelope(w, eventbus.Event{Type: "terminals"}); err != nil {
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
			// A real "ping" event, not an SSE comment: comments keep the socket
			// warm but fire no client event, so the @dc/events watchdog could not
			// tell a live-but-idle stream from a silently dead one. This lets it.
			if err := writeEnvelope(w, eventbus.Event{Type: "ping"}); err != nil {
				return
			}
		case ev := <-notifyEvents:
			if err := writeEnvelope(w, eventbus.Event{Type: "notifications", Data: ev}); err != nil {
				return
			}
		case ev := <-busEvents:
			if err := writeEnvelope(w, ev); err != nil {
				return
			}
		}
	}
}

func writeEnvelope(w http.ResponseWriter, ev eventbus.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return writeSSEvent(w, "dc", string(data))
}

// publishTerminals signals that the live coder/shell set or its order changed.
// It names the affected project so an open projects page can pull and swap just
// that project's two sections in place; an empty name (a reorder, or the connect
// snapshot) means "refresh everything". Every surface reacts by pulling its own
// per-client fragment (authenticated as that client, carrying its path), so the
// active tab and the CSRF token stay correct and each element keeps its own state
// (unfold, filter). Every call site is also a terminal mutation, so the terminal
// restore snapshot is rewritten here.
func (s *Server) publishTerminals(projectName string) {
	s.restorer.Write()
	s.bus.Publish(eventbus.Event{Type: "terminals", Data: map[string]string{"project": projectName}})
}
