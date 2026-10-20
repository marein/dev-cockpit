package web

import (
	"hash/fnv"
	"html/template"
	"net/http"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/activity"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
	"golang.org/x/net/html"
)

// An assistant on a link is a guest face of the owner's assistant page: its
// whole conversation, read only and live, the files its messages link, its
// jobs and its triggers beside it, and the running coders of its jobs among
// the link's terminals. Nothing of its composer, memory, approvals, drafts,
// model picks, uploads or speech reaches a guest, no job or trigger is made,
// changed or ended from there, and nothing a guest does starts a turn.

func guestAssistantURL(g *guest, id string) string {
	return g.base + "/assistants/" + id
}

// guestAssistant binds an assistant route to an assistant of the link that
// stands, named in the path or, on the lists of jobs and triggers, in the
// `assistant` query like the owner's. Any other answers like an address that
// does not exist.
func (s *Server) guestAssistant(c *gin.Context) {
	g, ok := guestOf(c)
	if !ok {
		s.handleNotFound(c)
		return
	}
	id := c.Param("id")
	if id == "" {
		id = strings.TrimSpace(c.Query("assistant"))
	}
	inst, ok := s.liveShareAssistant(g.share, id)
	if !ok {
		s.handleNotFound(c)
		return
	}
	g.assistant = inst
	c.Next()
}

// shareCoders answers the coders assistants of a link open, by id, each with
// the assistant it is shown through: every running coder one of them steers
// or once steered, unless another assistant steers it now. A coder that
// stopped is none of them, a guest sees only what runs.
func (s *Server) shareCoders(assistants []string) map[string]string {
	out := map[string]string{}
	for _, id := range assistants {
		for _, terminal := range s.runningJobCoders(id) {
			if _, seen := out[terminal]; seen {
				continue
			}
			if now, ok := s.watcher.Get(terminal); ok && now.State.Open() && now.Owner != id {
				continue
			}
			out[terminal] = id
		}
	}
	return out
}

// shareHelpers are the ids of a link's assistants that are not gone, read
// without their transcripts, which a guest's terminal list and every bare
// request of a guest ask for. One gone for certain leaves every link, the way
// liveShareAssistant takes it off.
func (s *Server) shareHelpers(sh share.Share) []string {
	var out []string
	for _, id := range sh.Targets(share.KindAssistant) {
		if s.assistants.Gone(id) {
			s.unshareAssistant(id)
			continue
		}
		out = append(out, id)
	}
	return out
}

// handleShareAssistantsEntry answers a guest's Assistants entry with the
// conversation of the link's first assistant, a See Other nobody may cache.
func (s *Server) handleShareAssistantsEntry(c *gin.Context) {
	g, _ := guestOf(c)
	ids := s.shareHelpers(g.share)
	if len(ids) == 0 {
		s.handleNotFound(c)
		return
	}
	c.Redirect(http.StatusSeeOther, guestAssistantURL(g, ids[0]))
}

// handleShareAssistant renders an assistant's conversation for a guest, from
// the first message on, with its jobs and triggers beside it. The page pulls
// itself again whenever its stream says the thread moved, the two lists pull
// themselves on the assistant signal.
func (s *Server) handleShareAssistant(c *gin.Context) {
	g, _ := guestOf(c)
	inst := g.assistant
	name := strings.TrimSpace(inst.Title)
	title := assistant.Name
	if name != "" {
		title = name + " - " + assistant.Name
	}
	base := guestAssistantURL(g, inst.ID)
	linked := map[string]bool{}
	jobs, older := s.guestJobViews(g, inst.ID)
	data := render.GuestAssistantData{
		Page:            s.page(c, title, "assistants"),
		Name:            name,
		StreamURL:       base + "/stream",
		PageURL:         base,
		Messages:        s.guestChatViews(g, inst, linked),
		Ctx:             s.guestAssistantsCtx(g, "/assistants/"+inst.ID),
		Running:         activity.Mark(s.assistants.RunningSince(inst.ID)),
		Jobs:            jobs,
		JobsOpen:        openJobs(jobs),
		JobsOlder:       older,
		JobsListURL:     g.base + assistantJobsPath + "?assistant=" + url.QueryEscape(inst.ID),
		Triggers:        s.guestTriggersData(g, inst.ID),
		TriggersListURL: g.base + assistantTriggersPath + "?assistant=" + url.QueryEscape(inst.ID),
	}
	data.WatchingOpen = data.JobsOpen + data.Triggers.Open
	s.guestFiles.put(inst.ID, threadPrint(inst), linked)
	c.HTML(http.StatusOK, "guest_assistant.gohtml", data)
}

