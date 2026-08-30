package web

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/notify"
)

// notificationListLimit caps how many entries the center fetches at once.
const notificationListLimit = 50

func (s *Server) handleNotificationsList(c *gin.Context) {
	list := s.notifier.List(notificationListLimit)
	if list == nil {
		list = []notify.Notification{}
	}
	c.JSON(http.StatusOK, gin.H{
		"notifications": list,
		"unread":        s.notifier.UnreadCount(),
	})
}

func (s *Server) handleNotificationsRead(c *gin.Context) {
	var unread int
	switch {
	case c.PostForm("all") != "":
		unread = s.notifier.MarkAllRead()
	case c.PostForm("target") != "":
		unread = s.notifier.MarkTargetRead(c.PostForm("target"))
	case c.PostForm("id") != "":
		unread = s.notifier.MarkRead(c.PostForm("id"))
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "A notification id is required."})
		return
	}
	c.JSON(http.StatusOK, gin.H{"unread": unread})
}

// hostPushInterval is the beat of the host event on the stream, faster than
// the 15 second heartbeat so the gauges read as live.
const hostPushInterval = 5 * time.Second

// hostSampleTTL is how long one host reading serves every connected browser.
// Below the push interval, so a lone tab gets a fresh number on every beat
// while ten tabs still cost one reading.
const hostSampleTTL = 4 * time.Second

