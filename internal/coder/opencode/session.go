package opencode

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/clirun"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/filesystem"
)

// metadataKey is the key a pre-created conversation session carries in its
// metadata JSON, holding the id the cockpit addresses it by. opencode stores
// the metadata verbatim and never touches it, so the mapping lives on the
// session itself: it survives every restart and disappears with the session.
const metadataKey = "devCockpitSessionID"

// dbFileName is the one database opencode keeps everything in, under its
// data directory (verified on 1.18.23, `opencode db path`).
const dbFileName = "opencode.db"

// sessionIDPattern is the alphabet a session id may use before it is embedded
// into a query. It is the tmux-safe identifier alphabet, which both id kinds
// fit: opencode's own `ses_…` ids and the UUIDs the cockpit mints. Nothing
// outside it, most of all no quote, ever reaches SQL.
var sessionIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// defaultTitlePattern is the placeholder opencode gives a session nobody
// named ("New session - <timestamp>", "Child session - " for subagents). It
// is machine text, so the list treats such a session as unnamed.
var defaultTitlePattern = regexp.MustCompile(`^(New|Child) session - \d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$`)

// listQuery reads every root session. Subagent sessions carry a parent and
// belong to their parent's conversation; archived sessions are opencode's
// attic. The cockpit id rides along where a conversation session carries one.
const listQuery = `SELECT s.id AS id, s.title AS title, s.directory AS directory, s.time_updated AS time_updated,` +
	` json_extract(s.metadata,'$.` + metadataKey + `') AS cockpit` +
	` FROM session s WHERE s.parent_id IS NULL AND s.time_archived IS NULL ORDER BY s.time_updated DESC`

type sessionRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Directory   string `json:"directory"`
	TimeUpdated int64  `json:"time_updated"`
	Cockpit     string `json:"cockpit"`
}

type storedSession struct {
	coder.Session
	nativeID string
}

// sessionRepository reads opencode's session table, directly and read only,
// see db.go; query is injectable, so the tests feed recorded answers instead
// of touching a database. The parsed list is cached against the database
// files, which is what keeps repeated renders from re-reading and re-parsing
// an unmoved store, and what the missing-database gate hangs on.
type sessionRepository struct {
	dataRoot string
	query    func(sql string) ([]byte, error)
	remove   func(nativeID string) error

	mu     sync.Mutex
	stamp  string
	cached []storedSession
	loaded bool
}

// removeSession deletes through the CLI's own command, which cascades onto
// the session's messages and parts.
func removeSession(nativeID string) error {
	return clirun.Check("opencode", "session", "delete", nativeID)
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
	id, err := validSessionID(sessionID)
	if err != nil {
		return err
	}
	if err := r.remove(r.nativeID(id)); err != nil {
		return err
	}
	if dir, err := r.filesDir(id); err == nil {
		_ = os.RemoveAll(dir)
	}
	r.invalidate()
	return nil
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

// filesDir is where the cockpit keeps a session's uploaded files. opencode
// has no per-session directory at all, everything of a session is rows, so
// the uploads live under a directory that says whose they are, inside
// opencode's data directory next to the database.
func (r *sessionRepository) filesDir(sessionID string) (string, error) {
	id, err := validSessionID(sessionID)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(r.dataRoot, "dev-cockpit-files", id)
	absRoot, _ := filepath.Abs(r.dataRoot)
	absDir, _ := filepath.Abs(dir)
	if !filesystem.IsUnder(absDir, absRoot) {
		return "", fmt.Errorf("Invalid session identifier.")
	}
	return absDir, nil
}

// nativeID maps the id the cockpit holds onto the id opencode knows. They are
// the same for every session opencode created itself; only a conversation
// session pre-created for the assistant is listed under the cockpit's id,
// with opencode's own riding in the store. An id nothing maps comes back
// unchanged, the caller's error then names the id that was asked for.
func (r *sessionRepository) nativeID(sessionID string) string {
	id := strings.TrimSpace(sessionID)
	if strings.HasPrefix(id, "ses") {
		return id
	}
	for _, s := range r.listStored() {
		if s.SessionID == id {
			return s.nativeID
		}
	}
	return id
}

// exists answers whether the store lists the session, under the id the
// cockpit addresses it by.
func (r *sessionRepository) exists(sessionID string) bool {
	for _, s := range r.listStored() {
		if s.SessionID == sessionID {
			return true
		}
	}
	return false
}

func (r *sessionRepository) listStored() []storedSession {
	stamp, ok := r.dbStamp()
	if !ok {
		// No database means opencode has never run here, and there is
		// nothing to ask.
		r.invalidate()
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.loaded || stamp != r.stamp {
		out, err := r.query(listQuery)
		if err != nil {
			// A failed reading is not an empty store: answer nothing now and
			// ask again next time.
			return nil
		}
		var rows []sessionRow
		if err := json.Unmarshal(out, &rows); err != nil {
			return nil
		}
		r.cached = parseRows(rows)
		r.stamp = stamp
		r.loaded = true
	}
	// The directory check runs on every scan, cached entries included, so a
	// session whose project directory vanished drops out at once.
	out := make([]storedSession, 0, len(r.cached))
	for _, s := range r.cached {
		if info, err := os.Stat(s.CWD); err != nil || !info.IsDir() {
			continue
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return coder.LessSession(out[i].Session, out[j].Session) })
	return out
}

func parseRows(rows []sessionRow) []storedSession {
	out := make([]storedSession, 0, len(rows))
	for _, row := range rows {
		id := strings.TrimSpace(row.ID)
		if id == "" || strings.TrimSpace(row.Directory) == "" {
			continue
		}
		sessionID := id
		if cockpit := strings.TrimSpace(row.Cockpit); cockpit != "" {
			if _, err := validSessionID(cockpit); err == nil {
				sessionID = cockpit
			}
		}
		title := strings.TrimSpace(row.Title)
		if defaultTitlePattern.MatchString(title) {
			title = ""
		}
		out = append(out, storedSession{
			Session: coder.Session{
				SessionID: sessionID,
				Name:      coder.DisplayName(title, sessionID),
				CWD:       coder.NormalizeCWD(row.Directory),
				UpdatedAt: time.UnixMilli(row.TimeUpdated).UTC(),
			},
			nativeID: id,
		})
	}
	return out
}

// dbStamp identifies the database's current state cheaply: the main file and
// its write-ahead log, by size and modification time. A moved stamp is what
// makes the next scan re-read.
func (r *sessionRepository) dbStamp() (string, bool) {
	path := filepath.Join(r.dataRoot, dbFileName)
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	stamp := fmt.Sprintf("%d:%d", info.ModTime().UnixNano(), info.Size())
	if wal, err := os.Stat(path + "-wal"); err == nil {
		stamp += fmt.Sprintf("/%d:%d", wal.ModTime().UnixNano(), wal.Size())
	}
	return stamp, true
}

func (r *sessionRepository) invalidate() {
	r.mu.Lock()
	r.loaded = false
	r.cached = nil
	r.mu.Unlock()
}

func validSessionID(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if id == "" {
		return "", fmt.Errorf("Session identifier is required.")
	}
	if !sessionIDPattern.MatchString(id) {
		return "", fmt.Errorf("Invalid session identifier.")
	}
	return id, nil
}
