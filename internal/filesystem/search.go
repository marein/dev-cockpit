package filesystem

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
)

// MaxSearchMatches caps how many matching lines a project search returns. This
// is a limit on the answer, not on the search: every file is scanned, and this
// only decides how much of the result travels to the browser.
const MaxSearchMatches = 200

// maxSearchFileBytes caps how large a file may be to be scanned by search. It
// keeps megabyte blobs out of a line oriented search; it is not a limit on how
// much of the project gets searched.
const maxSearchFileBytes = 1 << 20

// maxSnippetBytes caps how much of a matching line is returned.
const maxSnippetBytes = 200

// searchBatchFiles is how many files are handed to the workers at a time.
// Batching is what keeps the answer identical to a plain in-order scan: files
// are collected in the walk's lexical order, a whole batch is scanned in
// parallel, and only between batches can the search conclude it has enough.
// Larger batches parallelise better but scan further past the point where the
// cap was already reached.
const searchBatchFiles = 256

// SearchMatch is one matching line of a project wide text search.
type SearchMatch struct {
	Path string `json:"path"`
	Line int    `json:"line"`
	Text string `json:"text"`
}

// SearchFiles scans every regular file under root for a case insensitive
// substring match, staying out of the excluded directories and ignoring binary
// and oversized files.
//
// It used to stop after the first 5000 files as well as after 200 matches, which
// meant a search could report "no matches" while the file it was looking for sat
// unread a few thousand entries further down. In a large project, searching for a
// symbol in your own code spent the whole budget inside generated code and
// answered, quickly and confidently, that nothing matched. The file limit is
// gone; only the answer is capped.
//
// Files are read on a pool of workers because the scan is bound by reading tens
// of thousands of files rather than by matching bytes. The result is still the
// first MaxSearchMatches matches in path order, exactly what a single threaded
// in-order scan would have returned.
func SearchFiles(root, query string, ex Exclusions) ([]SearchMatch, bool, error) {
	matches := []SearchMatch{}
	needle := strings.ToLower(strings.TrimSpace(query))
	if needle == "" {
		return matches, false, nil
	}
	needleBytes := []byte(needle)
	workers := runtime.NumCPU()

	batch := make([]string, 0, searchBatchFiles)
	stoppedEarly := false

	// scanCollected scans what has been gathered so far and reports whether the
	// search already holds at least as much as it is allowed to return.
	scanCollected := func() bool {
		if len(batch) == 0 {
			return false
		}
		matches = append(matches, scanBatch(root, batch, needleBytes, workers)...)
		batch = batch[:0]
		return len(matches) >= MaxSearchMatches
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			if path == root {
				return walkErr
			}
			return nil
		}
		if d.IsDir() {
			if path != root && ex.SkipDir(relTo(root, path), d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > maxSearchFileBytes {
			return nil
		}
		batch = append(batch, path)
		if len(batch) < searchBatchFiles {
			return nil
		}
		if scanCollected() {
			stoppedEarly = true
			return filepath.SkipAll
		}
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	if !stoppedEarly {
		scanCollected()
	}

	// Workers finish in whatever order the disk hands files back, so the answer
	// is ordered here. Without this the same query could come back as a
	// different hundred lines on every run.
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		return matches[i].Line < matches[j].Line
	})

	truncated := stoppedEarly || len(matches) > MaxSearchMatches
	if len(matches) > MaxSearchMatches {
		matches = matches[:MaxSearchMatches]
	}
	return matches, truncated, nil
}

