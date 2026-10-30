package editorintelligence

import (
	"os"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

// libraryFilesFor bounds how long a walk's answer whether a project holds
// files of some extensions stands, as long as quick open's index does.
const libraryFilesFor = 30 * time.Second

// libraryFiles keeps per project root and extensions whether the project's
// own files hold one of them, see Profile.LibraryFiles.
type libraryFiles struct {
	mu   sync.Mutex
	seen map[string]libraryFilesSeen
}

type libraryFilesSeen struct {
	held bool
	at   time.Time
}

// held answers whether the project holds a file of one of the extensions
// outside git's folder and those dependencies are installed in, whatever
// the search settings exclude, since the server sees those files too. A
// walk that stops at its bound before it found one answers yes: an unknown
// project gets every ask.
func (lf *libraryFiles) held(root string, exts []string, now time.Time) bool {
	key := root + "\x00" + strings.Join(exts, ",")
	lf.mu.Lock()
	seen, ok := lf.seen[key]
	lf.mu.Unlock()
	if ok && now.Sub(seen.at) < libraryFilesFor {
		return seen.held
	}
	_, found, complete := firstFile(root, func(_, name string) bool {
		return name == ".git" || dependencyDir(name)
	}, func(rel string, _ os.DirEntry) bool {
		ext := strings.ToLower(path.Ext(rel))
		return ext != "" && slices.Contains(exts, ext[1:])
	})
	held := found || !complete
	lf.mu.Lock()
	defer lf.mu.Unlock()
	if lf.seen == nil {
		lf.seen = map[string]libraryFilesSeen{}
	}
	for k, old := range lf.seen {
		if now.Sub(old.at) >= libraryFilesFor {
			delete(lf.seen, k)
		}
	}
	lf.seen[key] = libraryFilesSeen{held: held, at: now}
	return held
}
