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
// A coder the call names wins, else the one the page's New offers. A first
// message that could not go out after the assistant was made is reported
// beside it, the assistant stands.
func (s *Server) assistantStart(c *gin.Context, coderID string) {
	if coderID == "" {
		coderID = s.assistantLastCoderID()
	}
	created, run, err := s.assistants.CreateWith(assistant.Handover{
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
