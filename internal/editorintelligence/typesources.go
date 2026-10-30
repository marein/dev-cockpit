package editorintelligence

import (
	"context"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

// imageSourcesBytes bounds the files the image cache keeps, and
// imageReadPaths how many files one answer reads out of an image at once.
const (
	imageSourcesBytes = 64 << 20
	imageReadPaths    = 256
)

// imageSources keeps the files a type search read out of a server's image.
// The searches name the same standard library files again and again, which
// never change under one image, and a container start for each answer would
// hold every keystroke back. The oldest files go first once the cache is
// full.
type imageSources struct {
	mu    sync.Mutex
	files map[string][]string
	order []string
	bytes int
}

func imageSourceKey(image, p string) string {
	return image + "\x00" + p
}

func (is *imageSources) get(image, p string) ([]string, bool) {
	is.mu.Lock()
	defer is.mu.Unlock()
	lines, ok := is.files[imageSourceKey(image, p)]
	return lines, ok
}

func (is *imageSources) put(image, p, text string) []string {
	lines := strings.Split(text, "\n")
	key := imageSourceKey(image, p)
	is.mu.Lock()
	defer is.mu.Unlock()
	if is.files == nil {
		is.files = map[string][]string{}
	}
	if _, ok := is.files[key]; ok {
		return is.files[key]
	}
	for is.bytes+len(text) > imageSourcesBytes && len(is.order) > 0 {
		oldest := is.files[is.order[0]]
		for _, line := range oldest {
			is.bytes -= len(line) + 1
		}
		delete(is.files, is.order[0])
		is.order = is.order[1:]
	}
	is.files[key] = lines
	is.order = append(is.order, key)
	is.bytes += len(text) + 1
	return lines
}

// sourceLines reads the lines a language's TypeOf asks for, each file once
// per answer: a file of the project, or a source under one of the server's
// roots outside it. Those come through the launcher, all of one answer at
// once, see fetch, and the ones of an image stay in the service's cache. A
// file that cannot be read answers no line.
type sourceLines struct {
	root     string
	roots    []SourceRoot
	images   *imageSources
	launcher Launcher
	files    map[string][]string
	// missing are the files outside the project asked for before fetch
	// read them, by root.
	missing map[SourceRoot]map[string]bool
}

func newSourceLines(root string, roots []SourceRoot, images *imageSources, launcher Launcher) *sourceLines {
	return &sourceLines{root: root, roots: roots, images: images, launcher: launcher, files: map[string][]string{}, missing: map[SourceRoot]map[string]bool{}}
}

// line answers the 1-based line n of the file at p, a project path or an
// absolute one under a root. missing reports a file outside the project
// that is not read yet, which fetch reads.
func (sl *sourceLines) line(p string, n int) (text string, ok, missing bool) {
	content, read := sl.files[p]
	if !read && !path.IsAbs(p) {
		if text, _, err := filesystem.ReadFileText(sl.root, p); err == nil {
			content = strings.Split(text, "\n")
		}
		sl.files[p], read = content, true
	}
	if !read {
		root, ok := FindSourceRoot(sl.roots, p)
		if !ok {
			return "", false, false
		}
		if lines, ok := sl.images.get(root.Image, p); ok && root.Image != "" {
			sl.files[p] = lines
			content = lines
		} else {
			if sl.missing[root] == nil {
				sl.missing[root] = map[string]bool{}
			}
			sl.missing[root][p] = true
			return "", false, true
		}
	}
	if n < 1 || n > len(content) {
		return "", false, false
	}
	return content[n-1], true, false
}

// fetch reads the files line found missing, one call of the launcher per
// root and at most imageReadPaths files each. A file it could not read
// stays without lines.
func (sl *sourceLines) fetch(ctx context.Context) {
	for root, wanted := range sl.missing {
		paths := make([]string, 0, len(wanted))
		for p := range wanted {
			paths = append(paths, p)
		}
		sort.Strings(paths)
		if len(paths) > imageReadPaths {
			paths = paths[:imageReadPaths]
		}
		texts, _ := sl.launcher.ReadSources(ctx, root, paths)
		for _, p := range paths {
			text, ok := texts[p]
			switch {
			case !ok:
				sl.files[p] = nil
			case root.Image != "":
				sl.files[p] = sl.images.put(root.Image, p, text)
			default:
				sl.files[p] = strings.Split(text, "\n")
			}
		}
	}
	sl.missing = map[SourceRoot]map[string]bool{}
}
