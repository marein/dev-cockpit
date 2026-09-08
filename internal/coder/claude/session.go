package claude

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
)

type transcriptEntry struct {
	Type        string `json:"type"`
	SessionID   string `json:"sessionId"`
	SessionName string `json:"sessionName"`
	AgentName   string `json:"agentName"`
	CustomTitle string `json:"customTitle"`
	AITitle     string `json:"aiTitle"`
	CWD         string `json:"cwd"`
	Timestamp   string `json:"timestamp"`
	// Message, IsMeta and IsSidechain are what the first prompt is read out
	// of, the title of a session nobody named.
	Message     json.RawMessage `json:"message"`
	IsMeta      bool            `json:"isMeta"`
	IsSidechain bool            `json:"isSidechain"`
}

type sessionRepository struct {
	stateRoot string
	mu        sync.Mutex
	cache     map[string]transcriptCache
}

// transcriptCache keeps the parse result of one transcript file, keyed on its
// mtime and size, so unchanged transcripts are never read twice. It exists
// because the transcripts are the only source of session metadata, so every
// scan parsed every line of every file, a cost that grows with the accumulated
// history and had reached hundreds of milliseconds per snapshot rebuild. With
// the cache a scan pays only for files that changed. Failed parses are cached
// too, otherwise a broken file would be re-read on every scan.
type transcriptCache struct {
	modTime time.Time
	size    int64
	session storedSession
	ok      bool
}

type storedSession struct {
	coder.Session
	sessionFile string
	sessionDir  string
	filesDir    string
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

// transcriptFile locates the transcript of a session without scanning every
// file: it lives at stateRoot/<project>/<sessionID>.jsonl, because the cockpit
// starts every session with the id it chose. Only when the file name does not
// match the session ID (sessions whose transcript declares a different
// sessionId) does it fall back to a scan. Nothing here is assembled from a
// working directory, the id is the whole lookup.
func (r *sessionRepository) transcriptFile(sessionID string) (string, error) {
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
		return stored.sessionFile, nil
	}
	abs, err := filepath.Abs(matches[0])
	if err != nil {
		return "", err
	}
	absRoot, _ := filepath.Abs(r.stateRoot)
	if !filesystem.IsUnder(abs, absRoot) {
		return "", fmt.Errorf("Invalid session identifier.")
	}
	return abs, nil
}

