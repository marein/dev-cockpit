package web

import (
	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/web/render"
)

// switcher collects the live sessions and shells for the quick-switch button.
// The current id comes from the route param, so attach pages can mark the entry
// you are already on. Snapshot is cached and shell listing is a cheap pane scan,
// so this is fine to build on every page render.
func (s *Server) switcher(c *gin.Context) render.Switcher {
	sw := render.Switcher{CurrentID: c.Param("id")}
	for _, r := range s.sessions.Snapshot().Running {
		sw.Sessions = append(sw.Sessions, render.SwitchTarget{
			ID:   r.Identifier,
			Name: r.Name,
			URL:  "/sessions/" + r.Identifier,
		})
	}
	for _, sh := range s.shells.List() {
		sw.Shells = append(sw.Shells, render.SwitchTarget{
			ID:   sh.Identifier,
			Name: sh.Name,
			URL:  "/shells/" + sh.Identifier,
		})
	}
	return sw
}
