package web

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// steerMarks is what a render shows of the jobs, read once: the terminals an
// open job holds, the criterion the steer dialog offers again, and the
// assistant behind each mark under the title the assistants list shows. Only
// render.Page.SteeredBy reads the last one, which names nobody to a guest.
func (s *Server) steerMarks() (map[string]bool, map[string]string, map[string]render.Steerer) {
	owners, prefill := s.watcher.Marks()
	titles := map[string]string{}
	for _, entry := range s.assistants.List() {
		titles[entry.ID] = entry.Title
	}
	steered := make(map[string]bool, len(owners))
	by := make(map[string]render.Steerer, len(owners))
	for terminal, owner := range owners {
		steered[terminal] = true
		if title, ok := titles[owner]; ok {
			by[terminal] = render.Steerer{Title: title, URL: "/assistants/" + owner}
		}
	}
	return steered, prefill, by
}

// handleCoderSteeredMark serves the coder icon of one terminal, which is how
// the attach pages follow the terminals event: their icons do not re-render
// on it, so a dc-steered-mark element pulls this fragment instead. The icon
// is rendered here exactly like everywhere else, purple while a job holds the
// terminal, and the client only swaps it in. A terminal that is gone answers
// empty: the page showing it is stale either way.
func (s *Server) handleCoderSteeredMark(c *gin.Context) {
	id := c.Param("id")
	coderID, launcher := "", ""
	for _, m := range s.coders {
		if _, err := m.ResolveRunning(id); err == nil {
			coderID = m.ID()
			launcher = coder.LauncherForSession(m.Coder(), id)
			break
		}
	}
	if coderID == "" {
		c.Status(http.StatusOK)
		return
	}
	steered, _, by := s.steerMarks()
	c.HTML(http.StatusOK, "steered_icon.gohtml", map[string]any{
		"ID":       id,
		"Coder":    coderID,
		"Launcher": launcher,
		"Steered":  steered[id],
		"By":       render.Page{Steerers: by, Access: accessOf(c)}.SteeredBy(id),
		"Working":  s.activity.Working()[id],
	})
}

// handleCoderSteeredBy serves the label a large screen's head stands beside
// the coder's name, pulled by its dc-steered-mark on the same event as the
// icon. No link reaches it, and nobody steering answers empty.
func (s *Server) handleCoderSteeredBy(c *gin.Context) {
	_, _, by := s.steerMarks()
	c.HTML(http.StatusOK, "steered_by.gohtml", render.Page{Steerers: by, Access: accessOf(c)}.SteeredBy(c.Param("id")))
}
