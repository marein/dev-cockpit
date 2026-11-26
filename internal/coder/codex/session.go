package codex

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/proctree"
)

// A session is one rollout file, sessions/<year>/<month>/<day>/
// rollout-<time>-<id>.jsonl, whose first line carries the session's id and
// working directory. The files are the store this reads: they are what
// `codex resume` itself falls back to, they have kept this layout across
// codex's versions, and they need no driver, unlike the sqlite indexes beside
// them, whose file names carry a schema version. Every write stays with the
// CLI. The name lives apart, in session_index.jsonl, one line per naming,
// the last one counts.
//
// codex mints every session's id itself. A session the cockpit made for an
// assistant (startThread) carries the id the cockpit addresses it by in its
// thread source, and is listed under that id; everything that takes an id
// maps it back through nativeID.

const (
	sessionsDir = "sessions"
	indexFile   = "session_index.jsonl"
	filesDir    = "dev-cockpit-files"
	// metaLineLimit bounds the first line of a rollout, which carries the
	// whole base instructions of the model and runs to a few hundred KB.
	metaLineLimit = 8 << 20
	indexLimit    = 16 << 20
	cliTimeout    = 30 * time.Second
	// cockpitSource is the thread source of a session the cockpit made, in
	// front of the id the cockpit addresses it by.
	cockpitSource = "dev-cockpit:"
)

var (
	sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	rolloutPattern   = regexp.MustCompile(`^rollout-.+-([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\.jsonl$`)
)

type rolloutMeta struct {
	id      string
	cwd     string
	cockpit string
	user    bool
}

func (m rolloutMeta) address() string {
	if m.cockpit != "" {
		return m.cockpit
	}
	return m.id
}

type storedRollout struct {
	rolloutMeta
	updated time.Time
}

type sessionRepository struct {
	home   string
	remove func(sessionID string) error
	name   func(sessionID, name string) error

	mu         sync.Mutex
	metas      map[string]rolloutMeta
	rollouts   map[string]string
	names      map[string]string
	namesStamp string
}

func newSessionRepository(home string) *sessionRepository {
	return &sessionRepository{home: home, remove: removeSession, name: nameThread}
}

// removeSession deletes through the CLI's own command, which takes the
// rollout, its index lines and its rows in codex's databases along.
func removeSession(sessionID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), cliTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "codex", "delete", "--force", "--", sessionID)
	out, err := cmd.CombinedOutput()
	if err == nil {
		return nil
	}
	detail := clip(string(out))
	if detail == "" {
		detail = err.Error()
	}
	return fmt.Errorf("codex delete failed: %s", detail)
}

