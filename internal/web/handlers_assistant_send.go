package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/filesystem"
)

// uploadEnvelope is what a multipart request costs on top of the file itself:
// boundaries, headers, the field names. One megabyte is far more than that and
// keeps the two caps from disagreeing at the edge.
const uploadEnvelope = 1 << 20

// maxUploadBytes bounds one attached file. There is only one number for this,
// the request cap, so raising it raises both: a file the browser accepts is a
// request the server accepts. The envelope keeps the generic body limit from
// firing first, which would replace the friendly per file message with the
// blunt one.
func (s *Server) maxUploadBytes() int64 {
	limit := s.cfg.MaxRequestBodySize - uploadEnvelope
	if limit < 0 {
		return 0
	}
	return limit
}

// assistantWriteGuard refuses a turn in an assistant whose composer is off.
// The page already hides the input, this is the same rule for a tab that has
// been open since before the coder went missing.
func (s *Server) assistantWriteGuard(id string) error {
	current, err := s.assistants.Get(id)
	if err != nil {
		return err
	}
	if reason := s.assistantBlockedReason(current); reason != "" {
		return errors.New(reason)
	}
	return nil
}

func (s *Server) assistantSend(c *gin.Context, id string) {
	if err := s.assistantWriteGuard(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	attachments, err := s.assistantAttachments(id, c.PostFormArray("attachment"))
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	run, err := s.assistants.Send(id, c.PostForm("message"), attachments)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	// The message took the draft with it, so the other devices empty their
	// composer instead of holding words that are already in the transcript.
	s.publishDraft(id)
	s.assistantRunResponse(c, id, run)
}

// assistantDraft stores the unsent composer. It goes through the same write
// guard as a message: a blocked assistant renders no composer, so nothing may
// write into its draft either. A file that is gone drops out of the draft
// instead of failing the save, the words are what the user came back for.
func (s *Server) assistantDraft(c *gin.Context, id string) {
	if err := s.assistantWriteGuard(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	names := c.PostFormArray("attachment")
	attachments := make([]assistant.Attachment, 0, len(names))
	for _, name := range names {
		one, err := s.assistantAttachments(id, []string{name})
		if err != nil || len(one) == 0 {
			continue
		}
		attachments = append(attachments, one[0])
	}
	draft, changed, err := s.assistants.SaveDraft(id, c.PostForm("message"), attachments)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	if changed {
		s.publishDraft(id)
	}
	c.JSON(http.StatusOK, gin.H{"saved": true, "updatedAt": draft.UpdatedAt})
}

// publishDraft tells the other devices that this assistant's draft moved.
// The event carries the assistant and nothing else: every page pulls the
// draft itself, the way the tab strip pulls its fragment, so a client applies
// what the server holds instead of what an event once carried.
func (s *Server) publishDraft(instanceID string) {
	s.bus.Publish(eventbus.Event{Type: "draft", Data: map[string]string{"assistant": instanceID}})
}

// handleAssistantDraft serves the stored draft for a device catching up: after
// a save somewhere else, and after the event stream reconnected. The composer
// reads its words and files, an open block its own entry of the answers.
func (s *Server) handleAssistantDraft(c *gin.Context) {
	current, err := s.assistants.Get(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	draft, err := s.assistants.Draft(current.ID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"text":      draft.Text,
		"files":     s.assistantDraftViews(current.ID, draft),
		"updatedAt": draft.UpdatedAt,
		"answers":   draft.Answers,
	})
}

// assistantAttachments resolves the names the composer uploaded before it sent
// the message. Only files that really sit in this conversation's directory are
// accepted, so a crafted post cannot attach an arbitrary host file.
func (s *Server) assistantAttachments(id string, names []string) ([]assistant.Attachment, error) {
	if len(names) == 0 {
		return nil, nil
	}
	dir, err := s.assistants.UploadDir(id)
	if err != nil {
		return nil, err
	}
	out := make([]assistant.Attachment, 0, len(names))
	for _, raw := range names {
		file, err := filesystem.OpenFile(dir, raw)
		if err != nil {
			return nil, errors.New("An attached file is no longer available.")
		}
		_ = file.Close()
		out = append(out, assistant.Attachment{
			Name:  file.Name,
			Path:  file.Path,
			Media: assistant.MediaKind(file.Name),
			Size:  file.Size,
		})
	}
	return out, nil
}

func (s *Server) assistantRetry(c *gin.Context, id string) {
	if err := s.assistantWriteGuard(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	run, err := s.assistants.Retry(id)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	s.assistantRunResponse(c, id, run)
}

func (s *Server) assistantRunResponse(c *gin.Context, id string, run assistant.Run) {
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"runId": run.RunID, "messageId": run.MessageID, "userMessageId": run.UserMessageID, "replacedId": run.ReplacedID, "title": run.Title, "queued": run.Queued, "since": activity.Mark(run.StartedAt)})
		return
	}
	c.Redirect(http.StatusSeeOther, "/assistants/"+id)
}

// assistantDiscard takes back one message that is still waiting in the queue.
// The service decides under its own lock whether the message still waits, so a
// discard racing the flush is answered instead of dropped.
func (s *Server) assistantDiscard(c *gin.Context, id string) {
	if err := s.assistantWriteGuard(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	if err := s.assistants.Discard(id, strings.TrimSpace(c.PostForm("message_id"))); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"discarded": true})
		return
	}
	c.Redirect(http.StatusSeeOther, "/assistants/"+id)
}

func (s *Server) assistantCancel(c *gin.Context, id string) {
	if err := s.assistants.Cancel(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
		return
	}
	c.Redirect(http.StatusSeeOther, "/assistants/"+id)
}

// handleAssistantUpload takes the files of the next message. They are stored
// before the message is sent, so the composer can show them and the coder gets
// a real path to open.
func (s *Server) handleAssistantUpload(c *gin.Context) {
	id := c.Param("id")
	if _, err := s.assistants.Get(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	dir, err := s.assistants.UploadDir(id)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	form, err := c.MultipartForm()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "The upload could not be read."})
		return
	}
	files := form.File["file"]
	if len(files) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Pick a file first."})
		return
	}
	saved := make([]gin.H, 0, len(files))
	for _, header := range files {
		if header.Size > s.maxUploadBytes() {
			c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "That file is too large."})
			return
		}
		src, err := header.Open()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "The upload could not be read."})
			return
		}
		attachment, err := s.workspace.SaveUpload(dir, header.Filename, src)
		_ = src.Close()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
			return
		}
		file := gin.H{
			"name":  attachment.Name,
			"media": attachment.Media,
			"size":  attachment.Size,
			"url":   s.assistantMediaURL(id, attachment.Path),
		}
		if width, height := assistantImageSize(attachment.Media, attachment.Path); width > 0 {
			file["width"], file["height"] = width, height
		}
		saved = append(saved, file)
	}
	c.JSON(http.StatusOK, gin.H{"files": saved})
}
