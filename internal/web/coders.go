package web

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/web/render"
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

// coderBase returns the canonical URL prefix of a coder's scoped pages.
func (s *Server) coderBase(co *coder.Manager) string {
	return "/coders/" + co.ID()
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

// coderNav feeds the coder switcher and section tabs on coder-scoped pages.
func (s *Server) coderNav(active string, co *coder.Manager) render.CoderNav {
	return render.CoderNav{Coders: s.coderIDs(), Selected: co.ID(), Active: active, Multi: s.multiCoder()}
}

// redirectLegacyCoderPath forwards a pre-canonical coder page URL (top-level
// /instructions, /agents, /skills, coder picked via query or form field) to
// /coders/<coder> plus the same path. 308 keeps method and body, so stale
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
