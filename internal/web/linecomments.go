package web

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

// lineComments keeps the editor's line comments per project: one note on one
// line of one file, so a pass over the code survives a reload and follows the
// user to another device. Every project has a file of its own under
// <state-dir>/line-comments/<project>.json, the notification inbox layout, so
// deleting a project is deleting its file and the backup carries the
// directory. Reads go through the disk on every call like every state file,
// and the lock only orders this process's read-modify-writes. The project is
// always a validated project name (FindByName on every route), a single path
// segment.
type lineComments struct {
	mu  sync.Mutex
	dir string
}

// lineComment is one note: the project relative path, the 1-based line, the
// code line's text as the editor last saw it (what the Markdown export
// quotes, so the export never has to read the files again), and the note
// itself.
type lineComment struct {
	ID        string    `json:"id"`
	Path      string    `json:"path"`
	Line      int       `json:"line"`
	LineText  string    `json:"lineText,omitempty"`
	Text      string    `json:"text"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// errTooManyComments bounds one project's list. The cap is far beyond any
// real pass over a project and only refuses a runaway writer, so the state
// file stays a state file.
var errTooManyComments = errors.New("This project already holds the maximum number of comments.")

const maxLineCommentsPerProject = 500

func newLineComments(stateDir string) *lineComments {
	return &lineComments{dir: filepath.Join(stateDir, "line-comments")}
}

func (l *lineComments) file(project string) string {
	return filepath.Join(l.dir, project+".json")
}

func (l *lineComments) load(project string) []lineComment {
	var list []lineComment
	statefile.Load(l.file(project), &list)
	return list
}

// store writes one project's list, and an emptied list takes the file with
// it: a project without notes has no file, like a coder without events has no
// inbox entries.
func (l *lineComments) store(project string, list []lineComment) {
	if len(list) == 0 {
		l.removeFile(project)
		return
	}
	statefile.Save(l.file(project), 0o644, list)
}

func (l *lineComments) removeFile(project string) bool {
	err := os.Remove(l.file(project))
	if err == nil {
		return true
	}
	if !errors.Is(err, fs.ErrNotExist) {
		log.Printf("line comments: remove %s: %v", l.file(project), err)
	}
	return false
}

// List answers the project's comments ordered the way the sheet reads: by
// file, then by line. A project without any answers empty, never nil.
func (l *lineComments) List(project string) []lineComment {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.load(project)
	if list == nil {
		list = []lineComment{}
	}
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Path != list[j].Path {
			return list[i].Path < list[j].Path
		}
		return list[i].Line < list[j].Line
	})
	return list
}

// Save stores one comment and says whether that changed anything. An entry
// with an id updates the note it names, path and line included, which is what
// lets a renamed file take its notes along; without one a new note is minted.
// An id nobody holds is a note deleted on another device meanwhile, and it is
// not resurrected: the save answers unchanged.
func (l *lineComments) Save(project string, next lineComment) (lineComment, bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.load(project)
	if next.ID == "" {
		if len(list) >= maxLineCommentsPerProject {
			return lineComment{}, false, errTooManyComments
		}
		next.ID = statefile.NewID()
		next.UpdatedAt = time.Now().UTC()
		l.store(project, append(list, next))
		return next, true, nil
	}
	for i := range list {
		if list[i].ID != next.ID {
			continue
		}
		if list[i].Path == next.Path && list[i].Line == next.Line &&
			list[i].LineText == next.LineText && list[i].Text == next.Text {
			return list[i], false, nil
		}
		next.UpdatedAt = time.Now().UTC()
		list[i] = next
		l.store(project, list)
		return next, true, nil
	}
	return lineComment{}, false, nil
}

// Move updates where one file's comments stand after the buffer edited around
// them: line and quoted text per id, nothing else. An entry that moved to
// another file since is left alone, the id has to name a note of this path.
func (l *lineComments) Move(project, path string, positions []lineComment) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.load(project)
	changed := false
	for _, pos := range positions {
		for i := range list {
			if list[i].ID != pos.ID || list[i].Path != path {
				continue
			}
			if list[i].Line != pos.Line || list[i].LineText != pos.LineText {
				list[i].Line = pos.Line
				list[i].LineText = pos.LineText
				list[i].UpdatedAt = time.Now().UTC()
				changed = true
			}
			break
		}
	}
	if changed {
		l.store(project, list)
	}
	return changed
}

// Rename takes a renamed or moved file's comments along to the new path so
// they do not orphan, the folder case included: an exact match takes the new
// path, a note under the old folder keeps its tail. The editor's rename and
// move handlers call it, the one moment the server knows both names.
func (l *lineComments) Rename(project, oldPath, newPath string) bool {
	if oldPath == "" || newPath == "" || oldPath == newPath {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.load(project)
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
		list[i].UpdatedAt = time.Now().UTC()
		changed = true
	}
	if changed {
		l.store(project, list)
	}
	return changed
}

// Remove drops the named comments and answers how many of them stood.
func (l *lineComments) Remove(project string, ids []string) int {
	if len(ids) == 0 {
		return 0
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.load(project)
	kept := list[:0]
	for _, comment := range list {
		if !wanted[comment.ID] {
			kept = append(kept, comment)
		}
	}
	removed := len(list) - len(kept)
	if removed > 0 {
		l.store(project, kept)
	}
	return removed
}

// matchCommentPath answers whether a comment's path matches one path filter
// value: the exact path, everything under it as a directory, or a glob where
// * matches inside one path segment and ** crosses segments, so `internal`
// covers a folder and `**/assistant.go` a name wherever it sits. A value
// that is no valid pattern matches nothing.
func matchCommentPath(pattern, target string) bool {
	pattern = strings.TrimSuffix(pattern, "/")
	if pattern == "" {
		return false
	}
	if !strings.ContainsAny(pattern, "*?[") {
		return target == pattern || strings.HasPrefix(target, pattern+"/")
	}
	return globSegments(strings.Split(pattern, "/"), strings.Split(target, "/"))
}

func globSegments(pattern, parts []string) bool {
	if len(pattern) == 0 {
		return len(parts) == 0
	}
	if pattern[0] == "**" {
		for i := 0; i <= len(parts); i++ {
			if globSegments(pattern[1:], parts[i:]) {
				return true
			}
		}
		return false
	}
	if len(parts) == 0 {
		return false
	}
	if ok, err := path.Match(pattern[0], parts[0]); err != nil || !ok {
		return false
	}
	return globSegments(pattern[1:], parts[1:])
}

func matchesAnyCommentPath(patterns []string, target string) bool {
	for _, pattern := range patterns {
		if matchCommentPath(pattern, target) {
			return true
		}
	}
	return false
}

// RemoveByPaths drops every comment whose file matches one of the path
// filters, ored together, and answers how many stood, which is how a whole
// file or folder is cleared at once.
func (l *lineComments) RemoveByPaths(project string, patterns []string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	list := l.load(project)
	kept := list[:0]
	for _, comment := range list {
		if !matchesAnyCommentPath(patterns, comment.Path) {
			kept = append(kept, comment)
		}
	}
	removed := len(list) - len(kept)
	if removed > 0 {
		l.store(project, kept)
	}
	return removed
}

// Clear drops everything the project holds by removing its file: the sheet's
// delete-all spent the notes, or the project itself is gone. It answers how
// many comments fell with the file.
func (l *lineComments) Clear(project string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.load(project))
	if !l.removeFile(project) {
		return 0
	}
	return n
}