// handleEventStream is the single server to client push channel, served at /events.
// It carries every server event, not only notifications:
// each SSE frame is a {type, data} envelope sent under the event name "dc", which
// the @dc/events client re-dispatches as a dc:<type> DOM event so any custom
// element can subscribe. On connect, including every EventSource reconnect, it
// pushes a snapshot of the current state (unread notifications, the terminals,
// projects and docker signals, the draft and assistant ones, the host reading),
// so a freshly attached or a woken background page catches up in one shot.
func (s *Server) handleEventStream(c *gin.Context) {
	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	if _, ok := w.(http.Flusher); !ok {
		c.String(http.StatusInternalServerError, "streaming unsupported")
		return
	}
	if err := writeSSERetry(w, 2*time.Second); err != nil {
		return
	}

	notifyEvents, cancelNotify := s.notifier.Subscribe()
	defer cancelNotify()
	busEvents, cancelBus := s.bus.Subscribe()
	defer cancelBus()

	// Snapshot: current unread state plus a bare terminals signal (no project) so
	// the tab strip and quick nav pull their fragment and the projects page
	// reconciles all its sections, catching a page up after connect or reconnect.
	if err := writeEnvelope(w, eventbus.Event{Type: "notifications", Data: s.notifier.UnreadEvent()}); err != nil {
		return
	}
	if err := writeEnvelope(w, eventbus.Event{Type: "terminals"}); err != nil {
		return
	}
	// The working marks as a snapshot, the same shape every change publishes:
	// the icons are decorated from the full set, so a page that connects
	// mid-turn shows the mark without waiting for the next transition.
	if err := writeEnvelope(w, eventbus.Event{Type: "activity", Data: map[string]any{"targets": s.activity.WorkingIDs()}}); err != nil {
		return
	}
	// The project set too: the terminals signal only reconciles the sections of
	// the rows a page already has, so a row created or removed while the socket
	// was down would stand there until the next navigation. A deletion that
	// brings compose stacks down is exactly that case, it outlives its request
	// and finishes into whatever stream is up then.
	if err := writeEnvelope(w, eventbus.Event{Type: "projects"}); err != nil {
		return
	}
	// And what docker shows: the cache only speaks when a container moves, so a
	// surface that reads it on connect alone stands on what it saw before the
	// socket went down until something happens to move. The editor's docker
	// segment and an open docker sheet are exactly that, they ask once and then
	// follow this event.
	if err := writeEnvelope(w, eventbus.Event{Type: "docker"}); err != nil {
		return
	}
	// A bare draft signal, no conversation named: an open composer pulls its own
	// draft after a reconnect, the way the tab strip pulls its fragment.
	if err := writeEnvelope(w, eventbus.Event{Type: "draft", Data: map[string]string{"conversation": ""}}); err != nil {
		return
	}
	// The same for the commit panel: a bare commitdraft signal, no project
	// named, and every open editor pulls its own project's draft.
	if err := writeEnvelope(w, eventbus.Event{Type: "commitdraft", Data: map[string]string{"project": ""}}); err != nil {
		return
	}
	// And the same for the editor palette: a bare searchdraft signal, no
	// project named, so a palette that sat through a gap opens on the search
	// the other device left rather than on the one from before the gap.
	if err := writeEnvelope(w, eventbus.Event{Type: "searchdraft", Data: map[string]string{"project": ""}}); err != nil {
		return
	}
	// The line comments the same way: a bare linecomments signal, no project
	// named, and every open editor pulls its own project's list.
	if err := writeEnvelope(w, eventbus.Event{Type: "linecomments", Data: map[string]string{"project": ""}}); err != nil {
		return
	}
	// And a bare git signal, no project named. The git event is otherwise only
	// published when something moves, and a move that fell into a gap is
	// published never again: the same file changing further does not move the
	// status list. Every open editor answers this one with its full catch-up.
	if err := writeEnvelope(w, eventbus.Event{Type: "git", Data: map[string]any{"project": "", "base": true}}); err != nil {
		return
	}
	// The same for the file watch: while the socket was down the watch lapsed,
	// its tick ended, and everything the disk did in that gap was published to
	// nobody. A bare files signal, no project and no paths, is what tells every
	// open editor to look at all of its own again.
	if err := writeEnvelope(w, eventbus.Event{Type: "files", Data: map[string]any{"project": ""}}); err != nil {
		return
	}
	// A bare lsp signal for the same reason: indexing moves published into a
	// gap come never again, and a page opened mid-indexing has seen none of
	// them, so every open editor pulls its project's indexing status.
	if err := writeEnvelope(w, eventbus.Event{Type: "lsp", Data: map[string]string{"project": ""}}); err != nil {
		return
	}
	// A bare assistant signal: an open conversation surface pulls its state and
	// catches up on a message that arrived while the socket was down, the same
	// way the tab strip and the quick nav catch up on the terminals signal.
	if err := writeEnvelope(w, eventbus.Event{Type: "assistant", Data: map[string]string{}}); err != nil {
		return
	}
	// And the standing git questions: the dialog is a mirror of server state,
	// so a page that just loaded, reconnected or woke pulls the list and shows
	// or clears its dialog accordingly. Without this a question parked while
	// the socket was down would wait out its whole window unseen.
	if err := writeEnvelope(w, eventbus.Event{Type: "gitprompt"}); err != nil {
		return
	}
	// The host reading rides the stream: it goes out on connect and then on its
	// own beat in the loop below. That ticker lives in this handler, so without
	// a connected browser nothing ticks and nothing is read, and several tabs
	// share one reading through the cache.
	if err := writeEnvelope(w, eventbus.Event{Type: "host", Data: s.host.Stats()}); err != nil {
		return
	}

	heartbeat := time.NewTicker(15 * time.Second)
	defer heartbeat.Stop()
	hostBeat := time.NewTicker(hostPushInterval)
	defer hostBeat.Stop()
	ctx := c.Request.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-heartbeat.C:
			// A real "ping" event, not an SSE comment: comments keep the socket
			// warm but fire no client event, so the @dc/events watchdog could not
			// tell a live-but-idle stream from a silently dead one. This lets it.
			if err := writeEnvelope(w, eventbus.Event{Type: "ping"}); err != nil {
				return
			}
		case <-hostBeat.C:
			if err := writeEnvelope(w, eventbus.Event{Type: "host", Data: s.host.Stats()}); err != nil {
				return
			}
		case ev := <-notifyEvents:
			if err := writeEnvelope(w, eventbus.Event{Type: "notifications", Data: ev}); err != nil {
				return
			}
		case ev := <-busEvents:
			if err := writeEnvelope(w, ev); err != nil {
				return
			}
		}
	}
}

func writeEnvelope(w http.ResponseWriter, ev eventbus.Event) error {
	data, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	return writeSSEvent(w, "dc", string(data))
}

