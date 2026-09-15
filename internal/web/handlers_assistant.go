package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/markdown"
	"github.com/marein/dev-cockpit/internal/web/render"
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

// assistantLastCoderID is the coder a new assistant starts on where the page
// has no open one to read it off: the one the assistant opened last runs on.
// The area remembers which that was (assistantRecent), so the plus button of a
// host with several coders installed offers the one actually in use instead of
// whichever the list happens to return first. An empty answer means "whichever
// is installed", which is what a fresh install gets.
func (s *Server) assistantLastCoderID() string {
	byID := map[string]string{}
	for _, entry := range s.assistants.List() {
		byID[entry.ID] = entry.CoderID
	}
	for _, id := range s.assistantRecent.Names() {
		if coder, ok := byID[id]; ok && s.assistantCoderInstalled(coder) {
			return coder
		}
	}
	return ""
}

// assistantNews is whether any assistant has an answer nobody has read. The
// mark the entry points carry is one for the whole area, like the terminals'
// (`[data-notify-any]`): news in one assistant is news whichever one an entry
// would open, and which one it is in is what the list column's rows say.
func (s *Server) assistantNews() bool {
	unread := s.notifier.UnreadTargets()
	for _, entry := range s.assistants.List() {
		if unread[entry.ID] {
			return true
		}
	}
	return false
}

// handleAssistantsEntry answers the area's own address, the one the rail and
// the tab bar link. Which assistant it opens is decided here and never in a
// rendered link, the way handleTerminalsEntry decides for the terminals: the
// assistant last looked at, else the first one in the order the list is sorted
// into, so a phone picked up later opens what the desktop was in. The list
// column stands beside it and marks that row, here and in the phone's sheet.
// Only with no assistant at all does the area open on itself, the empty state
// whose one control makes the first one. Like the terminals entry the answer is
// a See Other with no-store, never a permanent redirect, or the browser would
// keep reopening the assistant of the first click.
func (s *Server) handleAssistantsEntry(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if id := s.assistantEntryTarget(); id != "" {
		c.Redirect(http.StatusSeeOther, "/assistants/"+id)
		return
	}
	data := render.AssistantData{
		Page:       s.page(c, "Assistants", "assistants"),
		Path:       "/assistants",
		Coders:     s.assistantCoderOptions(),
		NewCoderID: s.assistantLastCoderID(),
		JobsURL:    assistantJobsPath,
		PostURL:    "/assistants/new",
	}
	data.Ctx = s.assistantCtxData(c, data.Path, "assistant-ctx-new")
	c.HTML(http.StatusOK, "assistant_page.gohtml", data)
}

// assistantEntryTarget names the assistant the area's address opens: the one
// looked at last that is still there, else the first row of the list, which is
// where the hand sorted order puts it. Empty means there is none at all.
func (s *Server) assistantEntryTarget() string {
	list := s.assistants.List()
	if len(list) == 0 {
		return ""
	}
	for _, id := range s.assistantRecent.Names() {
		for _, entry := range list {
			if entry.ID == id {
				return id
			}
		}
	}
	return list[0].ID
}

// handleAssistantPage renders one assistant, beside the list column of all of
// them, which marks the row of the one that is open.
//
// The render marks nothing read. The page pulls itself again on every
// assistant event, in background windows too, so a read here would clear the
// news before the push dispatcher re-checks unread and nothing would ever
// toast or push. Reading is the client's decision, it posts a read only for
// a surface that is visible in a focused window.
func (s *Server) handleAssistantPage(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	current, err := s.assistants.Get(id)
	if err != nil {
		s.renderError(c, http.StatusNotFound, "Assistant not found", "There is no such assistant.")
		return
	}
	// Which coder a new assistant starts on follows the one opened last, so
	// the plus button offers what this host actually uses.
	s.assistantRecent.Touch(current.ID)
	data := s.assistantData(current, c.Query("all") != "")
	data.Name = strings.TrimSpace(current.Title)
	title := "Assistant"
	if data.Name != "" {
		title = data.Name + " - Assistant"
	}
	data.Page = s.page(c, title, "assistants")
	data.Ctx = s.assistantCtxData(c, data.Path, "assistant-ctx-new")
	c.HTML(http.StatusOK, "assistant_page.gohtml", data)
}

