package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/local/dev-cockpit/internal/eventbus"
	"html/template"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/assistant"
	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/markdown"
	"github.com/local/dev-cockpit/internal/web/render"
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

// currentAssistantID names the conversation the entry points link to, without
// creating one. The layout renders it on every page, and a page view must
// never leave a trail of empty conversations behind.
func (s *Server) currentAssistantID() string {
	current, ok := s.conversations.Current()
	if !ok {
		return ""
	}
	return current.ID
}

// assistantEntryState is what every entry point to the assistant renders: the
// conversation it opens and whether that one has unread news. The tab strip
// fragment builds its own page data, so this lives next to the id lookup
// instead of inside page().
func (s *Server) assistantEntryState() (string, bool) {
	id := s.currentAssistantID()
	if id == "" {
		return "", false
	}
	return id, s.notifier.UnreadTargets()[id]
}

// handleAssistantPanel serves the interior of the assistant overlay as a
// fragment: the chat by default, or one of the overlay's own views. The
// overlay is the assistant's only surface and its own world, so its jobs,
// memory and history render inside it instead of opening a modal or
// navigating the page behind it. Opening the chat opens the live conversation
// and starts one when there is none; naming a conversation shows that one,
// read-only when it is history.
//
// The fetch marks nothing read. An open panel syncs on every assistant event,
// in background windows too, so a read here would clear the news before the
// push dispatcher re-checks unread and nothing would ever toast or push.
// Reading is the client's decision, it posts a read only for a surface that
// is visible in a focused window.
func (s *Server) handleAssistantPanel(c *gin.Context) {
	var current assistant.Conversation
	var err error
	if id := strings.TrimSpace(c.Query("conversation")); id != "" {
		current, err = s.conversations.Get(id)
	} else {
		current, err = s.conversations.Open("")
	}
	if err != nil {
		c.String(http.StatusServiceUnavailable, err.Error())
		return
	}
	data := s.assistantData(current, c.Query("all") != "", true)
	data.Page = render.Page{CSRFToken: s.csrfToken(c)}
	switch c.Query("view") {
	case "jobs":
		data.View = "jobs"
	case "memory":
		data.View = "memory"
		data.MemoryData = s.assistantMemoryData(c)
	case "history":
		data.View = "history"
		history := s.assistantHistoryData(c)
		data.HistoryData = &history
	default:
		data.View = "chat"
	}
	c.HTML(http.StatusOK, "assistant_panel_content.gohtml", data)
}

// assistantData builds the surface model both hosts render.
func (s *Server) assistantData(current assistant.Conversation, all, panel bool) render.AssistantData {
	blocked := s.assistantBlockedReason(current)
	base := "/assistant/" + current.ID
	messages, earlier, allURL := assistantWindow(s.assistantMessageViews(current, blocked != ""), base, all)
	jobs, olderJobs := s.assistantJobViews()
	// A transcript that is not the live conversation never receives frames
	// (wake reports land in the live one), so it gets no stream URL and the
	// surface never opens an idle stream for it.
	currentURL := ""
	streamURL := base + "/stream"
	if id := s.currentAssistantID(); id != "" && id != current.ID {
		currentURL = "/assistant/" + id
		streamURL = ""
	}
	return render.AssistantData{
		ID:             current.ID,
		Panel:          panel,
		CoderID:        current.CoderID,
		CoderLabel:     render.CoderLabel(current.CoderID),
		Coders:         s.assistantCoderOptions(),
		Messages:       messages,
		EarlierCount:   earlier,
		AllURL:         allURL,
		Running:        s.conversations.Running(current.ID),
		Blocked:        blocked,
		NewCoderID:     s.assistantNewCoderID(current),
		CurrentURL:     currentURL,
		Jobs:           jobs,
		JobsOpen:       openJobs(jobs),
		JobsOlder:      olderJobs,
		JobsURL:        assistantJobsPath,
		StreamURL:      streamURL,
		PostURL:        base,
		MessageURL:     base + "/messages/",
		UploadURL:      base + "/user-upload",
		MaxPromptBytes: assistant.MaxPromptBytes,
		MaxUploadBytes: s.maxUploadBytes(),
		MemoryCount:    len(s.assistant.Memory()),
		HistoryCount:   len(s.conversations.List()),
		Draft:          current.Draft.Text,
		DraftFiles:     s.assistantDraftFiles(current),
		DraftURL:       base + "/draft",
		ContextPercent: assistantContextPercent(current),
	}
}

