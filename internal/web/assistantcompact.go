package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// assistantCompact compacts the assistant's session on the user's word, from
// the browser alone: a compaction costs a turn and cannot be undone, so no
// process on the local socket starts one.
func (s *Server) assistantCompact(c *gin.Context, id string) {
	if s.localCall(c) {
		c.JSON(http.StatusForbidden, gin.H{"error": "Only the user compacts an assistant, from its page."})
		return
	}
	if err := s.assistantWriteGuard(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	run, err := s.assistants.Compact(id)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	s.assistantRunResponse(c, id, run)
}

func (s *Server) assistantCompactData(current assistant.Instance, blocked bool) *render.AssistantCompact {
	if blocked || !s.assistants.Compacts(current.CoderID) {
		return nil
	}
	return &render.AssistantCompact{Ready: current.HasTurn()}
}

func compactView(m assistant.Message) *render.ChatCompactView {
	if m.Compact == nil {
		return nil
	}
	view := &render.ChatCompactView{Mark: string(m.State), Badge: "bg-secondary-lt"}
	switch m.State {
	case assistant.StateStreaming:
		view.Line = "Compacting the conversation…"
	case assistant.StateComplete:
		view.Mark = m.Compact.Outcome
		view.Line = "Conversation compacted."
		view.Badge = "bg-green-lt"
		if m.Compact.Outcome == assistant.CompactNothing {
			view.Line = "Nothing to compact."
			view.Badge = "bg-secondary-lt"
		}
	case assistant.StateCancelled:
		view.Line = "You stopped the compaction."
	case assistant.StateInterrupted:
		view.Line = "The compaction was interrupted."
	default:
		view.Line = "The conversation was not compacted."
		view.Badge = "bg-red-lt"
	}
	return view
}

func compactHeadline(m assistant.Message) string {
	if view := compactView(m); view != nil {
		return view.Line
	}
	return ""
}