// publishTerminals signals that the live coder/shell set or its order changed.
// It names the affected project so an open projects page can pull and swap just
// that project's two sections in place; an empty name (a reorder, or the connect
// snapshot) means "refresh everything". Every surface reacts by pulling its own
// per-client fragment (authenticated as that client, carrying its path), so the
// active tab and the CSRF token stay correct and each element keeps its own state
// (unfold, filter). Every call site is also a terminal mutation, so the terminal
// restore snapshot is rewritten here.
func (s *Server) publishTerminals(projectName string) {
	s.restorer.Write()
	s.bus.Publish(eventbus.Event{Type: "terminals", Data: map[string]string{"project": projectName}})
}

// PublishTerminals is the same announcement for a terminal change the cockpit
// did not make itself. A coder names its own sessions, so a rename happens
// inside the CLI and never reaches a handler; the turn watch sees it on its
// next tick and reports it here. It takes the one terminals event and nothing
// of its own, so every surface follows a rename exactly the way it follows a
// start or a stop, restore snapshot included: the snapshot carries the display
// name, and a name it wrote before the rename would come back on the next
// restore.
func (s *Server) PublishTerminals(projectName string) {
	s.publishTerminals(projectName)
}

// publishProjects signals that the project set changed. The projects page pulls
// its own server-rendered list, including rows created or removed by the assistant.
func (s *Server) publishProjects() {
	s.bus.Publish(eventbus.Event{Type: "projects"})
}

// publishGit signals that one project's git state moved, named like the
// terminals event so an open editor of that project pulls the fresh status
// itself. It carries the movement, never the state: what changed is what the
// client's own request answers. base says whether the commit a diff is compared
// against moved, as opposed to the working copy alone, so a save costs every
// open diff nothing.
func (s *Server) publishGit(projectName string, base bool) {
	s.bus.Publish(eventbus.Event{Type: "git", Data: map[string]any{"project": projectName, "base": base}})
}

// publishFiles signals that paths of one project moved on disk while an editor
// had them on the screen. Like the git event it carries the movement and never
// the state: the paths say where to look, the client pulls the directory
// listing or the file itself and decides what that means for its own buffer.
// Files and directories travel apart because what they ask for is not the same
// request, /editor/file against /editor/list, and because only a directory can
// have made the quick open index wrong.
func (s *Server) publishFiles(projectName string, files, dirs []string) {
	s.bus.Publish(eventbus.Event{Type: "files", Data: map[string]any{
		"project": projectName,
		"files":   files,
		"dirs":    dirs,
	}})
}

// publishCommitDraft signals that one project's commit draft moved: a device
// saved the panel, or a commit spent it. Like the git event it carries the
// movement and never the state, every open panel pulls the draft itself.
func (s *Server) publishCommitDraft(projectName string) {
	s.bus.Publish(eventbus.Event{Type: "commitdraft", Data: map[string]string{"project": projectName}})
}

// publishSearchDraft signals that one project's palette moved: a device typed
// in the search, picked a folder or a mask, or threw a switch. Like the commit
// draft it carries the movement and never the state, every open palette pulls
// the draft itself.
func (s *Server) publishSearchDraft(projectName string) {
	s.bus.Publish(eventbus.Event{Type: "searchdraft", Data: map[string]string{"project": projectName}})
}

// publishLineComments signals that one project's line comments moved: a
// note was written, moved along with an edit, or cleared. Like the commit
// draft it carries the movement and never the state, every open editor pulls
// the list itself.
func (s *Server) publishLineComments(projectName string) {
	s.bus.Publish(eventbus.Event{Type: "linecomments", Data: map[string]string{"project": projectName}})
}

// publishGitPrompt signals that the standing askpass questions moved: one was
// parked, answered, or taken along by its action's end. Bare like the other
// signals, every page pulls the list itself and reconciles its dialog, which
// is also how an answered or expired question closes on every other device.
func (s *Server) publishGitPrompt() {
	s.bus.Publish(eventbus.Event{Type: "gitprompt"})
}
