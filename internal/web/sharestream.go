package web

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/terminal"
	"github.com/marein/dev-cockpit/internal/tmux"
)

// The long requests of a guest: a terminal's stream, which ends the moment the
// link stops opening the terminal's project or the assistant it is shown
// through, an assistant's conversation stream (shareassistant.go), and the
// app's event stream, which ends the moment the link opens nothing.

// followShare runs check on a guest's link as it stands now, and again after
// every write of the links, every move of the projects and at the link's
// expiry, until check answers false, which ends the context it returns. A
// request bound to a coder through an assistant is checked again on every
// move of the terminals too, which is how a job changes hands. A link that no
// longer stands ends it without a check.
func (s *Server) followShare(parent context.Context, g *guest, check func(share.Share) bool) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(parent)
	moves, unsubscribe := s.bus.Subscribe()
	go func() {
		defer unsubscribe()
		for {
			changed := s.shares.Changed()
			current, ok := s.shares.Current(g.share)
			if !ok || !check(current) {
				cancel()
				return
			}
			if !shareMoved(ctx, changed, moves, current.ExpiresAt, g.through != "") {
				return
			}
		}
	}()
	return ctx, cancel
}

// shareMoved waits for a write of the shares, a move of the projects, with
// jobs one of the terminals, or the expiry, and answers false when the stream
// ended first.
func shareMoved(ctx context.Context, changed <-chan struct{}, moves <-chan eventbus.Event, expires time.Time, jobs bool) bool {
	expiry := time.NewTimer(time.Until(expires))
	defer expiry.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-changed:
			return true
		case <-expiry.C:
			return true
		case ev := <-moves:
			if ev.Type == "projects" || (jobs && ev.Type == "terminals") {
				return true
			}
		}
	}
}

// handleShareStream streams a terminal of the link to a guest. The stream
// loop is the owner's, what it reads is a terminal.View, a viewer that never
// resizes the pane, whatever size the request asks for, and never moves the
// view of the owner's browsers. The theme the request may carry is never read.
// It ends when the link stops opening the terminal, by a removal, a revoke,
// the expiry, the project's or the assistant's end, or another assistant
// taking the coder over.
func (s *Server) handleShareStream(c *gin.Context) {
	g, _ := guestOf(c)
	id := c.Param("id")
	ctx, cancel := s.followShare(c.Request.Context(), g, func(sh share.Share) bool {
		return s.guestStands(g, sh, id)
	})
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	watch := func() (*terminal.View, terminal.Attachment, error) { return s.shells.WatchStream(id) }
	if s.terminalSessions()[id].Kind == "coder" {
		watch = func() (*terminal.View, terminal.Attachment, error) {
			co, _, err := s.resolveRunning(id)
			if err != nil {
				return nil, terminal.Attachment{}, err
			}
			return co.WatchStream(id)
		}
	}
	s.streamTerminal(c, &watchedTerminal{watch: watch}, id)
}

// watchedTerminal is the stream surface of a terminal.View.
type watchedTerminal struct {
	watch func() (*terminal.View, terminal.Attachment, error)
	view  *terminal.View
}

// errWatchedTerminalGone is what a guest reads when the terminal went between
// the check and the watch, without the id or anything tmux said.
var errWatchedTerminalGone = errors.New("The terminal is not running.")

func (t *watchedTerminal) Resolve(string) error { return nil }

func (t *watchedTerminal) AttachStream(string, string, string) (terminal.Attachment, error) {
	view, attached, err := t.watch()
	if err != nil {
		return terminal.Attachment{}, errWatchedTerminalGone
	}
	t.view = view
	return attached, nil
}

func (t *watchedTerminal) DetachStream(string) { t.view.Close() }

func (t *watchedTerminal) RefreshStream(string, int64) (terminal.Attachment, bool) {
	return t.view.Resync()
}

func (t *watchedTerminal) StreamDelta(_ string, offset int64) ([]byte, int64, bool) {
	return t.view.Delta(offset)
}

func (t *watchedTerminal) StreamUpdated(string) (<-chan struct{}, bool) { return t.view.Updated() }

func (t *watchedTerminal) StreamModes(string) (tmux.PaneModes, bool) { return t.view.Modes() }

func (t *watchedTerminal) StreamExited(string) bool { return t.view.Exited() }

func (t *watchedTerminal) Resnapshot(string) (terminal.Attachment, bool) { return t.view.Snapshot() }