// assistantCtxData builds the list column for the page at path: the page's
// column and the phone's sheet render the same model, prefix tells their
// posting forms apart.
//
// It is one flat list of every assistant, in the order the rows were dragged
// into. There is no current one and no earlier ones any more: they all live,
// they all take messages, and what separates them is what each one is called
// and what it holds, which is what the rows say.
func (s *Server) assistantCtxData(c *gin.Context, path, prefix string) *render.AssistantCtxData {
	clean := path
	if i := strings.IndexByte(clean, '?'); i >= 0 {
		clean = clean[:i]
	}
	activeID := strings.TrimPrefix(clean, "/assistants/")
	if activeID == clean {
		activeID = ""
	}
	return &render.AssistantCtxData{
		Page:       render.Page{CSRFToken: s.csrfToken(c)},
		Path:       clean,
		ActiveID:   activeID,
		Assistants: s.assistantCards(),
		Available:  len(s.assistants.Coders()) > 0,
		Coders:     s.assistantCoderOptions(),
		IDPrefix:   prefix,
		PostURL:    "/assistants/new",
	}
}

// assistantCards are the rows of that list: what each assistant is called, how
// much it holds, how many coders it steers right now and whether it has unread
// news. The open job count is what makes the list answer "who holds what" at a
// glance, which is the question several assistants create.
func (s *Server) assistantCards() []render.AssistantCard {
	summaries := s.assistants.List()
	unread := s.notifier.UnreadTargets()
	open := map[string]int{}
	for _, job := range s.watcher.List() {
		if job.State.Open() {
			open[job.Owner]++
		}
	}
	cards := make([]render.AssistantCard, 0, len(summaries))
	for _, entry := range summaries {
		cards = append(cards, render.AssistantCard{
			ID:         entry.ID,
			Title:      entry.Title,
			CoderLabel: render.CoderLabel(entry.CoderID),
			URL:        "/assistants/" + entry.ID,
			Messages:   entry.MessageCount,
			Running:    entry.Running,
			Unfinished: entry.Unfinished,
			OpenJobs:   open[entry.ID],
			News:       unread[entry.ID],
			Updated:    machineTime(entry.LastMessageAt),
		})
	}
	return cards
}

// assistantData builds the model one assistant's page renders.
func (s *Server) assistantData(current assistant.Instance, all bool) render.AssistantData {
	blocked := s.assistantBlockedReason(current)
	draft, _ := s.assistants.Draft(current.ID)
	base := "/assistants/" + current.ID
	messages, earlier, allURL := assistantWindow(s.assistantMessageViews(current, blocked != ""), base, all)
	jobs, olderJobs := s.assistantJobViews(current.ID)
	// A transferred assistant never receives frames again, so it gets no stream
	// URL and the surface never opens an idle stream for it. Every other one
	// does, however many of them are open at once: each has a stream of its own.
	streamURL := base + "/stream"
	if current.Status != assistant.StatusActive {
		streamURL = ""
	}
	// An empty stt URL is how the page knows the talk button has no engine
	// behind it; the route itself refuses a stale page as the backstop.
	sttURL := ""
	if !s.voiceSTTOff() {
		sttURL = base + "/stt"
	}
	return render.AssistantData{
		ID:             current.ID,
		Path:           base,
		CoderID:        current.CoderID,
		CoderLabel:     render.CoderLabel(current.CoderID),
		Coders:         s.assistantCoderOptions(),
		Messages:       messages,
		EarlierCount:   earlier,
		AllURL:         allURL,
		Running:        s.assistants.Running(current.ID),
		Blocked:        blocked,
		NewCoderID:     s.assistantNewCoderID(current),
		Jobs:           jobs,
		JobsOpen:       openJobs(jobs),
		JobsOlder:      olderJobs,
		JobsURL:        assistantJobsPath,
		JobsListURL:    assistantJobsPath + "?assistant=" + url.QueryEscape(current.ID),
		StreamURL:      streamURL,
		PostURL:        base,
		MessageURL:     base + "/messages/",
		UploadURL:      base + "/user-upload",
		SttURL:         sttURL,
		TTS:            !s.voiceTTSOff(),
		MaxPromptBytes: assistant.MaxPromptBytes,
		MaxUploadBytes: s.maxUploadBytes(),
		Draft:          draft.Text,
		DraftFiles:     s.assistantDraftFiles(current.ID, draft),
		DraftURL:       base + "/draft",
		ContextPercent: assistantContextPercent(current),
	}
}

