// Package linecomment keeps the editor's line comments per project: a thread
// on one line of one file, its first note and the replies under it, so a pass
// over the code survives a reload and follows the user to another device. A
// project comment is the same thread on the whole project, without a place.
// A review thread on the forge a project is bound to is a line comment thread
// too, one with a forge origin, kept in step with the forge by SyncForge, and
// a general thread there a project comment.
package linecomment

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// Service holds the comments. Every project has a file of its own under
// <state-dir>/line-comments/<project>.json, the notification inbox layout, so
// deleting a project is deleting its file and the backup carries the
// directory. Reads go through the disk on every call like every state file,
// and the lock only orders this process's read-modify-writes. The project is
// always a validated project name, a single path segment.
type Service struct {
	mu  sync.Mutex
	dir string
}

// Comment is one line comment, a thread: the project relative path, the
// 1-based line, the code line's text as the editor last saw it (what the text
// export quotes, so the export never has to read the files again), the note
// that opened the thread, the replies under it in the order they came and
// whether the thread is resolved. A note a guest wrote carries the name of
// the link it came through as its author and that link's id, which decides
// what the guest may change. A thread of the forge carries its authors there
// and where it came from. The owner's notes carry none of it. UpdatedAt is when
// the first note was last written, a move of its line leaves it. Project marks
// a thread on the whole project, which has no path, no line and no quote and is
// never outdated; a thread keeps what it stands on for good. Was is the id an
// own comment had before its publish gave it the one of its thread of the
// forge, so a page that still holds the old one finds it.
type Comment struct {
	ID        string    `json:"id"`
	Project   bool      `json:"project,omitempty"`
	Path      string    `json:"path"`
	Line      int       `json:"line"`
	LineText  string    `json:"lineText,omitempty"`
	Text      string    `json:"text"`
	Author    string    `json:"author,omitempty"`
	Link      string    `json:"link,omitempty"`
	Forge     *Forge    `json:"forge,omitempty"`
	Replies   []Reply   `json:"replies,omitempty"`
	Resolved  bool      `json:"resolved,omitempty"`
	Was       string    `json:"was,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Reply is one note under the first of a thread, with who wrote it the way a
// comment carries it and when it was last written.
type Reply struct {
	ID        string    `json:"id"`
	Text      string    `json:"text"`
	Author    string    `json:"author,omitempty"`
	Link      string    `json:"link,omitempty"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Writer is who changes line comments: the owner, the zero value, who may
// change every one, or the guest of a link, who writes under the link's name
// and changes only what was written under the same link.
type Writer struct {
	Link string
	Name string
}

// May reports whether w may change c's first note, move it and resolve it.
func (w Writer) May(c Comment) bool {
	return w.wrote(c.Link)
}

// MayReply reports whether w may change or delete the reply r.
func (w Writer) MayReply(r Reply) bool {
	return w.wrote(r.Link)
}

// MayAll reports whether w may change every note of c, which a delete of c
// takes along.
func (w Writer) MayAll(c Comment) bool {
	if !w.May(c) {
		return false
	}
	for _, r := range c.Replies {
		if !w.wrote(r.Link) {
			return false
		}
	}
	return true
}

// MayDelete reports whether w may delete the thread c here, which a thread of
// the forge never is.
func (w Writer) MayDelete(c Comment) bool {
	return c.Forge == nil && w.MayAll(c)
}

func (w Writer) wrote(link string) bool {
	return w.Link == "" || link == w.Link
}

// ErrNotYours refuses a guest's change of a note it did not write.
var ErrNotYours = errors.New("You can change only your own comments.")

// ErrForgeThread refuses a change of a thread of the forge here, which
// changes there.
var ErrForgeThread = errors.New("A review thread of the forge changes there, not here.")

// errTooMany bounds one project's list. The cap is far beyond any real pass
// over a project and only refuses a runaway writer, so the state file stays a
// state file.
var errTooMany = errors.New("This project already holds the maximum number of comments.")

// errPathTooLong refuses a path longer than MaxPath.
var errPathTooLong = errors.New("That file path is too long.")

// errUnread refuses a write over a file that is there but could not be read.
var errUnread = errors.New("The line comments could not be read, nothing was changed.")

// errStandsFor refuses an edit that would move a thread between a line and
// the project.
var errStandsFor = errors.New("A comment stays on what it was made on, a line or the project.")

const (
	// MaxPerProject bounds the notes people write in one project, first
	// notes and replies together, and as many notes of the forge's threads
	// stand beside them.
	MaxPerProject = 1000
	// MaxPath bounds the path a comment takes. No system opens a file by a
	// longer one, so it refuses no file a note could stand on.
	MaxPath = 4 << 10
	// MaxText bounds one stored note. It is far beyond any note a person
	// writes and only refuses a body that cannot be one.
	MaxText = 16 << 10
	// maxQuote bounds the quoted code line. The quote is what the text export
	// cites, one line of code, so anything past this is cut rather than
	// stored: the state file must not grow with a minified file somebody
	// commented on.
	maxQuote = 500
)

// New keeps the comments under the state directory.
func New(stateDir string) *Service {
	return &Service{dir: filepath.Join(stateDir, "line-comments")}
}

func (s *Service) file(project string) string {
	return filepath.Join(s.dir, project+".json")
}

func (s *Service) load(project string) []Comment {
	var list []Comment
	statefile.Load(s.file(project), &list)
	return list
}

// read is load for a write, which must not replace a file it could not read.
func (s *Service) read(project string) ([]Comment, error) {
	var list []Comment
	if err := statefile.Read(s.file(project), &list); err != nil {
		log.Printf("line comments: %v", err)
		return nil, errUnread
	}
	return list, nil
}

// store writes one project's list, and an emptied list takes the file with
// it: a project without notes has no file, like a coder without events has no
// inbox entries.
func (s *Service) store(project string, list []Comment) {
	if len(list) == 0 {
		s.removeFile(project)
		return
	}
	statefile.Save(s.file(project), 0o644, list)
}

func (s *Service) removeFile(project string) bool {
	err := os.Remove(s.file(project))
	if err == nil {
		return true
	}
	if !errors.Is(err, fs.ErrNotExist) {
		log.Printf("line comments: remove %s: %v", s.file(project), err)
	}
	return false
}

// List answers the project's comments ordered the way the sheet reads: the
// project comments first, then by file and by line. A project without any
// answers empty, never nil.
func (s *Service) List(project string) []Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load(project)
	if list == nil {
		list = []Comment{}
	}
	sortComments(list)
	return list
}

