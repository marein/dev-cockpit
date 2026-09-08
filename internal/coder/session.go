package coder

import (
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

// Session is one stored coder-CLI session that can be resumed.
type Session struct {
	SessionID string
	Name      string
	CWD       string
	UpdatedAt time.Time
}

// SessionRepository manages coder-specific persisted sessions and files.
type SessionRepository interface {
	List() []Session
	DeleteSession(sessionID string) error
	ListFiles(sessionID string) ([]filesystem.File, error)
	SaveFile(sessionID, rawName string, src io.Reader) (filesystem.File, error)
	OpenFile(sessionID, rawName string) (filesystem.OpenedFile, error)
	DeleteFile(sessionID, rawName string) (filesystem.File, error)
}

// SessionCandidates is the optional wider view of a coder's own store, the one
// a promote reads. List is what every surface shows, and it may leave out a
// record that carries nothing for a person: copilot hides one without a name
// and without conversation events, the empty record a resume leaves behind. A
// promote looks for exactly that shape, the record the CLI has only just
// created and has neither named nor written to, so it asks a repository that
// offers this view for it, and List where none is offered.
type SessionCandidates interface {
	CandidateSessions() []Session
}

// LessSession orders sessions newest-first, with name and ID as tie-breakers.
func LessSession(a, b Session) bool {
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	an, bn := strings.ToLower(a.Name), strings.ToLower(b.Name)
	if an != bn {
		return an > bn
	}
	return a.SessionID > b.SessionID
}

// ParseTimestamp parses an RFC3339 timestamp as written by coder-CLI state files.
func ParseTimestamp(raw string) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

// NormalizeCWD resolves symlinks so working directories compare reliably.
func NormalizeCWD(path string) string {
	if path == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved
	}
	return path
}
