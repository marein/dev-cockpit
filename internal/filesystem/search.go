package filesystem

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// MaxSearchMatches caps how many matching lines a project search returns.
const MaxSearchMatches = 200

// maxSearchFileBytes caps how large a file may be to be scanned by search.
const maxSearchFileBytes = 1 << 20

// maxSnippetBytes caps how much of a matching line is returned.
const maxSnippetBytes = 200

// SearchMatch is one matching line of a project wide text search.
type SearchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// SearchFiles scans every regular file under root for a case insensitive
// substring match. It walks with the same skip list as ListFilesRecursive,
// ignores binary and oversized files, and stops after MaxSearchMatches
// matches or MaxListedFiles files, reporting truncation.
func SearchFiles(root, query string) ([]SearchMatch, bool, error) {
	matches := []SearchMatch{}
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return matches, false, nil
	}
	needleBytes := []byte(needle)
	truncated := false
	errStop := errors.New("stop walking")
	scanned := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			return nil
		}
		if d.IsDir() {
			if path != root && skippedDirs[d.Name()] {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		scanned++
		if scanned > MaxListedFiles {
			truncated = true
			return errStop
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxSearchFileBytes {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil
		}
		if !bytes.Contains(bytes.ToLower(data), needleBytes) {
			return nil
		}
		rel := relTo(root, path)
		for i, line := range bytes.Split(data, []byte("\n")) {
			if !bytes.Contains(bytes.ToLower(line), needleBytes) {
				continue
			}
			matches = append(matches, SearchMatch{Path: rel, Line: i + 1, Text: searchSnippet(line, needle)})
			if len(matches) >= MaxSearchMatches {
				truncated = true
				return errStop
			}
		}
		return nil
	})
	if err != nil && !errors.Is(err, errStop) {
		return nil, false, err
	}
	return matches, truncated, nil
}

// searchSnippet trims a matching line for transport, keeping a window around
// the first match when the line is longer than maxSnippetBytes.
func searchSnippet(line []byte, needle string) string {
	text := strings.TrimSpace(string(line))
	if len(text) > maxSnippetBytes {
		idx := strings.Index(strings.ToLower(text), needle)
		if idx < 0 {
			idx = 0
		}
		start := idx - 60
		if start < 0 {
			start = 0
		}
		end := start + maxSnippetBytes
		if end > len(text) {
			end = len(text)
			start = end - maxSnippetBytes
		}
		text = strings.TrimSpace(text[start:end])
	}
	return strings.ToValidUTF8(text, "�")
}