func sortComments(list []Comment) {
	sort.SliceStable(list, func(i, j int) bool { return before(list[i], list[j]) })
}

// before is the order of the sheet.
func before(a, b Comment) bool {
	switch {
	case a.Project != b.Project:
		return a.Project
	case a.Path != b.Path:
		return a.Path < b.Path
	}
	return a.Line < b.Line
}

// OnPath reports whether c is a line comment on a file one of the path filter
// values matches. A project comment stands on no file and matches none.
func (c Comment) OnPath(patterns []string) bool {
	return !c.Project && matchesAnyPath(patterns, c.Path)
}

// Save stores the first note of a thread w writes and says whether that
// changed anything. An entry with an id updates the note it names, path and
// line included, which is what lets a renamed file take its notes along, and
// keeps who wrote it, the replies and the resolved state; without one a new
// thread is minted under w's name. An id nobody holds is a thread deleted on
// another device meanwhile, and it is not resurrected: the save answers
// unchanged. A thread of the forge is refused, and so is an edit that would
// move a thread between a line and the project. A project comment never
// stores a place.
func (s *Service) Save(project string, next Comment, w Writer) (Comment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(project)
	if err != nil {
		return Comment{}, false, err
	}
	if next.Project {
		next.Path, next.Line, next.LineText = "", 0, ""
	}
	if next.ID == "" {
		if countOwn(list) >= MaxPerProject {
			return Comment{}, false, errTooMany
		}
		if len(next.Path) > MaxPath {
			return Comment{}, false, errPathTooLong
		}
		next.ID = statefile.NewID()
		next.Author, next.Link = w.Name, w.Link
		next.UpdatedAt = time.Now().UTC()
		s.store(project, append(list, next))
		return next, true, nil
	}
	for i := range list {
		if list[i].ID != next.ID {
			continue
		}
		if list[i].Forge != nil {
			return Comment{}, false, ErrForgeThread
		}
		if !w.May(list[i]) {
			return Comment{}, false, ErrNotYours
		}
		if next.Project != list[i].Project {
			return Comment{}, false, errStandsFor
		}
		// TODO(v2.0.0): a released build took a path of any length, a note
		// keeps the one it holds.
		if next.Path != list[i].Path && len(next.Path) > MaxPath {
			return Comment{}, false, errPathTooLong
		}
		next.Author, next.Link = list[i].Author, list[i].Link
		next.Replies, next.Resolved = list[i].Replies, list[i].Resolved
		if list[i].Path == next.Path && list[i].Line == next.Line &&
			list[i].LineText == next.LineText && list[i].Text == next.Text {
			return list[i], false, nil
		}
		next.UpdatedAt = time.Now().UTC()
		list[i] = next
		s.store(project, list)
		return next, true, nil
	}
	return Comment{}, false, nil
}

// Reply adds a reply w writes to the thread with the id and answers the
// thread, or with an id of its own edits that reply, which keeps who wrote it.
// A thread or reply nobody holds was deleted meanwhile and answers the zero
// comment, nothing is resurrected. A guest edits only its own replies, and a
// thread of the forge takes its replies there.
func (s *Service) Reply(project, thread string, next Reply, w Writer) (Comment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(project)
	if err != nil {
		return Comment{}, false, err
	}
	c, err := own(list, thread)
	if c == nil || err != nil {
		return Comment{}, false, err
	}
	if next.ID == "" {
		if countOwn(list) >= MaxPerProject {
			return Comment{}, false, errTooMany
		}
		next.ID = statefile.NewID()
		next.Author, next.Link = w.Name, w.Link
		next.UpdatedAt = time.Now().UTC()
		c.Replies = append(c.Replies, next)
		s.store(project, list)
		return *c, true, nil
	}
	at := slices.IndexFunc(c.Replies, func(r Reply) bool { return r.ID == next.ID })
	switch {
	case at < 0:
		return Comment{}, false, nil
	case !w.wrote(c.Replies[at].Link):
		return Comment{}, false, ErrNotYours
	case c.Replies[at].Text == next.Text:
		return *c, false, nil
	}
	c.Replies[at].Text = next.Text
	c.Replies[at].UpdatedAt = time.Now().UTC()
	s.store(project, list)
	return *c, true, nil
}

