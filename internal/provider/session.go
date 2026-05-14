package provider

import (
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/filesystem"
)

// Session is one stored provider session that can be resumed.
type Session struct {
	SessionID     string
	Name          string
	CWD           string
	UpdatedAt     time.Time
	SizeBytes     int64 // negative when unknown
	RemoteControl bool
	TaskURL       string
}

// SessionRepository manages provider-specific persisted sessions and files.
type SessionRepository interface {
	List() []Session
	DeleteSession(sessionID string) error
	ListFiles(sessionID string) ([]filesystem.File, error)
	SaveFile(sessionID, rawName string, src io.Reader) (filesystem.File, error)
	OpenFile(sessionID, rawName string) (filesystem.OpenedFile, error)
	DeleteFile(sessionID, rawName string) (filesystem.File, error)
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

// ParseTimestamp parses an RFC3339 timestamp as written by provider state files.
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
