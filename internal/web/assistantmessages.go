package web

import (
	"html/template"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
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
		view := s.assistantMessageView(current.ID, m, last, !blocked, current.CoderID)
		answerChoices(&view, current.Messages, i, ownerChoicesURL(current.ID, !blocked))
		out = append(out, view)
	}
	return out
}

// chatMedia is where the files a rendered message links are served: base is
// the address they hang under, the owner's media route or a guest's shared
// link. linked, when set, collects those files as paths inside the workspace.
type chatMedia struct {
	base   string
	linked map[string]bool
}

func (m chatMedia) url(rel string) string {
	if m.linked != nil {
		m.linked[rel] = true
	}
	return m.base + rel
}

func ownerMedia(instanceID string) chatMedia {
	return chatMedia{base: "/assistants/" + instanceID + "/media/"}
}

func (s *Server) assistantMessageView(instanceID string, m assistant.Message, retryable, writable bool, coderID string) render.AssistantMessageView {
	return s.messageView(instanceID, m, retryable, writable, coderID, ownerMedia(instanceID))
}

// ownerChoicesURL is where the owner's page answers the questions of an
// answer, nowhere while the assistant takes no message.
func ownerChoicesURL(instanceID string, writable bool) string {
	if !writable {
		return ""
	}
	return "/assistants/" + instanceID
}

