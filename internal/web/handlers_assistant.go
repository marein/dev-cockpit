package web

import (
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/web/render"
)

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

// pullHeader marks a page pulling its own address again to catch up with the
// server. The render is the same, what differs is what it means: nobody
// navigated, so nothing that follows a person's steps is written.
const pullHeader = "X-DC-Pull"

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
	// the plus button offers what this host actually uses. The page pulls
	// itself again on every assistant event, in background windows too
	// (syncFromServer, it says so on pullHeader), and a pull is not a look:
	// a window left open on one assistant must not keep it the one looked at
	// last while the user reads another.
	if c.GetHeader(pullHeader) == "" {
		s.assistantRecent.Touch(current.ID)
	}
	data := s.assistantData(current, c.Query("all") != "")
	data.Name = strings.TrimSpace(current.Title)
	title := "Assistant"
	if data.Name != "" {
		title = data.Name + " - Assistant"
	}
	data.Page = s.page(c, title, "assistants")
	data.Ctx = s.assistantCtxData(c, data.Path, "assistant-ctx-new")
	data.Triggers = s.assistantTriggersData(render.Page{CSRFToken: s.csrfToken(c)}, current.ID)
	// The one number on the button that opens the aside: both kinds of standing
	// work, because the aside holds both and the button is the only thing a
	// phone sees of it.
	data.WatchingOpen = data.JobsOpen + data.Triggers.Open
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
			Running:    activity.Mark(s.assistants.RunningSince(entry.ID)),
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
	s.linkOwnerChat(messages, current.ID)
	jobs, olderJobs := s.assistantJobViews(current.ID)
	// Every assistant has a stream of its own, however many of them are open at
	// once.
	streamURL := base + "/stream"
	// An empty stt URL is how the page knows the talk button has no engine
	// behind it; the route itself refuses a stale page as the backstop.
	sttURL := ""
	if !s.voiceSTTOff() {
		sttURL = base + "/stt"
	}
	return render.AssistantData{
		ID:              current.ID,
		Path:            base,
		CoderID:         current.CoderID,
		CoderLabel:      render.CoderLabel(current.CoderID),
		Coders:          s.assistantCoderOptions(),
		Messages:        messages,
		EarlierCount:    earlier,
		AllURL:          allURL,
		Running:         activity.Mark(s.assistants.RunningSince(current.ID)),
		Blocked:         blocked,
		NewCoderID:      s.assistantNewCoderID(current),
		Jobs:            jobs,
		JobsOpen:        openJobs(jobs),
		JobsOlder:       olderJobs,
		JobsURL:         assistantJobsPath,
		JobsListURL:     assistantJobsPath + "?assistant=" + url.QueryEscape(current.ID),
		TriggersListURL: assistantTriggersPath + "?assistant=" + url.QueryEscape(current.ID),
		TriggersNewURL:  assistantTriggersPath + "?form=new&assistant=" + url.QueryEscape(current.ID),
		StreamURL:       streamURL,
		PostURL:         base,
		MessageURL:      base + "/messages/",
		UploadURL:       base + "/user-upload",
		SttURL:          sttURL,
		TTS:             !s.voiceTTSOff(),
		MaxPromptBytes:  assistant.MaxPromptBytes,
		MaxUploadBytes:  s.maxUploadBytes(),
		Draft:           draft.Text,
		DraftFiles:      s.assistantDraftFiles(current.ID, draft),
		DraftURL:        base + "/draft",
		ContextPercent:  assistantContextPercent(current),
		Models:          s.assistantModelsData(current),
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
// into this state: the coder it runs on is not installed any more.
func (s *Server) assistantBlockedReason(current assistant.Instance) string {
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

// handleAssistantAction is the one POST route of a conversation. It dispatches
// on the hidden form field, so every form posts to the path that renders it.
func (s *Server) handleAssistantAction(c *gin.Context) {
	id := c.Param("id")
	switch strings.TrimSpace(c.PostForm("form")) {
	case "message":
		s.assistantSend(c, id)
	case "answers":
		s.assistantAnswer(c, id)
	case "retry":
		s.assistantRetry(c, id)
	case "cancel":
		s.assistantCancel(c, id)
	case "discard":
		s.assistantDiscard(c, id)
	case "draft":
		s.assistantDraft(c, id)
	case "new":
		// The create forms of an assistant's page post to that page's own
		// path, the one that rendered them; the empty state's post to
		// /assistants/new, its own pair below.
		s.assistantNew(c, c.PostForm("coder"))
	case "rename":
		s.assistantRename(c, id)
	case "model":
		s.assistantModels(c, id)
	case "delete":
		s.assistantDelete(c, id)
	default:
		s.renderError(c, http.StatusBadRequest, "Unknown action", "That action isn't available on this page.")
	}
}

// handleAssistantNew is the POST half of /assistants/new, where the empty
// state's create form posts. The GET half is the area's entry: the create is
// a control on the pages of the area and no page of its own, so the address a
// login redirect or a backlink lands on is the area itself. A page of one
// assistant renders the same forms against its own path, see
// handleAssistantAction.
func (s *Server) handleAssistantNew(c *gin.Context) {
	s.assistantNew(c, c.PostForm("coder"))
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
		s.redirectWithFlash(c, "/assistants", "", err.Error())
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

// assistantDelete removes one assistant for good, on the user's word, by
// another assistant or by itself. A self delete stops the turn that asked,
// like any delete stops a running turn.
//
// Its jobs go with it. They can never be checked again once their owner is
// gone, so the coders they steered are the user's again, and the answer names
// them: a coder that quietly stopped being watched is the one ending nobody
// hears. Which ones those are is read before the delete, because the entries
// go with the directory, and nothing about them is changed before the delete
// went through: a delete that fails keeps the assistant with its jobs
// steering, the answer says kept and means it.
//
// Its approvals are declined, nobody is left to hear how they would end, and
// its running compose runs become the user's, so their end rings the project
// like a run the user started instead of vanishing with the thread it was for.
func (s *Server) assistantDelete(c *gin.Context, id string) {
	// Only the browser deletes as the user. A local call is an assistant's,
	// and it waits for the user's approval where the Assistant delete approval
	// asks, the way a project delete does.
	if s.localCall(c) {
		owner, err := s.assistantCaller(c)
		if err != nil {
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
			return
		}
		kind := s.assistantDeleteKind()
		target, err := kind.Resolve(id)
		if err != nil {
			s.assistantActionError(c, id, err)
			return
		}
		if s.askApproval(c, deletesApproval(owner, kind, []deleteTarget{target})) {
			return
		}
	}
	name := s.assistantName(id)
	held, err := s.deleteAssistant(id)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	handedBack := assistant.ReleasedNames(held)
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

// deleteAssistant removes an assistant for good, for the page and for an
// approved delete alike, and answers the jobs it held, whose coders it hands
// back to the user. Every link stops opening it first, so no guest reads it
// while it goes, and a delete that fails after that keeps it off them.
func (s *Server) deleteAssistant(id string) ([]assistant.Job, error) {
	s.unshareAssistant(id)
	held := s.watcher.OpenJobs(id)
	if err := s.assistants.Delete(id); err != nil {
		return nil, err
	}
	s.watcher.Dropped(id, held)
	s.approvals.DeclineOwner(id)
	s.docker.Disown(id)
	s.notifier.MarkTargetRead(id)
	return held, nil
}

// assistantDeleteKind is the assistant delete an assistant asks for. A
// target's delete looks the assistant up again. An assistant that deleted
// itself reads no note, its thread is gone with it.
func (s *Server) assistantDeleteKind() deleteKind {
	return deleteKind{ID: approvalAssistantDelete, Plural: "assistants", Resolve: func(id string) (deleteTarget, error) {
		if _, err := s.assistants.Get(id); err != nil {
			return deleteTarget{}, err
		}
		name := s.assistantName(id)
		return deleteTarget{
			Key:     id,
			Name:    "assistant " + name,
			Details: []askpass.Detail{{Label: "Assistant", Value: withShortID(name, id)}},
			Delete: func(bool) (string, []string, error) {
				if _, err := s.assistants.Get(id); err != nil {
					return "", nil, errors.New("the assistant no longer exists")
				}
				held, err := s.deleteAssistant(id)
				if err != nil {
					return "", nil, err
				}
				return assistant.AssistantDeleted(name, held), nil, nil
			},
		}, nil
	}}
}

// assistantActionError answers a refused action.
func (s *Server) assistantActionError(c *gin.Context, id string, err error) {
	if wantsJSON(c.Request) {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	s.redirectWithFlash(c, "/assistants/"+id, "", err.Error())
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
			"model":         entry.Model,
			"checkModel":    entry.CheckModel,
			"triggerModel":  entry.TriggerModel,
			"effort":        entry.Effort,
			"checkEffort":   entry.CheckEffort,
			"triggerEffort": entry.TriggerEffort,
			"lastMessageAt": entry.LastMessageAt,
			"preview":       entry.Preview,
			"openJobs":      open[entry.ID],
		})
	}
	c.JSON(http.StatusOK, gin.H{"assistants": out})
}

// handleAssistantInstanceRead serves one transcript as JSON for the
// `assistant-show` command, windowed and cut the way the activity route cuts a
// coder's record: entries picks the window, full lifts the per message cut, and
// contains narrows to the messages carrying a word before the window is taken,
// the way the instances route narrows the list. Any assistant may read any
// other's, which is the point of it. Reads only, it marks nothing read.
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
	instance, dropped, err := s.assistants.Transcript(c.Param("id"), c.Query("contains"), entries, budget)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		return
	}
	messages := make([]gin.H, 0, len(instance.Messages))
	for _, m := range instance.Messages {
		entry := gin.H{
			"role":      string(m.Role),
			"content":   m.Content,
			"createdAt": m.CreatedAt,
		}
		// The headline is the line the thread shows over a note and over an
		// answer a trigger pushed, and it is searched, so it travels: a
		// filtered reading whose hit fell in a headline has to show what it
		// hit.
		for _, note := range []*assistant.Note{m.Note, m.Origin} {
			if note != nil && strings.TrimSpace(note.Headline) != "" {
				entry["headline"] = note.Headline
				break
			}
		}
		messages = append(messages, entry)
	}
	c.JSON(http.StatusOK, gin.H{
		"id":            instance.ID,
		"title":         instance.Title,
		"coderId":       instance.CoderID,
		"model":         instance.Model,
		"checkModel":    instance.CheckModel,
		"triggerModel":  instance.TriggerModel,
		"effort":        instance.Effort,
		"checkEffort":   instance.CheckEffort,
		"triggerEffort": instance.TriggerEffort,
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

// PublishConversations announces a coarse change of the conversation list.
// Individual answer deltas never travel this bus, they belong to the
// conversation's own stream.
func (s *Server) PublishConversations() {
	s.bus.Publish(eventbus.Event{Type: "assistant", Data: map[string]string{}})
}
