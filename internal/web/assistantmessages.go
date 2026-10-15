package web

import (
	"html/template"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/markdown"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// assistantMessageViews renders the transcript. A blocked assistant takes no
// new turn, so it offers no retry and lets no waiting message be removed.
func (s *Server) assistantMessageViews(current assistant.Instance, blocked bool) []render.AssistantMessageView {
	out := make([]render.AssistantMessageView, 0, len(current.Messages))
	for i, m := range current.Messages {
		last := i == len(current.Messages)-1 && !blocked
		out = append(out, s.assistantMessageView(current.ID, m, last, !blocked, current.CoderID))
	}
	return out
}

func (s *Server) assistantMessageView(instanceID string, m assistant.Message, retryable, writable bool, coderID string) render.AssistantMessageView {
	view := render.AssistantMessageView{
		ID:        m.ID,
		RunID:     m.RunID,
		User:      m.Role == assistant.RoleUser,
		Note:      s.assistantNoteView(m.Note, m.Content),
		Auto:      m.Auto,
		Origin:    s.assistantNoteView(m.Origin, m.Content),
		Author:    render.CoderLabel(coderID),
		Model:     m.Model,
		ModelName: modelDisplayName(m.Model),
		Text:      m.Content,
		State:     string(m.State),
		Error:     m.Error,
		Streaming: m.State == assistant.StateStreaming,
		Failed:    m.State == assistant.StateFailed || m.State == assistant.StateInterrupted,
		// A turn a note bought is never sent again by hand: what bought it
		// was an event, and that event is spent.
		CanRetry:   retryable && m.Role == assistant.RoleAssistant && m.State.Retryable() && !m.Auto,
		Queued:     m.State == assistant.StateQueued,
		CanDiscard: writable && m.State == assistant.StateQueued,
		Time:       machineTime(m.CreatedAt),
	}
	for _, a := range m.Attachments {
		path := s.attachmentPath(instanceID, a)
		width, height := assistantImageSize(a.Media, path)
		view.Attachments = append(view.Attachments, render.AssistantAttachmentView{
			Name:     a.Name,
			URL:      s.assistantMediaURL(instanceID, path),
			Media:    a.Media,
			SizeText: filesystem.HumanSize(a.Size),
			Width:    width,
			Height:   height,
		})
	}
	if view.Note != nil {
		view.Author = "Cockpit"
	}
	if view.User {
		view.Author = "You"
		view.HTML = plainTextHTML(m.Content)
	} else if m.Content != "" {
		view.HTML = s.assistantMarkdown(instanceID, m.Content)
	}
	// The speaker renders on every message that can be read aloud, a check's
	// report included, and only while text to speech is on; the audio route
	// asks Speakable again for a page from before a settings change.
	if m.Speakable() && !s.voiceTTSOff() {
		view.AudioURL = "/assistants/" + instanceID + "/messages/" + m.ID + "/audio"
	}
	return view
}

// attachmentPath is where a message's file sits: in the upload folder of the
// assistant whose message it is, under its own name. The name is what
// identifies the file; the absolute path a transcript stored is where that
// folder stood when the message was sent, and a folder moves, with a restore
// onto another host or with a layout that gave every assistant its own
// workspace.
func (s *Server) attachmentPath(instanceID string, a assistant.Attachment) string {
	dir, err := s.assistants.UploadDir(instanceID)
	if err != nil || a.Name == "" {
		return a.Path
	}
	return filepath.Join(dir, a.Name)
}

// assistantImageSize is the pixel size of a picture on disk, and nothing at
// all for every other kind of file: only an image reserves a box in the
// transcript, and a size nobody could read is left unsaid.
func assistantImageSize(media, file string) (int, int) {
	if media != "image" {
		return 0, 0
	}
	return filesystem.ImageSize(file)
}

// assistantMediaURL turns an absolute path inside one assistant's workspace
// into the URL that serves it. A path outside that workspace yields an empty
// URL, so nothing the coder wrote into the transcript can point the browser
// somewhere else.
//
// The URL carries the assistant, and the path is read inside that assistant's
// workspace: see handleAssistantMedia.
func (s *Server) assistantMediaURL(instanceID, absolute string) string {
	rel, err := filepath.Rel(s.workspace.Dir(instanceID), absolute)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return "/assistants/" + instanceID + "/media/" + filepath.ToSlash(rel)
}

// assistantMarkdown renders an answer. A relative path in it is resolved
// against that assistant's workspace and embedded, so a picture, a recording
// or a clip the assistant points at plays in the answer instead of being a
// dead link. Raw HTML stays disabled in the renderer.
func (s *Server) assistantMarkdown(instanceID, src string) template.HTML {
	html, err := markdown.RenderGFMWithMedia(src, func(destination string) (markdown.Media, bool) {
		rel := strings.TrimSpace(destination)
		if rel == "" || strings.Contains(rel, "://") || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "#") {
			return markdown.Media{}, false
		}
		absolute, err := s.workspace.ResolveWorkspaceFile(instanceID, rel)
		if err != nil {
			return markdown.Media{}, false
		}
		kind := assistant.MediaKind(rel)
		width, height := assistantImageSize(kind, absolute)
		return markdown.Media{
			URL:    "/assistants/" + instanceID + "/media/" + path.Clean(filepath.ToSlash(rel)),
			Kind:   kind,
			Width:  width,
			Height: height,
		}, true
	})
	if err != nil {
		return ""
	}
	return template.HTML(html)
}

// handleAssistantMessage serves one rendered message, pulled by the browser
// when a streamed answer finished. The streamed frames carry no file links,
// the finished message does.
func (s *Server) handleAssistantMessage(c *gin.Context) {
	current, err := s.assistants.Get(c.Param("id"))
	if err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
	wanted := c.Param("messageId")
	blocked := s.assistantBlockedReason(current) != ""
	for i, m := range current.Messages {
		if m.ID != wanted {
			continue
		}
		last := i == len(current.Messages)-1 && !blocked
		views := []render.AssistantMessageView{s.assistantMessageView(current.ID, m, last, !blocked, current.CoderID)}
		linkChatViews(views, s.pathLinker().linkedText)
		c.HTML(http.StatusOK, "assistant_message.gohtml", render.AssistantMessageData{Message: views[0]})
		return
	}
	c.String(http.StatusNotFound, "Message not found.")
}