// handleShareAssistantJobs is the list of an assistant's jobs a guest's page
// pulls, the owner's fragment read only.
func (s *Server) handleShareAssistantJobs(c *gin.Context) {
	g, _ := guestOf(c)
	jobs, older := s.guestJobViews(g, g.assistant.ID)
	c.HTML(http.StatusOK, "assistant_jobs_list.gohtml", render.AssistantData{
		Page:      render.Page{Access: g.access},
		Jobs:      jobs,
		JobsOpen:  openJobs(jobs),
		JobsOlder: older,
	})
}

// handleShareAssistantTriggers is the list of an assistant's triggers a
// guest's page pulls, the owner's fragment read only. The forms the owner's
// path also answers are never a guest's.
func (s *Server) handleShareAssistantTriggers(c *gin.Context) {
	g, _ := guestOf(c)
	c.HTML(http.StatusOK, "assistant_triggers_list.gohtml", s.guestTriggersData(g, g.assistant.ID))
}

// guestJobViews are an assistant's jobs as its guest reads them: the owner's
// rows, the coder and its editor leading inside the share where the link
// opens them and nowhere where it does not.
func (s *Server) guestJobViews(g *guest, id string) ([]render.AssistantJobView, int) {
	jobs, older := s.assistantJobViews(id)
	lead := s.guestLead(g)
	for i := range jobs {
		jobs[i].URL, jobs[i].EditorURL = lead(jobs[i].URL), lead(jobs[i].EditorURL)
	}
	return jobs, older
}

// guestTriggersData are an assistant's triggers as its guest reads them: the
// owner's rows without a way to change one, the coder a row waits for leading
// inside the share where the link shows it.
func (s *Server) guestTriggersData(g *guest, id string) render.AssistantTriggersData {
	data := s.assistantTriggersData(render.Page{Access: g.access}, id)
	data.URL = ""
	lead := s.guestLead(g)
	for i := range data.Triggers {
		data.Triggers[i].EditURL = ""
		data.Triggers[i].TargetURL = lead(data.Triggers[i].TargetURL)
	}
	return data
}

// guestAssistantCards are the rows of the link's assistants that stand, the
// owner's rows in the owner's order with what each is doing, every one
// leading inside the share. The owner's news stays the owner's.
func (s *Server) guestAssistantCards(g *guest) []render.AssistantCard {
	on := s.shareHelpers(g.share)
	if len(on) == 0 {
		return nil
	}
	var cards []render.AssistantCard
	for _, card := range s.assistantCards() {
		if slices.Contains(on, card.ID) {
			card.URL, card.News = guestAssistantURL(g, card.ID), false
			cards = append(cards, card)
		}
	}
	return cards
}

// guestAssistantsCtx is the owner's list column of a guest's assistants for
// the page at path below the link's address, its row marked, without the
// controls that make or keep one: no menu, no grip, no memory and no new
// assistant.
func (s *Server) guestAssistantsCtx(g *guest, path string) *render.AssistantCtxData {
	return &render.AssistantCtxData{
		Page:       render.Page{Access: g.access},
		Path:       path,
		Assistants: s.guestAssistantCards(g),
		ActiveID:   strings.TrimPrefix(path, "/assistants/"),
	}
}

// handleShareAssistantStream is the conversation stream of a guest, the
// owner's stream loop with guestFrame for its frames. It ends when the
// assistant leaves the link, by a removal, a revoke, the expiry or its
// deletion.
func (s *Server) handleShareAssistantStream(c *gin.Context) {
	g, _ := guestOf(c)
	id := g.assistant.ID
	ctx, cancel := s.followShare(c.Request.Context(), g, func(sh share.Share) bool {
		_, ok := s.liveShareAssistant(sh, id)
		return ok
	})
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	s.streamConversation(c, id, guestFrame)
}