func (r *sessionRepository) List() []coder.Session {
	stored := r.stored()
	names := r.sessionNames()
	out := make([]coder.Session, 0, len(stored))
	for _, s := range stored {
		if dir, err := os.Stat(s.cwd); err != nil || !dir.IsDir() {
			continue
		}
		out = append(out, coder.Session{
			SessionID: s.address(),
			Name:      coder.DisplayName(names[s.id], s.address()),
			CWD:       s.cwd,
			UpdatedAt: s.updated,
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return coder.LessSession(out[i], out[j]) })
	return out
}

// stored reads the sessions a person or the cockpit started, whatever became
// of their working directory.
func (r *sessionRepository) stored() []storedRollout {
	matches, err := filepath.Glob(filepath.Join(r.home, sessionsDir, "*", "*", "*", "rollout-*.jsonl"))
	if err != nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.metas == nil {
		r.metas = map[string]rolloutMeta{}
	}
	seen := make(map[string]bool, len(matches))
	out := make([]storedRollout, 0, len(matches))
	for _, path := range matches {
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		meta, ok := r.metas[path]
		if !ok {
			if meta, ok = readMeta(path); !ok {
				continue
			}
			r.metas[path] = meta
		}
		seen[path] = true
		if meta.user {
			out = append(out, storedRollout{rolloutMeta: meta, updated: info.ModTime().UTC()})
		}
	}
	for path := range r.metas {
		if !seen[path] {
			delete(r.metas, path)
		}
	}
	return out
}

// nativeID maps the id the cockpit holds onto the id codex knows. They are
// the same for every session codex minted for a person; an id nothing maps
// comes back unchanged.
func (r *sessionRepository) nativeID(sessionID string) string {
	id := strings.TrimSpace(sessionID)
	for _, s := range r.stored() {
		if s.cockpit == id {
			return s.id
		}
	}
	return id
}

func (r *sessionRepository) exists(sessionID string) bool {
	for _, s := range r.stored() {
		if s.address() == sessionID {
			return true
		}
	}
	return false
}

// readMeta reads a rollout's first line. A file whose line is not whole yet
// answers false and is read again on the next listing, codex is still
// writing it.
func readMeta(path string) (rolloutMeta, bool) {
	match := rolloutPattern.FindStringSubmatch(filepath.Base(path))
	if match == nil {
		return rolloutMeta{}, false
	}
	f, err := os.Open(path)
	if err != nil {
		return rolloutMeta{}, false
	}
	defer f.Close()
	line, err := bufio.NewReaderSize(io.LimitReader(f, metaLineLimit), 64<<10).ReadBytes('\n')
	if err != nil {
		return rolloutMeta{}, false
	}
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			ID           string          `json:"id"`
			CWD          string          `json:"cwd"`
			ThreadSource string          `json:"thread_source"`
			Source       json.RawMessage `json:"source"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(line, &record); err != nil {
		return rolloutMeta{}, false
	}
	meta := rolloutMeta{id: record.Payload.ID, cwd: coder.NormalizeCWD(strings.TrimSpace(record.Payload.CWD))}
	if id, ok := strings.CutPrefix(record.Payload.ThreadSource, cockpitSource); ok && sessionIDPattern.MatchString(id) {
		meta.cockpit = id
	}
	// A thread an agent of codex opened for itself belongs to the session
	// that started it. Older rollouts say so in an object under source.
	meta.user = record.Type == "session_meta" && meta.id == match[1] && meta.cwd != "" &&
		(record.Payload.ThreadSource == "" || record.Payload.ThreadSource == "user" || meta.cockpit != "") &&
		!strings.HasPrefix(strings.TrimSpace(string(record.Payload.Source)), "{")
	return meta, true
}

func (r *sessionRepository) sessionNames() map[string]string {
	path := filepath.Join(r.home, indexFile)
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	stamp := fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
	r.mu.Lock()
	defer r.mu.Unlock()
	if stamp == r.namesStamp {
		return r.names
	}
	f, err := os.Open(path)
	if err != nil {
		return r.names
	}
	defer f.Close()
	names := map[string]string{}
	scanner := bufio.NewScanner(io.LimitReader(f, indexLimit))
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	for scanner.Scan() {
		var entry struct {
			ID   string `json:"id"`
			Name string `json:"thread_name"`
		}
		if json.Unmarshal(scanner.Bytes(), &entry) == nil && entry.ID != "" {
			names[entry.ID] = strings.Join(strings.Fields(entry.Name), " ")
		}
	}
	r.names, r.namesStamp = names, stamp
	return names
}

// ProcessSession implements coder.LateSessionRecords: codex writes a rollout
// with the first message and holds it open from then on.
func (r *sessionRepository) ProcessSession(pid int) (string, bool) {
	root := filepath.Join(coder.NormalizeCWD(r.home), sessionsDir)
	for _, p := range proctree.Capture().Descendants(pid) {
		for _, path := range proctree.OpenFiles(p) {
			if !filesystem.IsUnder(path, root) {
				continue
			}
			if match := rolloutPattern.FindStringSubmatch(filepath.Base(path)); match != nil {
				return match[1], true
			}
		}
	}
	return "", false
}

// NameSession implements coder.SessionNamer.
func (r *sessionRepository) NameSession(sessionID, name string) error {
	id, err := validSessionID(sessionID)
	if err != nil {
		return err
	}
	return r.name(r.nativeID(id), name)
}

func (r *sessionRepository) DeleteSession(sessionID string) error {
	id, err := validSessionID(sessionID)
	if err != nil {
		return err
	}
	if err := r.remove(r.nativeID(id)); err != nil {
		return err
	}
	if dir, err := r.uploadDir(id); err == nil {
		_ = os.RemoveAll(dir)
	}
	return nil
}

// ListFiles answers no files for a session codex has not written yet, like
// a claude session before its transcript.
func (r *sessionRepository) ListFiles(sessionID string) ([]filesystem.File, error) {
	dir, err := r.filesDir(sessionID)
	if errors.Is(err, coder.ErrNoSessionFiles) {
		return []filesystem.File{}, nil
	}
	if err != nil {
		return nil, err
	}
	return filesystem.ListFiles(dir)
}

func (r *sessionRepository) SaveFile(sessionID, rawName string, src io.Reader) (filesystem.File, error) {
	dir, err := r.filesDir(sessionID)
	if err != nil {
		return filesystem.File{}, err
	}
	return filesystem.SaveFile(dir, rawName, src)
}

func (r *sessionRepository) OpenFile(sessionID, rawName string) (filesystem.OpenedFile, error) {
	dir, err := r.filesDir(sessionID)
	if err != nil {
		return filesystem.OpenedFile{}, err
	}
	return filesystem.OpenFile(dir, rawName)
}

func (r *sessionRepository) DeleteFile(sessionID, rawName string) (filesystem.File, error) {
	dir, err := r.filesDir(sessionID)
	if err != nil {
		return filesystem.File{}, err
	}
	return filesystem.DeleteFile(dir, rawName)
}

// filesDir is where the cockpit keeps the uploaded files of a session codex
// has written. Before its first message a coder runs under the key the
// cockpit minted, which names no session, and a file kept under that key
// would be lost once codex writes the record under an id of its own.
func (r *sessionRepository) filesDir(sessionID string) (string, error) {
	dir, err := r.uploadDir(sessionID)
	if err != nil {
		return "", err
	}
	if _, err := r.rollout(sessionID); err != nil {
		return "", coder.ErrNoSessionFiles
	}
	return dir, nil
}

// uploadDir names a session's upload directory. A codex session is one file,
// so the uploads live in a directory of the cockpit's own inside codex's
// home, named after the session.
func (r *sessionRepository) uploadDir(sessionID string) (string, error) {
	id, err := validSessionID(sessionID)
	if err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(r.home, filesDir, id))
}

func validSessionID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", errors.New("Session identifier is required.")
	}
	if !sessionIDPattern.MatchString(id) {
		return "", errors.New("Invalid session identifier.")
	}
	return id, nil
}
