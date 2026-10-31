package editorintelligence

import (
	"encoding/json"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
)

// ownScanMaxBytes bounds a file the scan reads, typeDocumentsPerSearch how
// many files one type search opens to read their document symbols, and
// ownFilesKept how many files the cache holds before it starts over.
const (
	ownScanMaxBytes        = 1 << 20
	typeDocumentsPerSearch = 16
	ownFilesKept           = 200000
)

// crowdedPlan is what a type search adds when its first request filled the
// server's limit: requests for the namespaces the project's own types stand
// under, the files it opens now to read the types their document symbols
// name, the types such files named before, and whether a bound left own
// types for a later search.
type crowdedPlan struct {
	asks      []symbolAsk
	documents []ownDocument
	types     []TypeSymbol
	cut       bool
}

// ownDocument is a file of the project a type search opens, with the text
// and the stamp it was read at.
type ownDocument struct {
	rel   string
	text  string
	size  int64
	mtime time.Time
}

// ownFiles keeps what type searches read off the project's own files, by
// absolute path and only for as long as the file keeps the size and the
// modification time it was read at: the namespaces it declares, whether it
// may declare a type in the global namespace, and the types its document
// symbols named once a search asked.
type ownFiles struct {
	mu    sync.Mutex
	files map[string]*ownFile
}

type ownFile struct {
	size       int64
	mtime      time.Time
	namespaces []string
	global     bool
	types      []TypeSymbol
	read       bool
}

// ownWalk is how a type search walks the project's own files: from its
// root, past the folders skip names, keeping those of the language owns
// names.
type ownWalk struct {
	root string
	skip func(rel, name string) bool
	owns func(rel string) bool
}

// ownScan is what one walk over the project's own files found.
type ownScan struct {
	files      []ownEntry
	namespaces []string
	documents  []ownDocument
	types      []TypeSymbol
	cut        bool
}

// ownEntry is one of the project's own files a scan walked, with the
// namespaces it declares.
type ownEntry struct {
	rel        string
	namespaces []string
}

var (
	phpNamespaceDeclaration = regexp.MustCompile(`(?mi)(?:^|<\?php)[ \t]*namespace[ \t]+\\?([\p{L}_][\p{L}\p{N}_\\]*)[ \t]*[;{]`)
	phpGlobalBlock          = regexp.MustCompile(`(?mi)(?:^|<\?php)[ \t]*namespace[ \t]*\{`)
	phpTypeKeyword          = regexp.MustCompile(`(?i)\b(?:class|interface|trait|enum)[ \t]+[\p{L}_]`)
)

// scan walks the project's own PHP files, as far as the walk of the anchor
// reaches, and answers the namespaces they
// declare and the files that may declare a type outside every namespace,
// which no query reaches: the types of those read before, and the others to
// open now, those whose name holds one of the words first, at most
// typeDocumentsPerSearch. A file is read again only once its size or its
// modification time moved.
func (of *ownFiles) scan(walk ownWalk, words []string) ownScan {
	var found ownScan
	var unread []ownDocument
	namespaces := map[string]bool{}
	dirs := []string{"."}
	scanned := 0
	of.mu.Lock()
	if of.files == nil || len(of.files) > ownFilesKept {
		of.files = map[string]*ownFile{}
	}
	of.mu.Unlock()
	for len(dirs) > 0 && !found.cut {
		var next []string
		for _, dir := range dirs {
			entries, err := os.ReadDir(filepath.Join(walk.root, dir))
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if scanned++; scanned > anchorScanCap {
					found.cut = true
					break
				}
				rel := path.Join(dir, entry.Name())
				if entry.IsDir() {
					if !walk.skip(rel, entry.Name()) {
						next = append(next, rel)
					}
					continue
				}
				if !walk.owns(rel) || !entry.Type().IsRegular() {
					continue
				}
				info, err := entry.Info()
				if err != nil || info.Size() > ownScanMaxBytes {
					continue
				}
				file := of.file(filepath.Join(walk.root, filepath.FromSlash(rel)), info)
				found.files = append(found.files, ownEntry{rel: rel, namespaces: file.namespaces})
				for _, namespace := range file.namespaces {
					namespaces[namespace] = true
				}
				switch {
				case !file.global:
				case file.read:
					found.types = append(found.types, file.types...)
				default:
					unread = append(unread, ownDocument{rel: rel, size: info.Size(), mtime: info.ModTime()})
				}
			}
			if found.cut {
				break
			}
		}
		dirs = next
	}
	for namespace := range namespaces {
		found.namespaces = append(found.namespaces, namespace)
	}
	sort.Strings(found.namespaces)
	named := func(doc ownDocument) bool {
		name := strings.ToLower(path.Base(doc.rel))
		return slices.ContainsFunc(words, func(word string) bool { return word != "" && strings.Contains(name, word) })
	}
	sort.SliceStable(unread, func(i, j int) bool {
		if named(unread[i]) != named(unread[j]) {
			return named(unread[i])
		}
		return unread[i].rel < unread[j].rel
	})
	if len(unread) > typeDocumentsPerSearch {
		unread, found.cut = unread[:typeDocumentsPerSearch], true
	}
	for _, doc := range unread {
		if text, _, err := filesystem.ReadFileText(walk.root, doc.rel); err == nil {
			doc.text = text
			found.documents = append(found.documents, doc)
		}
	}
	return found
}