// guestFrame is what a guest's stream carries of a conversation frame, chosen
// by kind and by field: the answer being written, with every link into the
// cockpit taken out until the finished message brings the ones the share
// opens, since when the turn runs, the name of a tool at work, the signals
// that the thread moved, and the ping. The picks and any kind the owner's
// stream gains later stay with the owner.
func guestFrame(_ assistant.Instance, ev assistant.StreamEvent) (assistant.StreamEvent, bool) {
	switch ev.Kind {
	case assistant.FrameStart, assistant.FrameDelta, assistant.FrameHTML:
		return assistant.StreamEvent{Kind: ev.Kind, MessageID: ev.MessageID, Text: ev.Text, HTML: string(guestAnchors(template.HTML(ev.HTML), "", nil)), Since: ev.Since}, true
	case assistant.FrameTool:
		return assistant.StreamEvent{Kind: ev.Kind, MessageID: ev.MessageID, Text: ev.Text}, true
	case assistant.FrameEnd, assistant.FrameMessage, assistant.FrameGone, assistant.FramePing:
		return assistant.StreamEvent{Kind: ev.Kind, MessageID: ev.MessageID}, true
	}
	return assistant.StreamEvent{}, false
}

// handleShareAssistantMedia serves a file of a shared conversation: one that
// a message of it links, read inside that assistant's workspace, and nothing
// else of the workspace. A file the assistant wrote may be HTML or SVG, and
// it is served from the cockpit's own origin, so a guest's copy is sandboxed.
func (s *Server) handleShareAssistantMedia(c *gin.Context) {
	g, _ := guestOf(c)
	rel := path.Clean(strings.TrimPrefix(c.Param("path"), "/"))
	if !s.guestLinkedFiles(g, g.assistant)[rel] {
		s.handleNotFound(c)
		return
	}
	if err := s.serveAssistantMedia(c, g.assistant.ID, rel, true); err != nil {
		s.handleNotFound(c)
	}
}

// guestLinkedFiles answers the files a guest's render of the conversation
// links, as paths inside the workspace: the set the page rendered last, as
// long as the thread is the one it rendered, and a render of its own after
// the thread moved. A file is reachable exactly when the page shows it.
func (s *Server) guestLinkedFiles(g *guest, inst assistant.Instance) map[string]bool {
	sum := threadPrint(inst)
	if files, ok := s.guestFiles.get(inst.ID, sum); ok {
		return files
	}
	files := map[string]bool{}
	s.guestChatViews(g, inst, files)
	s.guestFiles.put(inst.ID, sum, files)
	return files
}

// chatFiles remembers, by assistant, the files the last guest render of its
// conversation linked and the print of the thread that render read.
type chatFiles struct {
	mu   sync.Mutex
	sets map[string]chatFileSet
}

type chatFileSet struct {
	sum   uint64
	files map[string]bool
}

func (f *chatFiles) get(id string, sum uint64) (map[string]bool, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	set, ok := f.sets[id]
	return set.files, ok && set.sum == sum
}

func (f *chatFiles) put(id string, sum uint64, files map[string]bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.sets == nil {
		f.sets = map[string]chatFileSet{}
	}
	f.sets[id] = chatFileSet{sum: sum, files: files}
}

func (f *chatFiles) drop(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sets, id)
}

// threadPrint is a hash of what a render of the thread reads of it: every
// message, its text, its state and its files.
func threadPrint(inst assistant.Instance) uint64 {
	h := fnv.New64a()
	for _, m := range inst.Messages {
		for _, part := range []string{m.ID, string(m.Role), string(m.State), m.Content} {
			h.Write([]byte(part))
			h.Write([]byte{0})
		}
		for _, a := range m.Attachments {
			h.Write([]byte(a.Name))
			h.Write([]byte{0})
			h.Write([]byte(a.Path))
			h.Write([]byte{0})
		}
		h.Write([]byte{1})
	}
	return h.Sum64()
}