// guestEvent answers what a guest's event stream carries of an event: the
// signals that something of the link's projects moved, named with one of them
// or with none, the docker one, and the working set narrowed to the terminals
// shown answers, the ones the link shows. A link with an assistant opens
// coders of any project, so it hears every terminals signal, one named with a
// project off the link bare, without the name, and the assistant signal its
// lists of jobs and triggers pull on, which is bare already. Everything else,
// the notifications, the other projects, the drafts, the questions git asks
// and the costs, stays with the owner, and so does any type added later.
func guestEvent(projects []string, assistants bool, shown func() []string, ev eventbus.Event) (eventbus.Event, bool) {
	switch ev.Type {
	case "docker", "ping":
		return ev, true
	case "activity":
		data, _ := ev.Data.(map[string]any)
		since, _ := data["since"].(map[string]int64)
		working := map[string]int64{}
		if len(since) > 0 {
			for _, id := range shown() {
				if at, ok := since[id]; ok {
					working[id] = at
				}
			}
		}
		return ActivityEvent(working), true
	case "assistant":
		if assistants {
			return eventbus.Event{Type: "assistant", Data: map[string]string{}}, true
		}
	case "terminals", "git", "files", "linecomments", "lsp":
		named := ""
		switch data := ev.Data.(type) {
		case map[string]string:
			named = data["project"]
		case map[string]any:
			named, _ = data["project"].(string)
		}
		if named == "" || slices.Contains(projects, named) {
			return ev, true
		}
		if ev.Type == "terminals" && assistants {
			return eventbus.Event{Type: "terminals"}, true
		}
	}
	return eventbus.Event{}, false
}

// guestRelinked are the signals a guest's pages pull the link's projects and
// terminals again on, sent when a project or an assistant went on the link or
// off it.
var guestRelinked = []eventbus.Event{{Type: "projects"}, {Type: "terminals"}}

// guestLinked is what a guest's event stream knows of its link: the names of
// the projects that stand on it and the ids of the assistants.
type guestLinked struct {
	projects   []string
	assistants []string
}

func (l guestLinked) equal(o guestLinked) bool {
	return slices.Equal(l.projects, o.projects) && slices.Equal(l.assistants, o.assistants)
}

func (s *Server) guestLinked(sh share.Share) guestLinked {
	return guestLinked{projects: guestProjectNames(s.liveShareProjects(sh)), assistants: s.shareHelpers(sh)}
}

// linkShown answers the ids of the terminals the link shows right now.
func (s *Server) linkShown(l guestLinked) []string {
	var ids []string
	for _, t := range s.linkTabs(l.projects, s.shareCoders(l.assistants)) {
		ids = append(ids, t.ID)
	}
	return ids
}

// handleShareEvents is the event stream of a guest's pages, the owner's
// signals narrowed by guestEvent to what the link opens right now. On connect
// it sends the bare signals the guest's surfaces catch up on and the working
// set, the way the owner's stream does, the projects among them, and when the
// link's projects or assistants change it sends the guest's own projects
// signal. The host reading goes out on connect and on the owner's beat, the
// same reading the owner's status line shows.
func (s *Server) handleShareEvents(c *gin.Context) {
	g, _ := guestOf(c)
	on := s.guestLinked(g.share)
	relinked := make(chan guestLinked, 1)
	ctx, cancel := s.followShare(c.Request.Context(), g, func(sh share.Share) bool {
		linked := s.guestLinked(sh)
		select {
		case <-relinked:
		default:
		}
		relinked <- linked
		return len(linked.projects)+len(linked.assistants) > 0
	})
	defer cancel()
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if err := writeSSERetry(w, 2*time.Second); err != nil {
		return
	}
	busEvents, cancelBus := s.bus.Subscribe()
	defer cancelBus()
	shown := func() []string { return s.linkShown(on) }
	if err := writeEnvelope(w, eventbus.Event{Type: "projects"}); err != nil {
		return
	}
	for _, ev := range []eventbus.Event{
		{Type: "terminals"},
		{Type: "docker"},
		{Type: "linecomments", Data: map[string]string{"project": ""}},
		{Type: "git", Data: map[string]any{"project": "", "base": true}},
		{Type: "files", Data: map[string]any{"project": ""}},
		{Type: "lsp", Data: map[string]string{"project": ""}},
		{Type: "assistant", Data: map[string]string{}},
		ActivityEvent(s.activity.Working()),
	} {
		ev, ok := guestEvent(on.projects, len(on.assistants) > 0, shown, ev)
		if !ok {
			continue
		}
		if err := writeEnvelope(w, ev); err != nil {
			return
		}
	}
	if err := writeEnvelope(w, s.hostEvent()); err != nil {
		return
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	hostBeat := time.NewTicker(hostPushInterval)
	defer hostBeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if err := writeEnvelope(w, eventbus.Event{Type: "ping"}); err != nil {
				return
			}
		case <-hostBeat.C:
			if err := writeEnvelope(w, s.hostEvent()); err != nil {
				return
			}
		case linked := <-relinked:
			if linked.equal(on) || len(linked.projects)+len(linked.assistants) == 0 {
				continue
			}
			on = linked
			for _, ev := range guestRelinked {
				if err := writeEnvelope(w, ev); err != nil {
					return
				}
			}
		case ev := <-busEvents:
			ev, ok := guestEvent(on.projects, len(on.assistants) > 0, shown, ev)
			if !ok {
				continue
			}
			if err := writeEnvelope(w, ev); err != nil {
				return
			}
		}
	}
}

func guestProjectNames(projects []project.Project) []string {
	names := make([]string, 0, len(projects))
	for _, p := range projects {
		names = append(names, p.Name)
	}
	return names
}