// assistantContextPercent is how full this conversation's context window stood
// after its last turn. A conversation that never got a reading, and a model
// whose window nobody knows, both come out as zero, which the ring draws as
// empty. The coder travels with the lookup: the same model does not have the
// same window under every CLI, and a reading whose window was unknown when it
// was taken resolves here as soon as the table knows it.
func assistantContextPercent(current assistant.Conversation) int {
	if current.Context == nil {
		return 0
	}
	return current.Context.PercentIn(current.CoderID)
}

// assistantDraftViews renders the draft's attachments the way the composer
// stores them, so the chips come back with the text they were attached to. A
// file that no longer resolves is left out instead of turning into a chip that
// points nowhere.
func (s *Server) assistantDraftViews(current assistant.Conversation) []gin.H {
	files := make([]gin.H, 0, len(current.Draft.Attachments))
	for _, a := range current.Draft.Attachments {
		url := s.assistantMediaURL(current.ID, a.Path)
		if url == "" {
			continue
		}
		files = append(files, gin.H{"name": a.Name, "media": a.Media, "size": a.Size, "url": url})
	}
	return files
}

// assistantDraftFiles is the same list as the attribute the page renders, so
// the composer starts from the draft without a request of its own.
func (s *Server) assistantDraftFiles(current assistant.Conversation) string {
	files := s.assistantDraftViews(current)
	if len(files) == 0 {
		return ""
	}
	out, err := json.Marshal(files)
	if err != nil {
		return ""
	}
	return string(out)
}

// assistantWindowSize is how many messages a conversation page renders before
// it starts holding the older ones back. A long conversation is mostly read
// from its end, and rendering a hundred answers turns every visit into a scroll
// through history nobody asked for.
const assistantWindowSize = 20

// assistantWindow cuts the transcript down to its last messages and returns the
// link that brings the rest back. The link carries the oldest message that
// stays on screen as its fragment, so opening the whole transcript lands on the
// message the reader was looking at instead of at either end.
func assistantWindow(views []render.AssistantMessageView, base string, all bool) ([]render.AssistantMessageView, int, string) {
	if all || len(views) <= assistantWindowSize {
		return views, 0, ""
	}
	earlier := len(views) - assistantWindowSize
	views = views[earlier:]
	return views, earlier, base + "?all=1#message-" + views[0].ID
}

func (s *Server) assistantCoderOptions() []render.AssistantCoderOption {
	coders := s.conversations.Coders()
	out := make([]render.AssistantCoderOption, 0, len(coders))
	for _, co := range coders {
		out = append(out, render.AssistantCoderOption{ID: co.ID, Label: render.CoderLabel(co.ID)})
	}
	return out
}

// assistantBlockedReason explains why the composer is off. Nothing here hides
// the transcript, a blocked conversation stays fully readable and only loses
// its input.
func (s *Server) assistantBlockedReason(current assistant.Conversation) string {
	// One conversation is live at a time. Every earlier one is a record of what
	// was said, which is what makes a new conversation the honest way to start a
	// new context instead of reviving one whose session is gone.
	switch current.Status {
	case assistant.StatusTransferred:
		return "This conversation moved to a coder terminal, so it is read-only here."
	case assistant.StatusArchived:
		return "This is an earlier conversation, kept as it was."
	}
	if !s.assistantCoderInstalled(current.CoderID) {
		return "The coder of this conversation is not available right now, so it is read-only."
	}
	return ""
}

// assistantNewCoderID is the coder the new conversation button starts on: the
// one this conversation ran on, as long as it is still there.
func (s *Server) assistantNewCoderID(current assistant.Conversation) string {
	if s.assistantCoderInstalled(current.CoderID) {
		return current.CoderID
	}
	return ""
}