// file answers what the cache knows of the file, reading it again when its
// stamp moved. A file that cannot be read declares nothing.
func (of *ownFiles) file(abs string, info os.FileInfo) ownFile {
	of.mu.Lock()
	cached, ok := of.files[abs]
	of.mu.Unlock()
	if ok && cached.size == info.Size() && cached.mtime.Equal(info.ModTime()) {
		return *cached
	}
	file := &ownFile{size: info.Size(), mtime: info.ModTime()}
	if data, err := os.ReadFile(abs); err == nil {
		text := string(data)
		for _, match := range phpNamespaceDeclaration.FindAllStringSubmatch(text, -1) {
			file.namespaces = append(file.namespaces, strings.ToLower(match[1]))
		}
		file.global = (len(file.namespaces) == 0 || phpGlobalBlock.MatchString(text)) && phpTypeKeyword.MatchString(text)
	}
	of.mu.Lock()
	of.files[abs] = file
	of.mu.Unlock()
	return *file
}

// remember keeps the types a file's document symbols named, as long as the
// file still has the stamp its text was read at.
func (of *ownFiles) remember(abs string, doc ownDocument, types []TypeSymbol) {
	of.mu.Lock()
	defer of.mu.Unlock()
	if file, ok := of.files[abs]; ok && file.size == doc.size && file.mtime.Equal(doc.mtime) {
		file.types, file.read = types, true
	}
}

// phpDocumentTypes reads the types intelephense's document symbols name in
// one file: the declarations of its PHP code, each on its name, inside the
// namespace a bracketed block puts it in. What an HTML part of the file
// declares, a script's class or a style's selector, comes in the plain
// shape and is no PHP type.
func phpDocumentTypes(raw json.RawMessage, rel, label string) ([]TypeSymbol, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return nil, nil
	}
	var symbols []documentSymbol
	if err := json.Unmarshal(raw, &symbols); err != nil {
		return nil, err
	}
	var types []TypeSymbol
	var walk func(symbols []documentSymbol, container string)
	walk = func(symbols []documentSymbol, container string) {
		for _, sym := range symbols {
			kind, isType := phpTypeKinds[sym.Kind]
			switch {
			case sym.Location != nil || sym.SelectionRange == nil:
			case sym.Kind == symbolNamespace:
				walk(sym.Children, sym.Name)
			case isType && phpNamespaceName(sym.Name):
				start := sym.SelectionRange.Start
				types = append(types, TypeSymbol{
					Location:  Location{Path: rel, Line: start.Line + 1, Character: start.Character},
					Name:      sym.Name,
					Kind:      kind,
					Container: container,
					Language:  label,
				})
			}
		}
	}
	walk(symbols, "")
	return types, nil
}
