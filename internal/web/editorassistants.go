package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// handleAssistantTab answers one assistant's surface as an editor tab shows
// it: the work column of its page without the page around it. The tab pulls
// this address again where the page pulls its own, the whole transcript
// included once it was asked for.
func (s *Server) handleAssistantTab(c *gin.Context) {
	current, err := s.assistants.Get(strings.TrimSpace(c.Param("id")))
	if err != nil {
		c.String(http.StatusNotFound, "There is no such assistant.")
		return
	}
	data := s.assistantSurface(c, current)
	data.TabURL = data.Path + "/tab"
	if data.AllURL != "" {
		data.AllURL = data.TabURL + strings.TrimPrefix(data.AllURL, data.Path)
	}
	if c.Query("all") != "" {
		data.TabURL += "?all=1"
	}
	c.HTML(http.StatusOK, "assistant_work.gohtml", data)
}

// handleEditorAssistants answers the assistants as the editor lists them, a
// row of its picker each. The editor pulls it on the assistant and the
// notification signals, so a failure that is read loses its red there.
func (s *Server) handleEditorAssistants(c *gin.Context) {
	c.HTML(http.StatusOK, "editor_assistants.gohtml", render.EditorAssistantsData{
		Cards:  s.assistantCards(),
		Failed: s.notifier.UnreadFailed(),
		Coders: s.assistantCoderOptions(),
	})
}