func (s *Server) assistantCoderInstalled(coderID string) bool {
	for _, co := range s.conversations.Coders() {
		if co.ID == coderID {
			return true
		}
	}
	return false
}

// assistantJobsPath is where the steered jobs live: the list, the two actions
// on them, and the path `dev-cockpit assistant coder-steer` posts to. A job belongs to
// the assistant, not to one conversation, so it has no conversation in its URL.
const assistantJobsPath = "/assistant/jobs"

// handleAssistantJobs serves the job list on its own, so a check that changed a
// job reaches the open page without a reload.
func (s *Server) handleAssistantJobs(c *gin.Context) {
	jobs, older := s.assistantJobViews()
	c.HTML(http.StatusOK, "assistant_jobs_list.gohtml", render.AssistantData{
		Jobs:      jobs,
		JobsOpen:  openJobs(jobs),
		JobsOlder: older,
		JobsURL:   assistantJobsPath,
		Page:      render.Page{CSRFToken: s.csrfToken(c)},
	})
}

// handleAssistantJobsAction is the POST side of that path. It dispatches on the
// hidden form field like every other form of the cockpit, and serves the page's
// buttons and the assistant's own commands through the same handlers.
func (s *Server) handleAssistantJobsAction(c *gin.Context) {
	switch strings.TrimSpace(c.PostForm("form")) {
	case "steer":
		s.assistantSteer(c)
	case "release":
		s.assistantRelease(c)
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}

// openJobs is how many jobs still wake the assistant, which is what the button
// on the page carries without opening the list.
func openJobs(jobs []render.AssistantJobView) int {
	count := 0
	for _, job := range jobs {
		if job.Open {
			count++
		}
	}
	return count
}

// closedJobsShown bounds the closed tail of the job list, the same number the
// `job-list` command uses: the jobs are the assistant's now and a host collects
// them for weeks, while a closed job is only history. What is dropped is shown
// as a count, never silently.
const closedJobsShown = 5

// assistantJobViews are the coders the assistant steers, open jobs first, the
// closed tail capped. Returns the views and how many closed jobs were dropped.
func (s *Server) assistantJobViews() ([]render.AssistantJobView, int) {
	jobs := s.watcher.List()
	out := make([]render.AssistantJobView, 0, len(jobs))
	closed := 0
	for _, w := range jobs {
		// The list is sorted open first and newest first, so everything past
		// the cap is the oldest closed history.
		if !w.State.Open() {
			closed++
			if closed > closedJobsShown {
				continue
			}
		}
		out = append(out, render.AssistantJobView{
			Terminal: w.Terminal,
			Name:     w.Name,
			Project:  w.Project,
			Task:     w.Task,
			DoneWhen: w.DoneWhen,
			State:    string(w.State),
			Open:     w.State.Open(),
			Checking: w.Checking(),
			Note:     w.Note,
			Wakes:    w.Wakes,
			MaxWakes: w.MaxWakes,
			Expires:  machineTime(w.ExpiresAt),
			URL:      "/coders/" + w.Terminal,
		})
	}
	older := closed - closedJobsShown
	if older < 0 {
		older = 0
	}
	return out, older
}

// assistantWakeView describes where a message came from when a check wrote it.
// The report carries the name of the job it was written for, so the page says
// which job reported, not an id. A report from before the note carried a name
// falls back to the store, which is right for as long as that job is the one
// standing on the terminal.
func (s *Server) assistantWakeView(note *assistant.WakeNote) *render.AssistantWakeView {
	if note == nil {
		return nil
	}
	view := &render.AssistantWakeView{
		Terminal: note.Terminal,
		Name:     note.Terminal,
		Verdict:  note.Verdict,
		Done:     note.Verdict == string(assistant.VerdictDone),
		Blocked:  note.Verdict == string(assistant.VerdictBlocked),
		Expired:  note.Verdict == string(assistant.VerdictExpired),
		URL:      "/coders/" + note.Terminal,
	}
	if note.Name != "" {
		view.Name = note.Name
	} else if job, ok := s.watcher.Get(note.Terminal); ok && job.Name != "" {
		view.Name = job.Name
	}
	return view
}

// assistantMessageViews renders the transcript. A blocked conversation takes
// no new turn, so it offers no retry and lets no waiting message be removed.
func (s *Server) assistantMessageViews(current assistant.Conversation, blocked bool) []render.AssistantMessageView {
	out := make([]render.AssistantMessageView, 0, len(current.Messages))
	for i, m := range current.Messages {
		last := i == len(current.Messages)-1 && !blocked
		out = append(out, s.assistantMessageView(current.ID, m, last, !blocked, render.CoderLabel(current.CoderID)))
	}
	return out
}

func (s *Server) assistantMessageView(conversationID string, m assistant.Message, retryable, writable bool, coder string) render.AssistantMessageView {
	view := render.AssistantMessageView{
		ID:         m.ID,
		RunID:      m.RunID,
		User:       m.Role == assistant.RoleUser,
		Wake:       s.assistantWakeView(m.Wake),
		Author:     coder,
		Text:       m.Content,
		State:      string(m.State),
		Error:      m.Error,
		Streaming:  m.State == assistant.StateStreaming,
		Failed:     m.State == assistant.StateFailed || m.State == assistant.StateInterrupted,
		CanRetry:   retryable && m.Role == assistant.RoleAssistant && m.State.Retryable(),
		Queued:     m.State == assistant.StateQueued,
		CanDiscard: writable && m.State == assistant.StateQueued,
		Time:       machineTime(m.CreatedAt),
	}
	for _, a := range m.Attachments {
		view.Attachments = append(view.Attachments, render.AssistantAttachmentView{
			Name:     a.Name,
			URL:      s.assistantMediaURL(conversationID, a.Path),
			Media:    a.Media,
			SizeText: filesystem.HumanSize(a.Size),
		})
	}
	if view.User {
		view.Author = "You"
		view.HTML = plainTextHTML(m.Content)
	} else if m.Content != "" {
		view.HTML = s.assistantMarkdown(conversationID, m.Content)
	}
	return view
}

// assistantMediaURL turns an absolute workspace path into the URL that serves
// it. A path outside the workspace yields an empty URL, so nothing the coder
// wrote into the transcript can point the browser somewhere else.
func (s *Server) assistantMediaURL(conversationID, absolute string) string {
	rel, err := filepath.Rel(s.assistant.Workspace(), absolute)
	if err != nil || strings.HasPrefix(rel, "..") {
		return ""
	}
	return "/assistant/" + conversationID + "/media/" + filepath.ToSlash(rel)
}

// assistantMarkdown renders an answer. A relative path in it is resolved
// against the assistant workspace and embedded, so a picture, a recording or a
// clip the assistant points at plays in the answer instead of being a dead
// link. Raw HTML stays disabled in the renderer.
func (s *Server) assistantMarkdown(conversationID, src string) template.HTML {
	html, err := markdown.RenderGFMWithMedia(src, func(destination string) (markdown.Media, bool) {
		rel := strings.TrimSpace(destination)
		if rel == "" || strings.Contains(rel, "://") || strings.HasPrefix(rel, "/") || strings.HasPrefix(rel, "#") {
			return markdown.Media{}, false
		}
		if _, err := s.assistant.ResolveWorkspaceFile(rel); err != nil {
			return markdown.Media{}, false
		}
		return markdown.Media{
			URL:  "/assistant/" + conversationID + "/media/" + path.Clean(filepath.ToSlash(rel)),
			Kind: assistant.MediaKind(rel),
		}, true
	})
	if err != nil {
		return ""
	}
	return template.HTML(html)
}

// handleAssistantMessage serves one rendered message, pulled by the browser
// when a streamed answer finished.
func (s *Server) handleAssistantMessage(c *gin.Context) {
	current, err := s.conversations.Get(c.Param("id"))
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
		c.HTML(http.StatusOK, "assistant_message.gohtml", render.AssistantMessageData{
			Message: s.assistantMessageView(current.ID, m, last, !blocked, render.CoderLabel(current.CoderID)),
		})
		return
	}
	c.String(http.StatusNotFound, "Message not found.")
}