// assistantContextPercent is how full this assistant's context window stood
// after its last turn. A conversation that never got a reading, and a model
// whose window nobody knows, both come out as zero, which the ring draws as
// empty. The coder travels with the lookup: the same model does not have the
// same window under every CLI, and a reading whose window was unknown when it
// was taken resolves here as soon as the table knows it.
func assistantContextPercent(current assistant.Instance) int {
	if current.Context == nil {
		return 0
	}
	return current.Context.PercentIn(current.CoderID)
}

// assistantDraftViews renders the draft's attachments the way the composer
// stores them, so the chips come back with the text they were attached to. A
// file that no longer resolves is left out instead of turning into a chip that
// points nowhere.
func (s *Server) assistantDraftViews(instanceID string, draft assistant.Draft) []gin.H {
	files := make([]gin.H, 0, len(draft.Attachments))
	for _, a := range draft.Attachments {
		path := s.attachmentPath(instanceID, a)
		url := s.assistantMediaURL(instanceID, path)
		if url == "" {
			continue
		}
		file := gin.H{"name": a.Name, "media": a.Media, "size": a.Size, "url": url}
		if width, height := assistantImageSize(a.Media, path); width > 0 {
			file["width"], file["height"] = width, height
		}
		files = append(files, file)
	}
	return files
}

// assistantDraftFiles is the same list as the attribute the page renders, so
// the composer starts from the draft without a request of its own.
func (s *Server) assistantDraftFiles(instanceID string, draft assistant.Draft) string {
	files := s.assistantDraftViews(instanceID, draft)
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
	coders := s.assistants.Coders()
	out := make([]render.AssistantCoderOption, 0, len(coders))
	for _, co := range coders {
		out = append(out, render.AssistantCoderOption{ID: co.ID, Label: render.CoderLabel(co.ID)})
	}
	return out
}

// assistantBlockedReason explains why the composer is off. Nothing here hides
// the transcript, a blocked assistant stays fully readable and only loses its
// input. An assistant lives until it is deleted, so there is exactly one way
// into this state that is not a missing coder: it was handed to a terminal.
func (s *Server) assistantBlockedReason(current assistant.Instance) string {
	if current.Status == assistant.StatusTransferred {
		return "This assistant moved to a coder terminal, so it is read-only here."
	}
	if !s.assistantCoderInstalled(current.CoderID) {
		return "The coder of this assistant is not available right now, so it is read-only."
	}
	return ""
}

// assistantNewCoderID is the coder the new assistant button starts on: the one
// this assistant runs on, as long as it is still there.
func (s *Server) assistantNewCoderID(current assistant.Instance) string {
	if s.assistantCoderInstalled(current.CoderID) {
		return current.CoderID
	}
	return ""
}

func (s *Server) assistantCoderInstalled(coderID string) bool {
	for _, co := range s.assistants.Coders() {
		if co.ID == coderID {
			return true
		}
	}
	return false
}