func (s *Server) messageView(instanceID string, m assistant.Message, retryable, writable bool, coderID string, media chatMedia) render.AssistantMessageView {
	view := render.AssistantMessageView{
		ID:        m.ID,
		RunID:     m.RunID,
		User:      m.Role == assistant.RoleUser,
		Answered:  m.Role == assistant.RoleUser && m.Answers != nil,
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
		url := ""
		if rel, ok := s.workspaceRel(instanceID, path); ok {
			url = media.url(rel)
		}
		view.Attachments = append(view.Attachments, render.AssistantAttachmentView{
			Name:     a.Name,
			URL:      url,
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
	} else if choices, ok := m.Choices(); ok {
		before, after := s.markdownWithMedia(instanceID, m.Content, media, choices.Fence)
		view.Parts = []render.ChatPartView{{HTML: before}, {Choices: choicesView(m.ID, choices)}, {HTML: after}}
	} else if m.Content != "" {
		view.HTML, _ = s.markdownWithMedia(instanceID, m.Content, media, -1)
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
	rel, ok := s.workspaceRel(instanceID, absolute)
	if !ok {
		return ""
	}
	return ownerMedia(instanceID).url(rel)
}

// workspaceRel is an absolute path inside one assistant's workspace as the
// path its media addresses carry, and false for a path outside it.
func (s *Server) workspaceRel(instanceID, absolute string) (string, bool) {
	rel, err := filepath.Rel(s.workspace.Dir(instanceID), absolute)
	if err != nil || strings.HasPrefix(rel, "..") {
		return "", false
	}
	return filepath.ToSlash(rel), true
}

// markdownWithMedia renders an answer, cut around its top level fenced block
// at cut, which is where the questions it asks stand: what comes before them
// and what after, all of it before for a cut no block holds. A relative path
// in it is resolved against that assistant's workspace and embedded, so a
// picture, a recording or a clip the assistant points at plays in the answer
// instead of being a dead link. Raw HTML stays disabled in the renderer.
func (s *Server) markdownWithMedia(instanceID, src string, media chatMedia, cut int) (template.HTML, template.HTML) {
	before, after, err := markdown.RenderGFMWithMediaAround(src, func(destination string) (markdown.Media, bool) {
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
			URL:    media.url(path.Clean(filepath.ToSlash(rel))),
			Kind:   kind,
			Width:  width,
			Height: height,
		}, true
	}, cut)
	if err != nil {
		return "", ""
	}
	return template.HTML(before), template.HTML(after)
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
		answerChoices(&views[0], current.Messages, i, ownerChoicesURL(current.ID, !blocked))
		s.linkOwnerChat(views, current.ID)
		c.HTML(http.StatusOK, "assistant_message.gohtml", render.AssistantMessageData{Message: views[0]})
		return
	}
	c.String(http.StatusNotFound, "Message not found.")
}

// handleAssistantMedia serves a file out of one assistant's workspace. It goes
// through http.ServeContent, so a video seeks with range requests instead of
// downloading from the start.
//
// The address carries the assistant, and the path is read inside that
// assistant's own workspace, so one assistant's address serves that assistant's
// files and nobody else's. Reading across stays possible where it belongs, on
// disk, where an assistant reads another's workspace with its own file tools;
// what is scoped here is the browser facing URL.
func (s *Server) handleAssistantMedia(c *gin.Context) {
	id := c.Param("id")
	if _, err := s.assistants.Get(id); err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
	if err := s.serveAssistantMedia(c, id, strings.TrimPrefix(c.Param("path"), "/"), false); err != nil {
		c.String(http.StatusNotFound, err.Error())
	}
}

// serveAssistantMedia serves the file at rel inside an assistant's workspace,
// never cached, as a download when the request asks for one. A sandboxed one
// runs no script, whatever the file holds. It answers the error of a path that
// resolves to no file there and writes nothing then.
func (s *Server) serveAssistantMedia(c *gin.Context, id, rel string, sandboxed bool) error {
	target, err := s.workspace.ResolveWorkspaceFile(id, rel)
	if err != nil {
		return err
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if sandboxed {
		c.Header("Content-Security-Policy", "sandbox")
	}
	if c.Query("download") == "1" {
		c.FileAttachment(target, filepath.Base(target))
		return nil
	}
	c.File(target)
	return nil
}

// linkOwnerChat links the owner's bubbles of an assistant's chat: the files of
// every project its absolute paths name, and the coders of its jobs.
func (s *Server) linkOwnerChat(views []render.AssistantMessageView, owner string) {
	linkChatViews(views, s.pathLinker().linkedText)
	linkChatViews(views, newCoderLinker(s.runningJobCoders(owner), "").linkedText)
}

// runningJobCoders answers the coders of an assistant's jobs that run right
// now, each once, in the order of its jobs.
func (s *Server) runningJobCoders(owner string) []string {
	jobs := s.watcher.ListOf(owner)
	if len(jobs) == 0 {
		return nil
	}
	running := s.terminalSessions()
	var out []string
	for _, job := range jobs {
		if ref, ok := running[job.Terminal]; ok && ref.Kind == "coder" && !slices.Contains(out, job.Terminal) {
			out = append(out, job.Terminal)
		}
	}
	return out
}

// coderLinker links the coders an assistant's chat names to their pages
// under base. The chat names a coder by the id the cockpit printed for it,
// whole or as its short id, so a word that is one of them is the mention. A
// short id two coders share names neither.
type coderLinker struct{ hrefs map[string]string }

// newCoderLinker answers the linker of the coders ids names.
func newCoderLinker(ids []string, base string) coderLinker {
	l := coderLinker{hrefs: map[string]string{}}
	shared := map[string]bool{}
	for _, id := range ids {
		href := base + "/coders/" + url.PathEscape(id)
		l.hrefs[id] = href
		short := coder.ShortID(id)
		if other, ok := l.hrefs[short]; ok && other != href {
			shared[short] = true
		}
		l.hrefs[short] = href
	}
	for short := range shared {
		delete(l.hrefs, short)
	}
	return l
}

// linkedText escapes text with an anchor around every mention of a coder.
func (l coderLinker) linkedText(text string) string {
	var b strings.Builder
	at := 0
	if len(l.hrefs) > 0 {
		for start, token := range filesystem.Tokens(text) {
			word := strings.TrimRight(token, ".,:;!?")
			href, ok := l.hrefs[word]
			if !ok {
				continue
			}
			b.WriteString(template.HTMLEscapeString(text[at:start]))
			b.WriteString(`<a href="` + template.HTMLEscapeString(href) + `">` + template.HTMLEscapeString(word) + "</a>")
			at = start + len(word)
		}
	}
	b.WriteString(template.HTMLEscapeString(text[at:]))
	return b.String()
}