// handleAssistantAction is the one POST route of a conversation. It dispatches
// on the hidden form field, so every form posts to the path that renders it.
func (s *Server) handleAssistantAction(c *gin.Context) {
	id := c.Param("id")
	switch strings.TrimSpace(c.PostForm("form")) {
	case "message":
		s.assistantSend(c, id)
	case "retry":
		s.assistantRetry(c, id)
	case "cancel":
		s.assistantCancel(c, id)
	case "discard":
		s.assistantDiscard(c, id)
	case "draft":
		s.assistantDraft(c, id)
	case "new":
		s.assistantNew(c, c.PostForm("coder"))
	case "delete":
		s.assistantDelete(c, id)
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}

// assistantWriteGuard refuses a turn in a conversation whose composer is off.
// The page already hides the input, this is the same rule for a tab that has
// been open since before the conversation became history.
func (s *Server) assistantWriteGuard(id string) error {
	current, err := s.conversations.Get(id)
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
	run, err := s.conversations.Send(id, c.PostForm("message"), attachments)
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
// guard as a message: an archived conversation renders no composer, so nothing
// may write into its draft either. A file that is gone drops out of the draft
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
	draft, changed, err := s.conversations.SaveDraft(id, c.PostForm("message"), attachments)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	if changed {
		s.publishDraft(id)
	}
	c.JSON(http.StatusOK, gin.H{"saved": true, "updatedAt": draft.UpdatedAt})
}

// publishDraft tells the other devices that this conversation's draft moved.
// The event carries the conversation and nothing else: every page pulls the
// draft itself, the way the tab strip pulls its fragment, so a client applies
// what the server holds instead of what an event once carried.
func (s *Server) publishDraft(conversationID string) {
	s.bus.Publish(eventbus.Event{Type: "draft", Data: map[string]string{"conversation": conversationID}})
}

// handleAssistantDraft serves the stored draft for a device catching up: after
// a save somewhere else, and after the event stream reconnected.
func (s *Server) handleAssistantDraft(c *gin.Context) {
	current, err := s.conversations.Get(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"text":      current.Draft.Text,
		"files":     s.assistantDraftViews(current),
		"updatedAt": current.Draft.UpdatedAt,
	})
}

