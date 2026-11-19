package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
)

// assistantStart is a create over the local socket, what `assistant-new`
// posts: the page's create with a name and a first message the new assistant
// answers as if the user had sent it. When to run it is the assistant's
// instructions' rule, on the user's word alone; the server checks the input.
// A first message that could not go out after the assistant was made is
// reported beside it, the assistant stands.
func (s *Server) assistantStart(c *gin.Context, coderID string) {
	created, run, err := s.startAssistant(assistant.Handover{
		CoderID: coderID,
		Name:    c.PostForm("name"),
		Message: c.PostForm("message"),
	})
	if err != nil && created.ID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	answer := gin.H{"id": created.ID, "name": created.Title, "answering": run.RunID != ""}
	if err != nil {
		answer["messageError"] = err.Error()
	}
	c.JSON(http.StatusOK, answer)
}

// startAssistant is the create with a name and a first message that
// `assistant-new` and a plugin run. A coder the handover names wins, else
// assistantDefaultCoderID.
func (s *Server) startAssistant(h assistant.Handover) (assistant.Instance, assistant.Run, error) {
	if h.CoderID == "" {
		h.CoderID = s.assistantDefaultCoderID(s.assistants.Coders())
	}
	return s.assistants.CreateWith(h)
}

// assistantDefaultCoderID picks from coders the one a create without a coder
// runs on: the one the page's New offers, else the first, as the service
// takes it for an empty id. The plugins' coder list marks the same one.
func (s *Server) assistantDefaultCoderID(coders []assistant.CoderInfo) string {
	if id := s.assistantLastCoderID(); id != "" {
		return id
	}
	if len(coders) > 0 {
		return coders[0].ID
	}
	return ""
}