// scanBatch reads and scans the given files across workers and returns every
// match found in them.
func scanBatch(root string, paths []string, needle []byte, workers int) []SearchMatch {
	if workers < 1 {
		workers = 1
	}
	if workers > len(paths) {
		workers = len(paths)
	}
	var (
		next atomic.Int64
		mu   sync.Mutex
		all  []SearchMatch
		wg   sync.WaitGroup
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			// One reusable read buffer and one reusable lowercase buffer per
			// worker, rather than two fresh copies of every file scanned.
			var data, lowered []byte
			local := make([]SearchMatch, 0, 8)
			for {
				i := int(next.Add(1)) - 1
				if i >= len(paths) {
					break
				}
				path := paths[i]
				var err error
				data, err = readInto(path, data)
				if err != nil {
					continue
				}
				if bytes.IndexByte(data, 0) >= 0 {
					continue // binary
				}
				lowered = appendLowerBytes(lowered[:0], data)
				if !bytes.Contains(lowered, needle) {
					continue
				}
				local = appendFileMatches(local, data, lowered, relTo(root, path), needle)
			}
			if len(local) > 0 {
				mu.Lock()
				all = append(all, local...)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return all
}

// readInto reads path into buf, growing it when needed, and returns the slice
// holding the file. Reusing one buffer across files is what keeps a full tree
// scan from allocating the whole tree.
func readInto(path string, buf []byte) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return buf, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return buf, err
	}
	size := info.Size()
	if size > maxSearchFileBytes {
		// The walk already filtered on size; a file that grew in between is
		// skipped rather than read past the limit.
		return buf, io.EOF
	}
	if int64(cap(buf)) < size {
		buf = make([]byte, size, size+size/4+64)
	}
	buf = buf[:size]
	if _, err := io.ReadFull(f, buf); err != nil {
		return buf, err
	}
	return buf, nil
}

// appendFileMatches records one match per matching line, which is what the
// palette lists. lowered is data lowercased, so the two share offsets: the
// search runs over the lowercased copy while the line is cut out of the
// original bytes.
func appendFileMatches(into []SearchMatch, data, lowered []byte, rel string, needle []byte) []SearchMatch {
	line := 1
	counted := 0 // offset up to which newlines have already been counted
	from := 0
	for from <= len(lowered) {
		idx := bytes.Index(lowered[from:], needle)
		if idx < 0 {
			return into
		}
		idx += from
		line += bytes.Count(lowered[counted:idx], newline)
		counted = idx

		start := bytes.LastIndexByte(data[:idx], '\n') + 1
		end := len(data)
		if nl := bytes.IndexByte(data[idx:], '\n'); nl >= 0 {
			end = idx + nl
		}
		into = append(into, SearchMatch{Path: rel, Line: line, Text: searchSnippet(data[start:end], string(needle))})

		// One hit per line: carry on after the end of this one.
		if end >= len(data) {
			return into
		}
		from = end + 1
	}
	return into
}

var newline = []byte{'\n'}

// appendLowerBytes is appendLowerASCII over a byte slice; see its sibling in
// quickopen.go. Lowercasing ASCII in place preserves length, which is what lets
// offsets in the lowercased copy address the original bytes.
func appendLowerBytes(dst, src []byte) []byte {
	if cap(dst) < len(src) {
		dst = make([]byte, 0, len(src)+len(src)/4+64)
	}
	for _, c := range src {
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		dst = append(dst, c)
	}
	return dst
}

// searchSnippet trims a matching line for transport, keeping a window around
// the first match when the line is longer than maxSnippetBytes.
func searchSnippet(line []byte, needle string) string {
	text := strings.TrimSpace(string(line))
	idx := strings.Index(strings.ToLower(text), needle)
	if idx < 0 {
		idx = 0
	}
	return SnippetAround(text, idx)
}

// SnippetAround trims a line for transport, keeping a window around the byte
// offset idx when the line is longer than the snippet cap. The search cuts
// around its match and the LSP usage previews around the usage's column, so
// the two lists that share one look share one cutting rule; idx counts into
// the given text, leading whitespace included, and is clamped.
func SnippetAround(text string, idx int) string {
	trimmed := strings.TrimSpace(text)
	idx -= strings.Index(text, trimmed)
	text = trimmed
	if len(text) > maxSnippetBytes {
		if idx < 0 {
			idx = 0
		}
		if idx > len(text) {
			idx = len(text)
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