// assistantAttachments resolves the names the composer uploaded before it sent
// the message. Only files that really sit in this conversation's directory are
// accepted, so a crafted post cannot attach an arbitrary host file.
func (s *Server) assistantAttachments(id string, names []string) ([]assistant.Attachment, error) {
	if len(names) == 0 {
		return nil, nil
	}
	dir, err := s.conversations.UploadDir(id)
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
	run, err := s.conversations.Retry(id)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	s.assistantRunResponse(c, id, run)
}

func (s *Server) assistantRunResponse(c *gin.Context, id string, run assistant.Run) {
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"runId": run.RunID, "messageId": run.MessageID, "userMessageId": run.UserMessageID, "replacedId": run.ReplacedID, "title": run.Title, "queued": run.Queued})
		return
	}
	c.Redirect(http.StatusSeeOther, "/projects?assistant="+id)
}

// assistantDiscard takes back one message that is still waiting in the queue.
// The service decides under its own lock whether the message still waits, so a
// discard racing the flush is answered instead of dropped.
func (s *Server) assistantDiscard(c *gin.Context, id string) {
	if err := s.assistantWriteGuard(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	if err := s.conversations.Discard(id, strings.TrimSpace(c.PostForm("message_id"))); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"discarded": true})
		return
	}
	c.Redirect(http.StatusSeeOther, "/projects?assistant="+id)
}

func (s *Server) assistantCancel(c *gin.Context, id string) {
	if err := s.conversations.Cancel(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"status": "cancelled"})
		return
	}
	c.Redirect(http.StatusSeeOther, "/projects?assistant="+id)
}

// assistantNew starts a fresh conversation. The memory carries what matters
// across, so a new conversation is the cheap way out of a long one.
func (s *Server) assistantNew(c *gin.Context, coderID string) {
	created, err := s.conversations.Open(strings.TrimSpace(coderID))
	if err != nil {
		if wantsJSON(c.Request) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"id": created.ID})
		return
	}
	c.Redirect(http.StatusSeeOther, "/projects?assistant="+created.ID)
}

