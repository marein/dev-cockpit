package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/filesystem"
	"github.com/local/dev-cockpit/internal/provider"
	"github.com/local/dev-cockpit/internal/sessionlabel"
)

type transcriptEntry struct {
	Type            string `json:"type"`
	SessionID       string `json:"sessionId"`
	BridgeSessionID string `json:"bridgeSessionId"`
	SessionName     string `json:"sessionName"`
	AgentName       string `json:"agentName"`
	CustomTitle     string `json:"customTitle"`
	CWD             string `json:"cwd"`
	Timestamp       string `json:"timestamp"`
	URL             string `json:"url"`
}

type sessionRepository struct {
	stateRoot string
}

type storedSession struct {
	provider.Session
	sessionFile string
	sessionDir  string
	filesDir    string
}

func (r *sessionRepository) List() []provider.Session {
	stored := r.listStored()
	out := make([]provider.Session, 0, len(stored))
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
	if err := os.RemoveAll(stored.sessionFile); err != nil {
		return err
	}
	if stored.sessionDir != "" {
		if err := os.RemoveAll(stored.sessionDir); err != nil {
			return err
		}
	}
	return nil
}

func (r *sessionRepository) ListFiles(sessionID string) ([]filesystem.File, error) {
	dir, err := r.filesDir(sessionID)
	if err != nil {
		return []filesystem.File{}, nil
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
		return filesystem.File{}, fmt.Errorf("Session files will be available after the first message.")
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

// filesDir locates the files directory for a session without scanning every
// transcript: the transcript lives at stateRoot/<project>/<sessionID>.jsonl
// and its files at stateRoot/<project>/<sessionID>/files. Only when the
// transcript file name does not match the session ID (sessions whose
// transcript declares a different sessionId) does it fall back to a scan.
func (r *sessionRepository) filesDir(sessionID string) (string, error) {
	id := strings.TrimSpace(sessionID)
	if id == "" {
		return "", fmt.Errorf("Session identifier is required.")
	}
	if strings.ContainsAny(id, `/\*?[`) {
		return "", fmt.Errorf("Invalid session identifier.")
	}
	matches, err := filepath.Glob(filepath.Join(r.stateRoot, "*", id+".jsonl"))
	if err != nil || len(matches) == 0 {
		stored, err := r.findStored(id)
		if err != nil {
			return "", err
		}
		return stored.filesDir, nil
	}
	abs, err := filepath.Abs(matches[0])
	if err != nil {
		return "", err
	}
	dir := filepath.Join(filepath.Dir(abs), id, "files")
	absRoot, _ := filepath.Abs(r.stateRoot)
	if !filesystem.IsUnder(dir, absRoot) {
		return "", fmt.Errorf("Invalid session identifier.")
	}
	return dir, nil
}

func (r *sessionRepository) listStored() []storedSession {
	info, err := os.Stat(r.stateRoot)
	if err != nil || !info.IsDir() {
		return nil
	}
	matches, err := filepath.Glob(filepath.Join(r.stateRoot, "*", "*.jsonl"))
	if err != nil {
		return nil
	}
	out := make([]storedSession, 0, len(matches))
	for _, transcript := range matches {
		session, ok := r.loadTranscript(transcript)
		if ok {
			out = append(out, session)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return provider.LessSession(out[i].Session, out[j].Session) })
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

func (r *sessionRepository) loadTranscript(path string) (storedSession, bool) {
	f, err := os.Open(path)
	if err != nil {
		return storedSession{}, false
	}
	defer f.Close()

	sessionID := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	name := ""
	namePriority := 0
	cwd := ""
	var updatedAt time.Time
	taskLink := ""

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var entry transcriptEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if v := strings.TrimSpace(entry.SessionID); v != "" {
			sessionID = v
		}
		if v := strings.TrimSpace(entry.CWD); cwd == "" && v != "" {
			cwd = provider.NormalizeCWD(v)
		}
		if t, ok := provider.ParseTimestamp(entry.Timestamp); ok {
			updatedAt = t
		}
		if v := taskURL(entry); taskLink == "" && v != "" {
			taskLink = v
		}
		switch entry.Type {
		case "custom-title":
			if v := strings.TrimSpace(entry.CustomTitle); v != "" {
				name = v
				namePriority = 3
			}
		case "agent-name":
			if namePriority < 2 {
				if v := strings.TrimSpace(entry.SessionName); v != "" {
					name = v
					namePriority = 2
				} else if namePriority < 1 {
					if v := strings.TrimSpace(entry.AgentName); v != "" {
						name = v
						namePriority = 1
					}
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return storedSession{}, false
	}
	if cwd == "" {
		return storedSession{}, false
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		return storedSession{}, false
	}
	if updatedAt.IsZero() {
		fileInfo, err := os.Stat(path)
		if err == nil {
			updatedAt = fileInfo.ModTime().UTC()
		}
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return storedSession{}, false
	}
	sessionDir := filepath.Join(filepath.Dir(absPath), sessionID)
	filesDir := filepath.Join(sessionDir, "files")
	sizePaths := []string{absPath}
	if info, err := os.Stat(filesDir); err == nil && info.IsDir() {
		sizePaths = append(sizePaths, filesDir)
	}
	return storedSession{
		Session: provider.Session{
			SessionID:     sessionID,
			Name:          sessionlabel.DisplayName(name, sessionID),
			CWD:           cwd,
			UpdatedAt:     updatedAt,
			SizeBytes:     filesystem.PathsSizeBytes(sizePaths...),
			RemoteControl: taskLink != "",
			TaskURL:       taskLink,
		},
		sessionFile: absPath,
		sessionDir:  sessionDir,
		filesDir:    filesDir,
	}, true
}

func taskURL(entry transcriptEntry) string {
	if url := strings.TrimSpace(entry.URL); strings.HasPrefix(url, "https://claude.ai/code/") {
		return url
	}
	id := strings.TrimSpace(entry.BridgeSessionID)
	if strings.HasPrefix(id, "cse_") {
		return "https://claude.ai/code/session_" + strings.TrimPrefix(id, "cse_")
	}
	return ""
}
