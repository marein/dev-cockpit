package web

import (
	"errors"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/markdown"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// The copy view of a coder terminal: its recorded conversation as bubbles,
// read with its structure standing (coder.Manager.Conversation), a snapshot
// taken when the view opens. GET /coders/:id/conversation answers the newest
// page as an HTML fragment, and with ?before=<message id> the page that
// stands above that message.
func (s *Server) handleCoderConversation(c *gin.Context) {
	id := c.Param("id")
	co, running, err := s.resolveRunning(id)
	if err != nil {
		c.String(http.StatusNotFound, userFacingError(c, err))
		return
	}
	renderCoderConversation(c, co, id, s.fileFinder(running.CWD))
}

// renderCoderConversation answers the page for a running session. A record
// that is not written yet is an empty conversation, every other failure says
// what went wrong, so a broken record never reads as one nobody spoke in.
// The bubbles carry their file links in their markup, find names them.
func renderCoderConversation(c *gin.Context, co *coder.Manager, id string, find func(string) []fileLink) {
	data := render.CoderCopyData{}
	conversation, err := co.Conversation(id, coder.TranscriptPage, strings.TrimSpace(c.Query("before")))
	switch {
	case errors.Is(err, coder.ErrNoConversation), errors.Is(err, coder.ErrConversationChanged):
		data.Note = err.Error()
	case errors.Is(err, fs.ErrNotExist):
		data.Note = "Nothing was said yet."
	case err != nil:
		log.Printf("%s %s: %v", c.Request.Method, c.Request.URL.Path, err)
		data.Note = userFacingError(c, err)
	case len(conversation.Messages) == 0:
		data.Note = "Nothing was said yet."
	}
	data.Messages = copyViews(render.CoderLabel(co.ID()), conversation.Messages)
	linkCopyViews(data.Messages, find)
	data.Dropped = conversation.Dropped
	if data.Dropped > 0 {
		data.Before = conversation.Messages[0].ID
	}
	c.HTML(http.StatusOK, "coder_copy_page.gohtml", data)
}

// copyViews renders the messages as bubbles: the user's words as plain text,
// a command as a chip, a coder's turn as its parts in record order. What the
// copy button of a bubble carries is the recorded text, markdown as written.
func copyViews(author string, messages []coder.Message) []render.ChatMessageView {
	views := make([]render.ChatMessageView, 0, len(messages))
	for _, m := range messages {
		view := render.ChatMessageView{ID: m.ID, Time: machineTime(m.Time), Copy: m.Text()}
		if m.Role == coder.RoleUser {
			view.User = true
			view.Author = "You"
			view.Text = m.Text()
			view.Command = m.Kind == coder.KindCommand
			view.HTML = plainTextHTML(m.Text())
		} else {
			view.Coder = true
			view.Author = author
			for _, p := range m.Parts {
				part := render.ChatPartView{Tool: p.Tool, Line: p.Text, HTML: template.HTML(template.HTMLEscapeString(p.Text))}
				if p.Kind == coder.PartText {
					part = render.ChatPartView{HTML: copyMarkdown(p.Text)}
				}
				view.Parts = append(view.Parts, part)
			}
		}
		views = append(views, view)
	}
	return views
}

// copyLinkBudget is how much text of one page is searched for file links, in
// bytes. Text dense with paths costs about 200ms per MiB, a page may hold up
// to coder.TranscriptCap, so a quarter of that keeps a page at about 50ms.
const copyLinkBudget = coder.TranscriptCap / 4

// linkCopyViews links the file mentions in the bubbles' words and tool lines,
// the newest first, which the view opens on.
func linkCopyViews(views []render.ChatMessageView, find func(string) []fileLink) {
	budget := copyLinkBudget
	for i := len(views) - 1; i >= 0; i-- {
		view := &views[i]
		view.HTML = linkedHTML(view.HTML, &budget, find)
		for j := range view.Parts {
			view.Parts[j].HTML = linkedHTML(view.Parts[j].HTML, &budget, find)
		}
	}
}

// copyMarkdown renders a coder's words the way the assistant's are rendered,
// raw HTML dropped by the renderer; a text the renderer refuses stands as
// plain text rather than not at all.
func copyMarkdown(text string) template.HTML {
	html, err := markdown.RenderGFM(text)
	if err != nil {
		return plainTextHTML(text)
	}
	return template.HTML(html)
}