// assistantSteer starts steering a terminal. Both callers come through here:
// the page's button and the assistant's own `dev-cockpit assistant coder-steer`.
func (s *Server) assistantSteer(c *gin.Context) {
	target, err := s.assistantSteerTarget(c.PostForm("terminal"))
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	// The page may leave the criterion empty, the check then judges against
	// the session's own task. The assistant's own command may not: it is the
	// one caller that can write a checkable criterion, so the requirement
	// stays at its door, decided by the surface like every ownership question.
	if s.localCall(c) {
		if _, err := assistant.ValidateDoneWhen(c.PostForm("done_when")); err != nil {
			s.assistantJobError(c, err)
			return
		}
	}
	job, err := s.watcher.Steer(assistant.Job{
		Terminal: target.Identifier,
		Name:     target.Name,
		Project:  target.Project,
		CoderID:  target.CoderID,
		Task:     c.PostForm("task"),
		DoneWhen: c.PostForm("done_when"),
		Source:   s.turnSource(c),
	})
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	// A task over its bound was stored cut, and the caller has to hear that.
	// The notice comes from the rule Add applied, so every path says the same
	// sentence.
	_, taskNotice := assistant.TruncateTask(c.PostForm("task"))
	if wantsJSON(c.Request) {
		answer := gin.H{
			"terminal":  job.Terminal,
			"name":      job.Name,
			"maxWakes":  job.MaxWakes,
			"expiresAt": job.ExpiresAt,
		}
		if taskNotice != "" {
			answer["notice"] = taskNotice
		}
		c.JSON(http.StatusOK, answer)
		return
	}
	flash := "Steering " + job.Name + "."
	if taskNotice != "" {
		flash = "Steering " + job.Name + ", " + taskNotice + "."
	}
	s.redirectWithFlash(c, "/projects", flash, "")
}

// assistantRelease calls a job off, from the page's Stop button or from
// `dev-cockpit assistant coder-release`.
func (s *Server) assistantRelease(c *gin.Context) {
	terminal := strings.TrimSpace(c.PostForm("terminal"))
	if err := s.watcher.Release(terminal); err != nil {
		s.assistantJobError(c, err)
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"terminal": terminal, "state": string(assistant.JobStopped)})
		return
	}
	s.redirectWithFlash(c, "/projects", "The coder is released.", "")
}

// assistantJobError answers a refused job action. The page's job list posts
// with fetch and shows the sentence in a toast, the command line prints it.
func (s *Server) assistantJobError(c *gin.Context, err error) {
	if wantsJSON(c.Request) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.redirectWithFlash(c, "/projects", "", err.Error())
}

// steerTarget is the running coder a job steers, resolved to what the job
// stores about it.
type steerTarget struct {
	Identifier string
	Name       string
	Project    string
	CoderID    string
}

// assistantSteerTarget resolves the terminal a job steers. Only a running
// coder can be steered: a shell produces no report worth a turn, and a coder
// that is not running has nothing to say yet.
func (s *Server) assistantSteerTarget(raw string) (steerTarget, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return steerTarget{}, errors.New("A job needs the terminal it steers.")
	}
	for _, m := range s.coders {
		for _, running := range m.Snapshot().Running {
			if running.Identifier != id {
				continue
			}
			return steerTarget{
				Identifier: running.Identifier,
				Name:       running.Name,
				Project:    s.projects.ProjectNameFor(running.CWD),
				CoderID:    m.ID(),
			}, nil
		}
	}
	return steerTarget{}, fmt.Errorf("No running coder with id %q.", id)
}

func (s *Server) assistantDelete(c *gin.Context, id string) {
	if err := s.conversations.Delete(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	s.notifier.MarkTargetRead(id)
	// The jobs stay: they belong to the assistant, and the next conversation is
	// where their reports arrive.
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"deleted": true})
		return
	}
	s.redirectWithFlash(c, "/projects", "Conversation deleted.", "")
}