// filesDir locates the files directory for a session: next to its transcript,
// at stateRoot/<project>/<sessionID>/files.
func (r *sessionRepository) filesDir(sessionID string) (string, error) {
	transcript, err := r.transcriptFile(sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(transcript), strings.TrimSpace(sessionID), "files"), nil
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
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = map[string]transcriptCache{}
	}
	seen := make(map[string]bool, len(matches))
	out := make([]storedSession, 0, len(matches))
	for _, transcript := range matches {
		seen[transcript] = true
		info, err := os.Stat(transcript)
		if err != nil {
			delete(r.cache, transcript)
			continue
		}
		entry, hit := r.cache[transcript]
		if !hit || !entry.modTime.Equal(info.ModTime()) || entry.size != info.Size() {
			session, ok := r.loadTranscript(transcript)
			entry = transcriptCache{modTime: info.ModTime(), size: info.Size(), session: session, ok: ok}
			r.cache[transcript] = entry
		}
		if !entry.ok {
			continue
		}
		// The cwd check runs on every scan, cached entries included, so a
		// session whose project directory vanished drops out at once.
		if info, err := os.Stat(entry.session.CWD); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, entry.session)
	}
	for path := range r.cache {
		if !seen[path] {
			delete(r.cache, path)
		}
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

// maxTranscriptLine caps how much of a single transcript line is held in
// memory. A line carrying an image the coder read, a screenshot handed to it
// by path, holds the same base64 twice and passes a megabyte, so the cap sits
// far above that. Beyond it the line is dropped, never the file.
const maxTranscriptLine = 256 * 1024

// newTranscriptSplit returns a scanner split that works like bufio.ScanLines,
// except that a line reaching maxTranscriptLine is dropped and reading
// continues. The stock scanner turns that case into an error for the whole
// file, which cost every session whose coder had read an image its place in
// the list. Dropping spans every call up to the next newline, so the rest of
// an oversized line never reaches the parser as a line of its own. The state
// belongs to one file, every transcript needs its own split.
func newTranscriptSplit() bufio.SplitFunc {
	dropping := false
	return func(data []byte, atEOF bool) (int, []byte, error) {
		if atEOF && len(data) == 0 {
			return 0, nil, nil
		}
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			if dropping {
				dropping = false
				return i + 1, nil, nil
			}
			return i + 1, bytes.TrimRight(data[:i], "\r"), nil
		}
		if atEOF {
			if dropping {
				return len(data), nil, nil
			}
			return len(data), bytes.TrimRight(data, "\r"), nil
		}
		if len(data) >= maxTranscriptLine {
			dropping = true
			return len(data), nil, nil
		}
		return 0, nil, nil
	}
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
	prompt := ""
	cwd := ""
	var updatedAt time.Time

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), maxTranscriptLine)
	scanner.Split(newTranscriptSplit())
	for scanner.Scan() {
		var entry transcriptEntry
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			continue
		}
		if v := strings.TrimSpace(entry.SessionID); v != "" {
			sessionID = v
		}
		if v := strings.TrimSpace(entry.CWD); cwd == "" && v != "" {
			cwd = coder.NormalizeCWD(v)
		}
		if t, ok := coder.ParseTimestamp(entry.Timestamp); ok {
			updatedAt = t
		}
		if prompt == "" && entry.Type == "user" && !entry.IsMeta && !entry.IsSidechain {
			prompt = promptTitle(entry.Message)
		}
		switch entry.Type {
		case "custom-title":
			if v := strings.TrimSpace(entry.CustomTitle); v != "" {
				name = v
				namePriority = 4
			}
		case "agent-name":
			if namePriority < 3 {
				if v := strings.TrimSpace(entry.SessionName); v != "" {
					name = v
					namePriority = 3
				} else if namePriority < 2 {
					if v := strings.TrimSpace(entry.AgentName); v != "" {
						name = v
						namePriority = 2
					}
				}
			}
		case "ai-title":
			// claude's own title for a session nobody named: a short summary
			// of the first prompt, written by a background request to the
			// small model a moment after that prompt. It is the title its own
			// session picker shows, so the cockpit shows it too, and anything
			// a person chose stands above it.
			if namePriority < 1 {
				if v := strings.TrimSpace(entry.AITitle); v != "" {
					name = v
					namePriority = 1
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return storedSession{}, false
	}
	// Neither a person nor claude has titled this session, so its first prompt
	// stands in: claude writes its ai-title a moment after that prompt, and
	// until it lands, a session somebody is already talking to must not read
	// as a hexadecimal label.
	if name == "" {
		name = prompt
	}
	if cwd == "" {
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
	return storedSession{
		Session: coder.Session{
			SessionID: sessionID,
			Name:      coder.DisplayName(name, sessionID),
			CWD:       cwd,
			UpdatedAt: updatedAt,
		},
		sessionFile: absPath,
		sessionDir:  sessionDir,
		filesDir:    filesDir,
	}, true
}

// promptTitle turns a user message into the title of a session nobody named,
// which is what copilot and opencode do for their own sessions. Only the text
// a person wrote counts: tool results and the reminders the harness injects
// carry no intent, and a slash command is read as the command it is, because
// its wrapper says more about the transcript format than about the session.
// The title is one line and bounded, it stands in tab strips, menus and lists.
func promptTitle(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &message); err != nil || message.Role != "user" {
		return ""
	}
	text := ""
	if err := json.Unmarshal(message.Content, &text); err != nil {
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if err := json.Unmarshal(message.Content, &blocks); err != nil {
			return ""
		}
		var parts []string
		for _, b := range blocks {
			if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
				parts = append(parts, b.Text)
			}
		}
		text = strings.Join(parts, " ")
	}
	if command := betweenTags(text, "command-name"); command != "" {
		return coder.ShortTitle(command)
	}
	return coder.ShortTitle(dropTagged(text, "system-reminder"))
}

// betweenTags returns what one XML-ish wrapper in text holds.
func betweenTags(text, tag string) string {
	openTag, closeTag := "<"+tag+">", "</"+tag+">"
	start := strings.Index(text, openTag)
	if start < 0 {
		return ""
	}
	rest := text[start+len(openTag):]
	end := strings.Index(rest, closeTag)
	if end < 0 {
		return ""
	}
	return strings.TrimSpace(rest[:end])
}

// dropTagged removes every wrapper of one kind, contents included.
func dropTagged(text, tag string) string {
	openTag, closeTag := "<"+tag+">", "</"+tag+">"
	for {
		start := strings.Index(text, openTag)
		if start < 0 {
			return text
		}
		end := strings.Index(text[start:], closeTag)
		if end < 0 {
			return text[:start]
		}
		text = text[:start] + text[start+end+len(closeTag):]
	}
}
