package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
)

// assistantPingInterval is how often the conversation stream proves it is
// alive with a frame the browser can see. The keepalive beside it is an SSE
// comment on a much shorter beat, which holds the socket open but tells the
// page nothing, and a visible frame every second would be noise on a phone.
// Same 15 seconds as the ping on /events, and the client judges it the same
// way: silent past 45 seconds means the socket died, which leaves room for one
// missed ping. A variable so a test does not have to wait out the beat.
var assistantPingInterval = 15 * time.Second

// assistantStreamSnapshotHook runs, when set, right after the stream reads
// the instance for its connect snapshot and before that snapshot goes out.
// Nil in production, a test uses it to land a SetModels exactly there, after
// the subscribe so the write's frame is not lost, before the snapshot is
// written so the snapshot itself still carries the older picks.
var assistantStreamSnapshotHook func(id string)

// conversationFrame is what one reader of a conversation stream gets of a
// frame, false keeps the frame from it.
type conversationFrame func(inst assistant.Instance, ev assistant.StreamEvent) (assistant.StreamEvent, bool)

// handleAssistantStream is one assistant's own SSE channel. Answer text
// never travels the app wide event stream.
func (s *Server) handleAssistantStream(c *gin.Context) {
	id := c.Param("id")
	if _, err := s.assistants.Get(id); err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
	s.streamConversation(c, id, s.ownerFrame)
}

// ownerFrame gives the owner's page every frame: the model named as the page
// shows it, and the picks with the launcher and warning of the assistant's
// coder.
func (s *Server) ownerFrame(inst assistant.Instance, ev assistant.StreamEvent) (assistant.StreamEvent, bool) {
	switch ev.Kind {
	case assistant.FrameStart:
		ev.Model = pickDisplayName(ev.Model, ev.Effort)
	case assistant.FrameModels:
		if ev.Models != nil {
			picks := s.modelsFrame(inst.CoderID, *ev.Models)
			ev.Models = &picks
		}
	}
	return ev, true
}

// streamConversation writes one assistant's stream for every reader of it:
// the picks it opens with, the answer running at connect, every frame of the
// hub after that, and the ping. What a reader gets of each frame is its
// frame's answer, the loop knows no reader.
func (s *Server) streamConversation(c *gin.Context, id string, frame conversationFrame) {
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, ok := w.(http.Flusher); !ok {
		c.String(http.StatusInternalServerError, "streaming unsupported")
		return
	}
	if err := writeSSERetry(w, time.Second); err != nil {
		return
	}
	// Subscribe before reading the picks for the connect snapshot: a SetModels
	// landing between the two would otherwise publish its frame before the
	// subscriber exists, and the snapshot read afterwards would still carry
	// the older picks, leaving the page on them until the next move or
	// reconnect. Subscribed first, that save arrives as a fresh frame right
	// behind the snapshot and wins on its own stamp.
	snapshot, running, events, unsubscribe := s.assistants.Subscribe(id)
	defer unsubscribe()
	inst, err := s.assistants.Get(id)
	if err != nil {
		return
	}
	if assistantStreamSnapshotHook != nil {
		assistantStreamSnapshotHook(id)
	}
	write := func(ev assistant.StreamEvent) error {
		ev, ok := frame(inst, ev)
		if !ok {
			return nil
		}
		return writeConversationEvent(w, ev)
	}
	picks := inst.ModelPicks()
	if err := write(assistant.StreamEvent{Kind: assistant.FrameModels, Models: &picks}); err != nil {
		return
	}
	if running {
		if err := write(snapshot); err != nil {
			return
		}
	}

	heartbeat := time.NewTicker(s.cfg.StreamHeartbeatInterval)
	defer heartbeat.Stop()
	ping := time.NewTicker(assistantPingInterval)
	defer ping.Stop()
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := write(ev); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := writeSSEKeepalive(w); err != nil {
				return
			}
		case <-ping.C:
			if err := write(assistant.StreamEvent{Kind: assistant.FramePing}); err != nil {
				return
			}
		}
	}
}

// writeConversationEvent sends one stream frame as JSON under the "assistant"
// event name.
func writeConversationEvent(w http.ResponseWriter, ev assistant.StreamEvent) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return writeSSEvent(w, "assistant", string(payload))
}