func (s *Server) assistantActionError(c *gin.Context, id string, err error) {
	status := http.StatusBadRequest
	if errors.Is(err, assistant.ErrBusy) {
		status = http.StatusTooManyRequests
	}
	if wantsJSON(c.Request) {
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	s.redirectWithFlash(c, "/projects?assistant="+id, "", err.Error())
}

// handleAssistantUpload takes the files of the next message. They are stored
// before the message is sent, so the composer can show them and the coder gets
// a real path to open.
func (s *Server) handleAssistantUpload(c *gin.Context) {
	id := c.Param("id")
	if _, err := s.conversations.Get(id); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	dir, err := s.conversations.UploadDir(id)
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
		attachment, err := s.assistant.SaveUpload(dir, header.Filename, src)
		_ = src.Close()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": userFacingError(c, err)})
			return
		}
		saved = append(saved, gin.H{
			"name":  attachment.Name,
			"media": attachment.Media,
			"size":  attachment.Size,
			"url":   s.assistantMediaURL(id, attachment.Path),
		})
	}
	c.JSON(http.StatusOK, gin.H{"files": saved})
}

// handleAssistantMedia serves a file out of the assistant workspace. It goes
// through http.ServeContent, so a video seeks with range requests instead of
// downloading from the start.
func (s *Server) handleAssistantMedia(c *gin.Context) {
	if _, err := s.conversations.Get(c.Param("id")); err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
	target, err := s.assistant.ResolveWorkspaceFile(strings.TrimPrefix(c.Param("path"), "/"))
	if err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if c.Query("download") == "1" {
		c.FileAttachment(target, filepath.Base(target))
		return
	}
	c.File(target)
}

// handleAssistantStream is the conversation's own SSE channel. Answer text
// never travels the app wide event stream.
func (s *Server) handleAssistantStream(c *gin.Context) {
	id := c.Param("id")
	if _, err := s.conversations.Get(id); err != nil {
		c.String(http.StatusNotFound, err.Error())
		return
	}
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

	snapshot, running, events, unsubscribe := s.conversations.Subscribe(id)
	defer unsubscribe()
	if running {
		if err := writeConversationEvent(w, snapshot); err != nil {
			return
		}
	}

	heartbeat := time.NewTicker(s.cfg.StreamHeartbeatInterval)
	defer heartbeat.Stop()
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			if err := writeConversationEvent(w, ev); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := writeSSEKeepalive(w); err != nil {
				return
			}
		}
	}
}

// handleAssistantHistory serves the history list body: the overlay's history
// view fetches it, and the live refresh swaps it, so a conversation that
// finishes elsewhere shows up without a reload.
func (s *Server) handleAssistantHistory(c *gin.Context) {
	c.HTML(http.StatusOK, "assistant_history_content.gohtml", s.assistantHistoryData(c))
}

func (s *Server) assistantHistoryData(c *gin.Context) render.AssistantHistoryData {
	summaries := s.conversations.List()
	currentID := s.currentAssistantID()
	cards := make([]render.AssistantCard, 0, len(summaries))
	for _, entry := range summaries {
		cards = append(cards, render.AssistantCard{
			ID:         entry.ID,
			Title:      entry.Title,
			Preview:    entry.Preview,
			CoderLabel: render.CoderLabel(entry.CoderID),
			URL:        "/assistant/" + entry.ID,
			Messages:   entry.MessageCount,
			Unfinished: entry.Unfinished,
			Current:    entry.ID == currentID,
			Updated:    machineTime(entry.LastMessageAt),
		})
	}
	currentURL := ""
	if currentID != "" {
		currentURL = "/assistant/" + currentID
	}
	return render.AssistantHistoryData{
		Page:          render.Page{CSRFToken: s.csrfToken(c)},
		Conversations: cards,
		Available:     len(s.conversations.Coders()) > 0,
		CurrentURL:    currentURL,
	}
}