// assistantJobsPath is where the steered jobs live: the list, the two actions
// on them, and the path `dev-cockpit assistant coder-steer` posts to. One path
// for every assistant's jobs, because a terminal carries at most one job and
// who may steer it is a question about all of them at once. Which assistant a
// job belongs to travels in the request and in the answer, never in the URL.
const assistantJobsPath = "/assistants/jobs"

// handleAssistantJobs serves the job list on its own, so a check that changed a
// job reaches the open page without a reload. The `assistant` query narrows it
// to one assistant's jobs, which is what its own page asks for.
func (s *Server) handleAssistantJobs(c *gin.Context) {
	jobs, older := s.assistantJobViews(strings.TrimSpace(c.Query("assistant")))
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

// assistantJobViews are the coders an assistant steers, open jobs first, the
// closed tail capped. An empty owner is every assistant's jobs, which is what a
// surface that is not one assistant's page shows. Returns the views and how
// many closed jobs were dropped.
func (s *Server) assistantJobViews(owner string) ([]render.AssistantJobView, int) {
	jobs := s.watcher.List()
	if owner != "" {
		jobs = s.watcher.ListOf(owner)
	}
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
		view := render.AssistantJobView{
			Terminal: w.Terminal,
			OwnerID:  w.Owner,
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
		}
		if w.Project != "" {
			view.EditorURL = "/projects/" + url.PathEscape(w.Project) + "/editor"
		}
		// Only a list that spans several assistants says whose job this is. On
		// one assistant's own page the answer is always the same and the line
		// would be noise on every row.
		if owner == "" {
			view.Owner = s.assistantName(w.Owner)
		}
		out = append(out, view)
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

// assistantMessageViews renders the transcript. A blocked assistant takes no
// new turn, so it offers no retry and lets no waiting message be removed.
func (s *Server) assistantMessageViews(current assistant.Instance, blocked bool) []render.AssistantMessageView {
	out := make([]render.AssistantMessageView, 0, len(current.Messages))
	for i, m := range current.Messages {
		last := i == len(current.Messages)-1 && !blocked
		out = append(out, s.assistantMessageView(current.ID, m, last, !blocked, render.CoderLabel(current.CoderID)))
	}
	return out
}

func (s *Server) assistantMessageView(instanceID string, m assistant.Message, retryable, writable bool, coder string) render.AssistantMessageView {
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
	if view.User {
		view.Author = "You"
		view.HTML = plainTextHTML(m.Content)
	} else if m.Content != "" {
		view.HTML = s.assistantMarkdown(instanceID, m.Content)
	}
	// The speaker renders only on a finished answer with words in it, and
	// only while text to speech is on; the audio route repeats those checks
	// for a page from before a settings change.
	if !view.User && m.Content != "" && m.State == assistant.StateComplete && !s.voiceTTSOff() {
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
// when a streamed answer finished.
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
	case "rename":
		s.assistantRename(c, id)
	case "delete":
		s.assistantDelete(c, id)
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}

// assistantWriteGuard refuses a turn in an assistant whose composer is off.
// The page already hides the input, this is the same rule for a tab that has
// been open since before the assistant was handed to a terminal.
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
// a save somewhere else, and after the event stream reconnected.
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
		c.JSON(http.StatusOK, gin.H{"runId": run.RunID, "messageId": run.MessageID, "userMessageId": run.UserMessageID, "replacedId": run.ReplacedID, "title": run.Title, "queued": run.Queued})
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

// assistantNew starts another assistant, beside the ones that are already
// there. The memory is shared, so a new one knows what the others know and
// only starts its own thread.
func (s *Server) assistantNew(c *gin.Context, coderID string) {
	created, err := s.assistants.Create(strings.TrimSpace(coderID))
	if err != nil {
		if wantsJSON(c.Request) {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		s.redirectWithFlash(c, "/projects", "", err.Error())
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"id": created.ID, "name": created.Title})
		return
	}
	c.Redirect(http.StatusSeeOther, "/assistants/"+created.ID)
}

// assistantRename gives one assistant a name of its own. With several of them
// the name is how the user tells them apart, so it is not derived from the
// first prompt forever.
func (s *Server) assistantRename(c *gin.Context, id string) {
	if err := s.assistants.Rename(id, c.PostForm("title")); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	if wantsJSON(c.Request) {
		c.JSON(http.StatusOK, gin.H{"renamed": true})
		return
	}
	c.Redirect(http.StatusSeeOther, "/assistants/"+id)
}

// assistantOrderRequest is the list the drag posts, first id at the top.
type assistantOrderRequest struct {
	IDs []string `json:"ids"`
}

// maxAssistantOrderIDs bounds one reorder write, the way the tab strip's does:
// a rogue request cannot make the index file grow by what it posts.
const maxAssistantOrderIDs = 512

// handleAssistantOrder writes the order the list was dragged into. The list is
// the user's to sort, not the clock's, so the order lives in the index file on
// the server and comes back on the next reload, on this device and on every
// other one. A posted subset is folded into the order that stands rather than
// taken as the whole of it, so an assistant created while the drag was in
// flight keeps its place.
func (s *Server) handleAssistantOrder(c *gin.Context) {
	var req assistantOrderRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "Invalid order.")
		return
	}
	if len(req.IDs) > maxAssistantOrderIDs {
		c.String(http.StatusRequestEntityTooLarge, "Too many entries.")
		return
	}
	s.assistants.Reorder(req.IDs)
	// The order changed for everybody, so every open list refreshes itself.
	s.PublishConversations()
	c.Status(http.StatusNoContent)
}