// guestChatViews renders the whole conversation for a guest, from the first
// message: no retry, no discard and no speaker, the files the messages link
// served under the link's address, and every path of the cockpit a message
// links leading inside the share where the link opens its target and plain
// text where it does not. A project's path links its file there when the
// project is on the link, a coder's id its page when the link shows the
// coder. linked, when given, collects the files as paths inside the
// workspace.
func (s *Server) guestChatViews(g *guest, inst assistant.Instance, linked map[string]bool) []render.AssistantMessageView {
	media := chatMedia{base: guestAssistantURL(g, inst.ID) + "/media/", linked: linked}
	views := make([]render.AssistantMessageView, 0, len(inst.Messages))
	for i, m := range inst.Messages {
		view := s.messageView(inst.ID, m, false, false, inst.CoderID, media)
		view.AudioURL = ""
		answerChoices(&view, inst.Messages, i, "")
		views = append(views, view)
	}
	reach := s.guestReach(g)
	for i := range views {
		views[i].HTML = guestAnchors(views[i].HTML, g.base, reach)
		for j := range views[i].Parts {
			views[i].Parts[j].HTML = guestAnchors(views[i].Parts[j].HTML, g.base, reach)
		}
	}
	on := guestProjectNames(s.guestProjects(g))
	paths := s.pathLinker()
	paths.base = g.base
	paths.allowed = func(name string) bool { return slices.Contains(on, name) }
	linkChatViews(views, paths.linkedText)
	linkChatViews(views, newCoderLinker(s.guestShownCoders(g), g.base).linkedText)
	return views
}

// guestReach leads an owner's address into the share where the guest's link
// opens its target: a coder's page when the link shows the coder, a
// project's editor when the project is on the link. Any other answers false.
func (s *Server) guestReach(g *guest) func(string) (string, bool) {
	on := guestProjectNames(s.guestProjects(g))
	coders := s.guestShownCoders(g)
	return func(href string) (string, bool) {
		u, err := url.Parse(href)
		if err != nil || !cockpitAddress(href) {
			return "", false
		}
		parts := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
		switch {
		case len(parts) == 2 && parts[0] == "coders" && slices.Contains(coders, parts[1]):
			return g.base + "/coders/" + url.PathEscape(parts[1]), true
		case len(parts) == 3 && parts[0] == "projects" && parts[2] == "editor" && slices.Contains(on, parts[1]):
			return g.base + u.RequestURI(), true
		}
		return "", false
	}
}

// guestLead is guestReach for an address a view model carries: where it
// leads inside the share, empty where it leads nowhere.
func (s *Server) guestLead(g *guest) func(string) string {
	reach := s.guestReach(g)
	return func(href string) string {
		to, _ := reach(href)
		return to
	}
}

// guestAnchors takes the cockpit's own links out of rendered HTML for a guest:
// an address under base stays, one reach maps into the share is led there,
// and every other path of the cockpit leaves its text without the link. A
// link that names a host stays as it is, also one naming the cockpit's own,
// which leads a guest to the login.
func guestAnchors(fragment template.HTML, base string, reach func(string) (string, bool)) (out template.HTML) {
	defer recoverLinks(func() { out = template.HTML(template.HTMLEscapeString(string(fragment))) })
	var b strings.Builder
	z := html.NewTokenizer(strings.NewReader(string(fragment)))
	var dropped []bool
	for {
		switch z.Next() {
		case html.ErrorToken:
			return template.HTML(b.String())
		case html.StartTagToken:
			raw := string(z.Raw())
			token := z.Token()
			if token.Data != "a" {
				b.WriteString(raw)
				continue
			}
			keep := true
			for i, attr := range token.Attr {
				if attr.Key != "href" || !cockpitAddress(attr.Val) || (base != "" && strings.HasPrefix(attr.Val, base+"/")) {
					continue
				}
				keep = false
				if reach != nil {
					if to, ok := reach(attr.Val); ok {
						token.Attr[i].Val, keep = to, true
					}
				}
			}
			dropped = append(dropped, !keep)
			if keep {
				b.WriteString(token.String())
			}
			continue
		case html.EndTagToken:
			name, _ := z.TagName()
			if string(name) != "a" || len(dropped) == 0 {
				break
			}
			drop := dropped[len(dropped)-1]
			dropped = dropped[:len(dropped)-1]
			if !drop {
				b.Write(z.Raw())
			}
			continue
		}
		b.Write(z.Raw())
	}
}

// cockpitAddress reports a path from the cockpit's root, an address that
// names no host.
func cockpitAddress(href string) bool {
	href = strings.TrimSpace(href)
	return strings.HasPrefix(href, "/") && !strings.HasPrefix(href, "//") && !strings.HasPrefix(href, `/\`)
}
