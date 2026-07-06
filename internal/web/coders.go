package web

import (
	"errors"

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

// coderQuery returns the query suffix that keeps a link on the given coder.
// Empty in single-coder mode so URLs stay clean.
func (s *Server) coderQuery(co *coder.Manager) string {
	if !s.multiCoder() {
		return ""
	}
	return "?coder=" + co.ID()
}

// coderTabs feeds the coder switcher on coder-scoped pages.
func (s *Server) coderTabs(base string, co *coder.Manager) render.CoderTabs {
	return render.CoderTabs{Base: base, Coders: s.coderIDs(), Selected: co.ID()}
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