// RemoveReply drops the reply with the id from the thread and answers the
// thread, the zero comment when either is gone. A guest drops only its own.
func (s *Service) RemoveReply(project, thread, id string, w Writer) (Comment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(project)
	if err != nil {
		return Comment{}, false, err
	}
	c, err := own(list, thread)
	if c == nil || err != nil {
		return Comment{}, false, err
	}
	at := slices.IndexFunc(c.Replies, func(r Reply) bool { return r.ID == id })
	if at < 0 {
		return Comment{}, false, nil
	}
	if !w.wrote(c.Replies[at].Link) {
		return Comment{}, false, ErrNotYours
	}
	c.Replies = slices.Delete(c.Replies, at, at+1)
	s.store(project, list)
	return *c, true, nil
}

// Resolve marks the thread resolved, or open again, and answers it, the zero
// comment when it is gone. Who opened a thread resolves it, the owner every
// one, and a thread of the forge resolves there.
func (s *Service) Resolve(project, thread string, resolved bool, w Writer) (Comment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(project)
	if err != nil {
		return Comment{}, false, err
	}
	c, err := own(list, thread)
	switch {
	case c == nil || err != nil:
		return Comment{}, false, err
	case !w.May(*c):
		return Comment{}, false, ErrNotYours
	case c.Resolved == resolved:
		return *c, false, nil
	}
	c.Resolved = resolved
	s.store(project, list)
	return *c, true, nil
}

// own answers the thread with the id in list, nil when there is none, and
// refuses one of the forge.
func own(list []Comment, id string) (*Comment, error) {
	at := slices.IndexFunc(list, func(c Comment) bool { return c.ID == id })
	if at < 0 {
		return nil, nil
	}
	if list[at].Forge != nil {
		return nil, ErrForgeThread
	}
	return &list[at], nil
}

func countOwn(list []Comment) int {
	n := 0
	for _, c := range list {
		if c.Forge == nil {
			n += 1 + len(c.Replies)
		}
	}
	return n
}

// Move updates where one file's comments stand after the buffer edited around
// them: line and quoted text per id, nothing else. An entry that moved to
// another file since is left alone, the id has to name a note of this path,
// and so is one w may not change. A thread of the forge follows its line
// like any other.
func (s *Service) Move(project, path string, positions []Comment, w Writer) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(project)
	if err != nil {
		return false
	}
	changed := false
	for _, pos := range positions {
		for i := range list {
			if list[i].ID != pos.ID || list[i].Path != path || !w.May(list[i]) {
				continue
			}
			if list[i].Line != pos.Line || list[i].LineText != pos.LineText {
				list[i].Line = pos.Line
				list[i].LineText = pos.LineText
				changed = true
			}
			break
		}
	}
	if changed {
		s.store(project, list)
	}
	return changed
}

// Rename takes a renamed or moved file's comments along to the new path so
// they do not orphan, the folder case included: an exact match takes the new
// path, a note under the old folder keeps its tail. The editor's rename and
// move handlers call it, the one moment the server knows both names.
func (s *Service) Rename(project, oldPath, newPath string) bool {
	if oldPath == "" || newPath == "" || oldPath == newPath {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(project)
	if err != nil {
		return false
	}
	changed := false
	for i := range list {
		switch {
		case list[i].Path == oldPath:
			list[i].Path = newPath
		case strings.HasPrefix(list[i].Path, oldPath+"/"):
			list[i].Path = newPath + strings.TrimPrefix(list[i].Path, oldPath)
		default:
			continue
		}
		changed = true
	}
	if changed {
		s.store(project, list)
	}
	return changed
}

// Remove drops the threads drop picks, each with its replies, and answers how
// many fell. A thread of the forge never falls here, SyncForge and DropForge
// take it.
func (s *Service) Remove(project string, drop func(Comment) bool) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(project)
	if err != nil {
		return 0
	}
	kept := list[:0]
	for _, comment := range list {
		if comment.Forge != nil || !drop(comment) {
			kept = append(kept, comment)
		}
	}
	removed := len(list) - len(kept)
	if removed > 0 {
		s.store(project, kept)
	}
	return removed
}

// Clear drops everything the project holds by removing its file, which is
// what a deleted project does to its notes. It answers how many comments fell
// with the file.
func (s *Service) Clear(project string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.load(project))
	if !s.removeFile(project) {
		return 0
	}
	return n
}