// handleAssistantConversations serves the conversation index as JSON, newest
// first, for the assistant's own `conversation-list` command. A contains word
// narrows the list to the conversations that carry it in the title or in a
// message; the match reads the transcripts, so it lives in the service. The
// overlay's history view has a fragment of its own, this route only reports.
func (s *Server) handleAssistantConversations(c *gin.Context) {
	entries := s.conversations.Search(c.Query("contains"))
	out := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		out = append(out, gin.H{
			"id":            entry.ID,
			"title":         entry.Title,
			"coderId":       entry.CoderID,
			"lastMessageAt": entry.LastMessageAt,
			"preview":       entry.Preview,
		})
	}
	c.JSON(http.StatusOK, gin.H{"conversations": out})
}

// handleAssistantConversationRead serves one transcript as JSON for the
// assistant's `conversation-show` command, windowed and cut the way the activity
// route cuts a coder's record: entries picks the window, full lifts the per
// message cut. Reads only, it marks nothing read.
func (s *Server) handleAssistantConversationRead(c *gin.Context) {
	entries, err := strconv.Atoi(c.DefaultQuery("entries", "0"))
	if err != nil || entries < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "entries has to be a number."})
		return
	}
	budget := assistant.TranscriptMessageRunes
	if full, _ := strconv.ParseBool(c.DefaultQuery("full", "false")); full {
		budget = 0
	}
	conversation, dropped, err := s.conversations.Transcript(c.Param("id"), entries, budget)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	messages := make([]gin.H, 0, len(conversation.Messages))
	for _, m := range conversation.Messages {
		messages = append(messages, gin.H{
			"role":      string(m.Role),
			"content":   m.Content,
			"createdAt": m.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"id":            conversation.ID,
		"title":         conversation.Title,
		"coderId":       conversation.CoderID,
		"lastMessageAt": conversation.LastMessageAt,
		"messageCount":  conversation.MessageCount,
		"dropped":       dropped,
		"messages":      messages,
	})
}

// handleAssistantMemory serves the memory list body: what the assistant knows
// about the user, each row with its own prefilled form, so the memory is never
// a black box the user cannot correct in place. The overlay's memory view
// fetches it and a deletion refreshes it in place.
func (s *Server) handleAssistantMemory(c *gin.Context) {
	c.HTML(http.StatusOK, "assistant_memory_content.gohtml", *s.assistantMemoryData(c))
}

func (s *Server) assistantMemoryData(c *gin.Context) *render.AssistantMemoryData {
	data := &render.AssistantMemoryData{Page: render.Page{CSRFToken: s.csrfToken(c)}}
	for _, entry := range s.assistant.Memory() {
		data.Entries = append(data.Entries, render.AssistantMemoryEntry{
			Slug:    entry.Slug,
			Title:   entry.Title,
			Body:    entry.Body,
			Updated: machineTime(entry.Updated),
		})
	}
	return data
}

func (s *Server) handleAssistantMemorySave(c *gin.Context) {
	switch strings.TrimSpace(c.PostForm("form")) {
	case "delete":
		if err := s.assistant.DeleteMemory(strings.TrimSpace(c.PostForm("slug"))); err != nil {
			s.redirectWithFlash(c, "/projects?assistant=memory", "", err.Error())
			return
		}
		s.redirectWithFlash(c, "/projects?assistant=memory", "Memory deleted.", "")
	case "save":
		if _, err := s.assistant.SaveMemory(
			strings.TrimSpace(c.PostForm("slug")),
			c.PostForm("title"),
			c.PostForm("body"),
		); err != nil {
			s.redirectWithFlash(c, "/projects?assistant=memory", "", err.Error())
			return
		}
		s.redirectWithFlash(c, "/projects?assistant=memory", "Memory saved.", "")
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}

// plainTextHTML renders a user message: the text is escaped and only its line
// breaks become markup, so a prompt is always shown literally.
func plainTextHTML(text string) template.HTML {
	return template.HTML(strings.ReplaceAll(template.HTMLEscapeString(text), "\n", "<br>"))
}

// machineTime formats a timestamp for the dc-time element, which renders it in
// the browser locale.
func machineTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
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

// PublishConversations announces a coarse change of the conversation list.
// Individual answer deltas never travel this bus, they belong to the
// conversation's own stream.
func (s *Server) PublishConversations() {
	s.bus.Publish(eventbus.Event{Type: "assistant", Data: map[string]string{}})
}
