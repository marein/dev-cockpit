package copilot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/local/dev-cockpit/internal/coder"
	"github.com/local/dev-cockpit/internal/filesystem"
)

type sessionRepository struct {
	stateRoot string
}

type storedSession struct {
	coder.Session
	sessionDir string
	filesDir   string
}

func (r *sessionRepository) List() []coder.Session {
	stored := r.listStored()
	out := make([]coder.Session, 0, len(stored))
	for _, s := range stored {
		out = append(out, s.Session)
	}
	return out
}

func (r *sessionRepository) DeleteSession(sessionID string) error {
	stored, err := r.findStored(sessionID)
	if err != nil {
		return err
	}
	return os.RemoveAll(stored.sessionDir)
}

func (r *sessionRepository) ListFiles(sessionID string) ([]filesystem.File, error) {
	dir, err := r.filesDir(sessionID)
	if err != nil {
		return nil, err
	}
	files, err := filesystem.ListFiles(dir)
	if err != nil {
		return nil, err
	}
	return files, nil
}

func (r *sessionRepository) SaveFile(sessionID, rawName string, src io.Reader) (filesystem.File, error) {
	dir, err := r.filesDir(sessionID)
	if err != nil {
		return filesystem.File{}, err
	}
	file, err := filesystem.SaveFile(dir, rawName, src)
	if err != nil {
		return filesystem.File{}, err
	}
	return file, nil
}

func (r *sessionRepository) OpenFile(sessionID, rawName string) (filesystem.OpenedFile, error) {
	dir, err := r.filesDir(sessionID)
	if err != nil {
		return filesystem.OpenedFile{}, err
	}
	file, err := filesystem.OpenFile(dir, rawName)
	if err != nil {
		return filesystem.OpenedFile{}, err
	}
	return file, nil
}

func (r *sessionRepository) DeleteFile(sessionID, rawName string) (filesystem.File, error) {
	dir, err := r.filesDir(sessionID)
	if err != nil {
		return filesystem.File{}, err
	}
	file, err := filesystem.DeleteFile(dir, rawName)
	if err != nil {
		return filesystem.File{}, err
	}
	return file, nil
}

func (r *sessionRepository) listStored() []storedSession {
	info, err := os.Stat(r.stateRoot)
	if err != nil || !info.IsDir() {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(r.stateRoot, "*", "workspace.yaml"))
	if err != nil {
		return nil
	}
	out := make([]storedSession, 0, len(matches))
	for _, wsFile := range matches {
		md, err := loadWorkspace(wsFile)
		if err != nil || md.CWD == "" {
			continue
		}
		// A copilot boot with --resume writes an extra empty session-state
		// record it never returns to. Unnamed records without any conversation
		// events carry nothing to resume, so they stay out of the list (they
		// cannot be deleted here either: the resuming process holds a lock).
		if strings.TrimSpace(md.Name) == "" {
			if _, err := os.Stat(filepath.Join(filepath.Dir(wsFile), "events.jsonl")); err != nil {
				continue
			}
		}
		cwd, err := filesystem.ExpandHome(md.CWD)
		if err != nil {
			cwd = md.CWD
		}
		cwd = coder.NormalizeCWD(cwd)
		info, err := os.Stat(cwd)
		if err != nil || !info.IsDir() {
			continue
		}
		sessionDir, _ := filepath.Abs(filepath.Dir(wsFile))
		id := md.ID
		if id == "" {
			id = filepath.Base(filepath.Dir(wsFile))
		}
		updatedAt, ok := coder.ParseTimestamp(md.UpdatedAt)
		if !ok {
			if info, err := os.Stat(wsFile); err == nil {
				updatedAt = info.ModTime().UTC()
			}
		}
		out = append(out, storedSession{
			Session: coder.Session{
				SessionID: id,
				Name:      coder.DisplayName(md.Name, id),
				CWD:       cwd,
				UpdatedAt: updatedAt,
			},
			sessionDir: sessionDir,
			filesDir:   filepath.Join(sessionDir, "files"),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return coder.LessSession(out[i].Session, out[j].Session) })
	return out
}

func (r *sessionRepository) findStored(sessionID string) (storedSession, error) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return storedSession{}, fmt.Errorf("Session identifier is required.")
	}
	for _, stored := range r.listStored() {
		if stored.SessionID == id {
			return stored, nil
		}
	}
	return storedSession{}, fmt.Errorf(`No session "%s" was found.`, id)
}

func (r *sessionRepository) filesDir(sessionID string) (string, error) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return "", fmt.Errorf("Session identifier is required.")
	}
	dir := filepath.Join(r.stateRoot, id, "files")
	absRoot, _ := filepath.Abs(r.stateRoot)
	absDir, _ := filepath.Abs(dir)
	if !filesystem.IsUnder(absDir, absRoot) {
		return "", fmt.Errorf("Invalid session identifier.")
	}
	return absDir, nil
}
