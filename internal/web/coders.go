package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/web/render"
)

func (s *Server) multiCoder() bool { return len(s.coders) > 1 }

func (s *Server) coderIDs() []string {
	ids := make([]string, 0, len(s.coders))
	for i := range s.coders {
		ids = append(ids, s.coders[i].ID())
	}
	return ids
}

func (s *Server) coderByID(id string) *coder.Manager {
	for i := range s.coders {
		if s.coders[i].ID() == id {
			return s.coders[i]
		}
	}
	return nil
}

// coderFromRequest picks the coder addressed by the request's "coder" form or
// query value. Empty selects the first active coder, so single-coder setups
// never need the parameter.
func (s *Server) coderFromRequest(c *gin.Context) (*coder.Manager, error) {
	raw := c.PostForm("coder")
	if raw == "" {
		raw = c.Query("coder")
	}
	if raw == "" {
		return s.coders[0], nil
	}
	if co := s.coderByID(raw); co != nil {
		return co, nil
	}
	return nil, errors.New(`Unknown coder "` + raw + `".`)
}

// coderBase returns the canonical URL prefix of a coder's scoped pages. They
// are settings of one coder, so they live under the settings.
func (s *Server) coderBase(co *coder.Manager) string {
	return "/settings/coders/" + co.ID()
}

// settingsNav builds the settings sidebar for a page that is not coder
// scoped. The coder entries then lead to the instructions and none is marked.
func (s *Server) settingsNav(active string) render.SettingsNav {
	return s.coderSettingsNav(active, nil, "")
}

// coderSettingsNav builds the settings sidebar for a coder page: the page's
// own coder is marked, and every coder entry keeps the section, so switching
// the coder stays on instructions, agents or skills.
func (s *Server) coderSettingsNav(active string, co *coder.Manager, section string) render.SettingsNav {
	nav := render.SettingsNav{
		Active:  active,
		Section: section,
		Reviews: s.backups.PendingReviewCount(),
	}
	if co != nil {
		nav.Selected = co.ID()
	}
	target := section
	if target == "" {
		target = "instructions"
	}
	for i := range s.coders {
		nav.Coders = append(nav.Coders, render.SettingsCoder{
			ID:  s.coders[i].ID(),
			URL: s.coderBase(s.coders[i]) + "/" + target,
		})
	}
	return nav
}

// coderTitle prefixes a section title with the coder label when several
// coders are active, matching the page header.
func (s *Server) coderTitle(co *coder.Manager, section string) string {
	if !s.multiCoder() {
		return section
	}
	id := co.ID()
	return strings.ToUpper(id[:1]) + id[1:] + " " + section
}

// redirectMovedCoderPath forwards a coder page URL from before the pages moved
// under the settings (/coders/<coder>/...) to its canonical path, keeping the
// rest of the path and the query. 308 keeps method and body, so a form of a
// page loaded before the move still saves.
// TODO(v2.0.0): drop together with the pre-settings routes.
func (s *Server) redirectMovedCoderPath(co *coder.Manager) gin.HandlerFunc {
	old := "/coders/" + co.ID()
	return func(c *gin.Context) {
		target := s.coderBase(co) + strings.TrimPrefix(c.Request.URL.Path, old)
		if q := c.Request.URL.RawQuery; q != "" {
			target += "?" + q
		}
		c.Redirect(http.StatusPermanentRedirect, target)
	}
}

// redirectLegacyCoderPath forwards a pre-canonical coder page URL (top-level
// /instructions, /agents, /skills, coder picked via query or form field) to
// the coder's base plus the same path. 308 keeps method and body, so stale
// forms and bookmarks replay against the canonical route.
// TODO(v2.0.0): drop together with the legacy routes.
func (s *Server) redirectLegacyCoderPath(c *gin.Context) {
	co, err := s.coderFromRequest(c)
	if err != nil {
		section := strings.SplitN(strings.TrimPrefix(c.Request.URL.Path, "/"), "/", 2)[0]
		s.redirectWithFlash(c, s.coderBase(s.coders[0])+"/"+section, "", err.Error())
		return
	}
	c.Redirect(http.StatusPermanentRedirect, s.coderBase(co)+c.Request.URL.Path)
}

// resolveRunning finds the coder owning the live session with the given
// identifier. On a miss it keeps the most specific error: any validation or
// refusal error wins over plain "no active session".
func (s *Server) resolveRunning(rawID string) (*coder.Manager, coder.Running, error) {
	var firstErr error
	for i := range s.coders {
		r, err := s.coders[i].ResolveRunning(rawID)
		if err == nil {
			return s.coders[i], r, nil
		}
		if firstErr == nil || (errors.Is(firstErr, coder.ErrNotRunning) && !errors.Is(err, coder.ErrNotRunning)) {
			firstErr = err
		}
	}
	return nil, coder.Running{}, firstErr
}

// resolveResumable finds the coder owning the stored session with the given id.
func (s *Server) resolveResumable(rawID string) (*coder.Manager, coder.Session, error) {
	var firstErr error
	for i := range s.coders {
		stored, err := s.coders[i].ResolveResumable(rawID)
		if err == nil {
			return s.coders[i], stored, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return nil, coder.Session{}, firstErr
}

// coderForSession locates the coder responsible for a session id, live or
// stored, for the session file endpoints.
func (s *Server) coderForSession(rawID string) (*coder.Manager, error) {
	co, _, errRun := s.resolveRunning(rawID)
	if errRun == nil {
		return co, nil
	}
	if stored, _, err := s.resolveResumable(rawID); err == nil {
		return stored, nil
	}
	return nil, errRun
}

// coderForInput routes input and resize to the owning coder. While a browser
// stream is attached the owner is found without touching the process table,
// keeping the per-keystroke path fork-free.
func (s *Server) coderForInput(rawID string) (*coder.Manager, error) {
	for i := range s.coders {
		if s.coders[i].OwnsStream(rawID) {
			return s.coders[i], nil
		}
	}
	co, _, err := s.resolveRunning(rawID)
	return co, err
}
