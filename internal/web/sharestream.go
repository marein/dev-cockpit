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

// The two long requests of a guest: a terminal's stream, which ends the
// moment the link stops opening the terminal's project, and the app's event
// stream, which ends the moment the link opens nothing.

// followShare runs check on a guest's link as it stands now, and again after
// every write of the links, every move of the projects and at the link's
// expiry, until check answers false, which ends the context it returns. A
// link that no longer stands ends it without a check.
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
			if !shareMoved(ctx, changed, moves, current.ExpiresAt) {
				return
			}
		}
	}()
	return ctx, cancel
}

// shareMoved waits for a write of the shares, a move of the projects or the
// expiry, and answers false when the stream ended first.
func shareMoved(ctx context.Context, changed <-chan struct{}, moves <-chan eventbus.Event, expires time.Time) bool {
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
			if ev.Type == "projects" {
				return true
			}
		}
	}
}

// handleShareStream streams a terminal of a project of the link to a guest.
// The stream loop is the owner's, what it reads is a terminal.View, a viewer
// that never resizes the pane, whatever size the request asks for, and never
// moves the view of the owner's browsers. The theme the request may carry is
// never read. It ends when the project leaves the link, by a removal, a
// revoke, the expiry or the project's end.
func (s *Server) handleShareStream(c *gin.Context) {
	g, _ := guestOf(c)
	ctx, cancel := s.followShare(c.Request.Context(), g, func(sh share.Share) bool {
		_, ok := s.liveShareProject(sh, g.project.Name)
		return ok
	})
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	id := c.Param("id")
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
// or with none, and the docker one. Everything else, the notifications, the
// other projects, the drafts, the assistants, the questions git asks, the
// costs and the host, stays with the owner, and so does any type added later.
func guestEvent(projects []string, ev eventbus.Event) bool {
	switch ev.Type {
	case "docker", "ping":
		return true
	case "terminals", "git", "files", "linecomments", "lsp":
		named := ""
		switch data := ev.Data.(type) {
		case map[string]string:
			named = data["project"]
		case map[string]any:
			named, _ = data["project"].(string)
		}
		return named == "" || slices.Contains(projects, named)
	}
	return false
}

// guestRelinked are the signals a guest's pages pull the link's projects
// again on, sent when a project went on the link or off it.
var guestRelinked = []eventbus.Event{{Type: "projects"}, {Type: "terminals"}}

// handleShareEvents is the event stream of a guest's pages, the owner's
// signals narrowed by guestEvent to the projects the link opens right now. On
// connect it sends the bare signals the guest's surfaces catch up on, the way
// the owner's stream does, the projects among them, and when the link's
// projects change it sends the guest's own projects signal.
func (s *Server) handleShareEvents(c *gin.Context) {
	g, _ := guestOf(c)
	on := guestProjectNames(s.guestProjects(g))
	relinked := make(chan []string, 1)
	ctx, cancel := s.followShare(c.Request.Context(), g, func(sh share.Share) bool {
		names := guestProjectNames(s.liveShareProjects(sh))
		select {
		case <-relinked:
		default:
		}
		relinked <- names
		return len(names) > 0
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
	for _, ev := range []eventbus.Event{
		{Type: "projects"},
		{Type: "terminals"},
		{Type: "docker"},
		{Type: "linecomments", Data: map[string]string{"project": ""}},
		{Type: "git", Data: map[string]any{"project": "", "base": true}},
		{Type: "files", Data: map[string]any{"project": ""}},
		{Type: "lsp", Data: map[string]string{"project": ""}},
	} {
		if err := writeEnvelope(w, ev); err != nil {
			return
		}
	}
	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			if err := writeEnvelope(w, eventbus.Event{Type: "ping"}); err != nil {
				return
			}
		case names := <-relinked:
			if slices.Equal(names, on) || len(names) == 0 {
				continue
			}
			on = names
			for _, ev := range guestRelinked {
				if err := writeEnvelope(w, ev); err != nil {
					return
				}
			}
		case ev := <-busEvents:
			if !guestEvent(on, ev) {
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