// assistantSteer starts steering a terminal. Both callers come through here:
// the page's button and an assistant's own `dev-cockpit assistant coder-steer`.
//
// A job needs an owner, because its checks wake exactly that assistant and its
// reports land in exactly that thread. An assistant steering names itself by
// being the caller; the user steering from the page says which assistant it is
// for, and the dialog only asks when there is more than one to choose from.
func (s *Server) assistantSteer(c *gin.Context) {
	// Who is steering comes before what is being steered: a caller that cannot
	// be attributed is refused over the call itself, not over the terminal it
	// happened to name, so the sentence it reads is the one it can act on.
	owner, err := s.steerOwner(c)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	target, err := s.assistantSteerTarget(c.PostForm("terminal"))
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	// The page may leave the criterion empty, the check then judges against
	// the session's own task. An assistant's own command may not: it is the
	// one caller that can write a checkable criterion, so the requirement
	// stays at its door, decided by the surface like every ownership question.
	if s.localCall(c) {
		if _, err := assistant.ValidateDoneWhen(c.PostForm("done_when")); err != nil {
			s.assistantJobError(c, err)
			return
		}
	}
	job, err := s.watcher.Steer(assistant.Job{
		Owner:    owner,
		Terminal: target.Identifier,
		Name:     target.Name,
		Project:  target.Project,
		CoderID:  target.CoderID,
		Task:     c.PostForm("task"),
		DoneWhen: c.PostForm("done_when"),
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

// steerOwner is the assistant a new job belongs to. A turn steering is itself,
// and it has to be able to say which one it is: a call over the socket that
// names none is refused rather than charged to whoever the user last looked at.
// The user steering picks one, and the dialog only asks where there is a
// choice: with a single assistant the form carries none and the only one there
// is takes the job. With several and no pick the steer is refused rather than
// charged to one of them, and with none there is no job at all: a check has to
// be able to wake somebody, and a job nothing wakes is the promise this feature
// exists to keep.
func (s *Server) steerOwner(c *gin.Context) (string, error) {
	if s.localCall(c) {
		return s.assistantCaller(c)
	}
	if asked := strings.TrimSpace(c.PostForm("assistant")); asked != "" {
		if _, err := s.assistants.Get(asked); err != nil {
			return "", errors.New("That assistant does not exist any more.")
		}
		return asked, nil
	}
	switch live := s.assistants.List(); len(live) {
	case 0:
		return "", errors.New("There is no assistant to steer this coder. Start one first.")
	case 1:
		return live[0].ID, nil
	}
	return "", errors.New("Say which assistant this job reports to.")
}

// assistantRelease calls a job off, from the page's Stop button or from
// `dev-cockpit assistant coder-release`. The user may call any job off, an
// assistant only its own: taking somebody else's coder away is the user's
// decision, and the refusal names who holds it.
func (s *Server) assistantRelease(c *gin.Context) {
	terminal := strings.TrimSpace(c.PostForm("terminal"))
	// Who is calling decides what they may call off. A browser is the user and
	// may call off any job; a call over the socket is a turn and may call off
	// only its own, so one that cannot say which assistant it is gets no say at
	// all instead of the user's.
	by := ""
	if s.localCall(c) {
		from, err := s.assistantCaller(c)
		if err != nil {
			s.assistantJobError(c, err)
			return
		}
		by = from
	}
	if err := s.watcher.Release(terminal, by); err != nil {
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

// assistantDelete removes one assistant for good. An assistant cannot delete
// itself: it would be deleting the transcript the answer it is writing goes
// into, and there would be nobody left to tell the user what happened. The user
// deletes it, or another assistant they asked to.
//
// Its jobs go first. They can never be checked again once their owner is gone,
// so the coders they steered are handed back to the user, and the answer names
// them: a coder that quietly stopped being watched is the one ending nobody
// hears.
func (s *Server) assistantDelete(c *gin.Context, id string) {
	if from := s.callingAssistant(c); from == id {
		s.assistantActionError(c, id, errors.New(assistant.SelfDeleteRefusal))
		return
	}
	name := s.assistantName(id)
	released := s.watcher.ReleaseAll(id)
	if err := s.assistants.Delete(id); err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	s.notifier.MarkTargetRead(id)
	handedBack := assistant.ReleasedNames(released)
	if wantsJSON(c.Request) {
		answer := gin.H{"deleted": true}
		if handedBack != "" {
			answer["released"] = handedBack
		}
		c.JSON(http.StatusOK, answer)
		return
	}
	flash := name + " is deleted."
	if handedBack != "" {
		flash += " " + handedBack
	}
	s.redirectWithFlash(c, "/assistants", flash, "")
}

// assistantActionError answers a refused action.
func (s *Server) assistantActionError(c *gin.Context, id string, err error) {
	if wantsJSON(c.Request) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.redirectWithFlash(c, "/assistants/"+id, "", err.Error())
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
	rel := strings.TrimPrefix(c.Param("path"), "/")
	target, err := s.workspace.ResolveWorkspaceFile(id, rel)
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

// assistantPingInterval is how often the conversation stream proves it is
// alive with a frame the browser can see. The keepalive beside it is an SSE
// comment on a much shorter beat, which holds the socket open but tells the
// page nothing, and a visible frame every second would be noise on a phone.
// Same 15 seconds as the ping on /events, and the client judges it the same
// way: silent past 45 seconds means the socket died, which leaves room for one
// missed ping. A variable so a test does not have to wait out the beat.
var assistantPingInterval = 15 * time.Second

// handleAssistantStream is one assistant's own SSE channel. Answer text
// never travels the app wide event stream.
func (s *Server) handleAssistantStream(c *gin.Context) {
	id := c.Param("id")
	if _, err := s.assistants.Get(id); err != nil {
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

	snapshot, running, events, unsubscribe := s.assistants.Subscribe(id)
	defer unsubscribe()
	if running {
		if err := writeConversationEvent(w, snapshot); err != nil {
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
			if err := writeConversationEvent(w, ev); err != nil {
				return
			}
		case <-heartbeat.C:
			if err := writeSSEKeepalive(w); err != nil {
				return
			}
		case <-ping.C:
			if err := writeConversationEvent(w, assistant.StreamEvent{Kind: assistant.FramePing}); err != nil {
				return
			}
		}
	}
}

// handleAssistantInstances serves the index of assistants as JSON, newest
// first, for an assistant's own `assistant-list` command: this is how they see
// each other. A contains word narrows the list to the ones that carry it in the
// name or in a message; the match reads the transcripts, so it lives in the
// service. The list column has a fragment of its own, this route only reports.
func (s *Server) handleAssistantInstances(c *gin.Context) {
	entries := s.assistants.Search(c.Query("contains"))
	open := map[string]int{}
	for _, job := range s.watcher.List() {
		if job.State.Open() {
			open[job.Owner]++
		}
	}
	out := make([]gin.H, 0, len(entries))
	for _, entry := range entries {
		out = append(out, gin.H{
			"id":            entry.ID,
			"title":         entry.Title,
			"coderId":       entry.CoderID,
			"lastMessageAt": entry.LastMessageAt,
			"preview":       entry.Preview,
			"openJobs":      open[entry.ID],
		})
	}
	c.JSON(http.StatusOK, gin.H{"assistants": out})
}

// handleAssistantInstanceRead serves one transcript as JSON for the
// `assistant-show` command, windowed and cut the way the activity route cuts a
// coder's record: entries picks the window, full lifts the per message cut. Any
// assistant may read any other's, which is the point of it. Reads only, it
// marks nothing read.
func (s *Server) handleAssistantInstanceRead(c *gin.Context) {
	entries, err := strconv.Atoi(c.DefaultQuery("entries", "0"))
	if err != nil || entries < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "entries has to be a number."})
		return
	}
	budget := assistant.TranscriptMessageRunes
	if full, _ := strconv.ParseBool(c.DefaultQuery("full", "false")); full {
		budget = 0
	}
	instance, dropped, err := s.assistants.Transcript(c.Param("id"), entries, budget)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	messages := make([]gin.H, 0, len(instance.Messages))
	for _, m := range instance.Messages {
		messages = append(messages, gin.H{
			"role":      string(m.Role),
			"content":   m.Content,
			"createdAt": m.CreatedAt,
		})
	}
	c.JSON(http.StatusOK, gin.H{
		"id":            instance.ID,
		"title":         instance.Title,
		"coderId":       instance.CoderID,
		"lastMessageAt": instance.LastMessageAt,
		"messageCount":  instance.MessageCount,
		"dropped":       dropped,
		"messages":      messages,
	})
}

// handleAssistantMemory serves the memory list body: what the assistant knows
// about the user, each row with its own prefilled form, so the memory is never
// a black box the user cannot correct in place. The overlay's memory view
// fetches it and a deletion refreshes it in place.
func (s *Server) handleAssistantMemory(c *gin.Context) {
	prefix := strings.TrimSpace(c.Query("prefix"))
	if prefix == "" {
		prefix = "memory"
	}
	c.HTML(http.StatusOK, "assistant_memory_content.gohtml", *s.assistantMemoryData(c, prefix))
}

func (s *Server) assistantMemoryData(c *gin.Context, prefix string) *render.AssistantMemoryData {
	data := &render.AssistantMemoryData{Page: render.Page{CSRFToken: s.csrfToken(c)}, Prefix: prefix}
	for _, entry := range s.workspace.Memory() {
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
	done := func(message string, err error) {
		if wantsJSON(c.Request) {
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"saved": true})
			return
		}
		if err != nil {
			s.redirectWithFlash(c, "/assistants", "", err.Error())
			return
		}
		s.redirectWithFlash(c, "/assistants", message, "")
	}
	switch strings.TrimSpace(c.PostForm("form")) {
	case "delete":
		done("Memory deleted.", s.workspace.DeleteMemory(strings.TrimSpace(c.PostForm("slug"))))
	case "save":
		_, err := s.workspace.SaveMemory(
			strings.TrimSpace(c.PostForm("slug")),
			c.PostForm("title"),
			c.PostForm("body"),
		)
		done("Memory saved.", err)
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
