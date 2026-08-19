package filesystem

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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

// SearchMatch is one matching line of a project wide text search. MatchStart
// and MatchLen locate the hit inside Text, in UTF-16 units, the coordinate the
// browser slices strings in, so the palette can mark the hit by position
// instead of searching the snippet again.
type SearchMatch struct {
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Text       string `json:"text"`
	MatchStart int    `json:"start"`
	MatchLen   int    `json:"len"`
}

// A matcher is one strategy for finding hits in a file. enter hands it the
// next file's bytes, find answers the next hit at or after from as a
// [start, end) pair in those bytes, start -1 when nothing further matches.
// Matchers may keep buffers, so every worker holds one of its own.
type matcher interface {
	enter(data []byte)
	find(from int) (start, end int)
}

// literalMatcher finds a case insensitive substring. It lowercases each file
// into a reusable buffer; lowering ASCII preserves length, which is what lets
// offsets in the lowered copy address the original bytes.
type literalMatcher struct {
	needle  []byte
	lowered []byte
}

func (m *literalMatcher) enter(data []byte) {
	m.lowered = appendLowerBytes(m.lowered[:0], data)
}

func (m *literalMatcher) find(from int) (int, int) {
	idx := bytes.Index(m.lowered[from:], m.needle)
	if idx < 0 {
		return -1, -1
	}
	return from + idx, from + idx + len(m.needle)
}

// regexpMatcher finds RE2 matches on the original bytes, (?i) covers the case
// folding there. The dot does not cross newlines by default, so the line model
// of the search stands.
type regexpMatcher struct {
	re   *regexp.Regexp
	data []byte
}

func (m *regexpMatcher) enter(data []byte) {
	m.data = data
}

func (m *regexpMatcher) find(from int) (int, int) {
	loc := m.re.FindIndex(m.data[from:])
	if loc == nil {
		return -1, -1
	}
	return from + loc[0], from + loc[1]
}

// newMatcherFactory answers a constructor for per worker matchers. A regex
// pattern is compiled once here, so a broken one fails the search before any
// file is read, with the compile message worth showing in the palette. (?m)
// joins (?i) because find continues from an offset behind the previous hit's
// line: without it ^ and $ would anchor at whatever offset that left behind
// instead of at line boundaries, which is what a line oriented search means
// by them.
func newMatcherFactory(query string, useRegex bool) (func() matcher, error) {
	if useRegex {
		re, err := regexp.Compile("(?im)" + query)
		if err != nil {
			// The message quotes the pattern, so the error of a bare compile
			// is the one to show: nobody typed the injected flags.
			if _, bare := regexp.Compile(query); bare != nil {
				return nil, bare
			}
			return nil, err
		}
		return func() matcher { return &regexpMatcher{re: re} }, nil
	}
	needle := []byte(strings.ToLower(query))
	return func() matcher { return &literalMatcher{needle: needle} }, nil
}

// SearchFiles scans every regular file under root for a case insensitive
// substring match, or with useRegex for a case insensitive RE2 match, staying
// out of the excluded directories and ignoring binary and oversized files.
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
func SearchFiles(root, query string, useRegex bool, ex Exclusions) ([]SearchMatch, bool, error) {
	matches := []SearchMatch{}
	query = strings.TrimSpace(query)
	if query == "" {
		return matches, false, nil
	}
	newMatcher, err := newMatcherFactory(query, useRegex)
	if err != nil {
		return nil, false, err
	}
	workers := runtime.NumCPU()

	batch := make([]string, 0, searchBatchFiles)
	stoppedEarly := false

	// scanCollected scans what has been gathered so far and reports whether the
	// search already holds at least as much as it is allowed to return.
	scanCollected := func() bool {
		if len(batch) == 0 {
			return false
		}
		matches = append(matches, scanBatch(root, batch, newMatcher, workers)...)
		batch = batch[:0]
		return len(matches) >= MaxSearchMatches
	}

	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
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
func scanBatch(root string, paths []string, newMatcher func() matcher, workers int) []SearchMatch {
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
			// One reusable read buffer per worker, plus whatever buffers the
			// matcher keeps, rather than fresh copies of every file scanned.
			var data []byte
			m := newMatcher()
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
				m.enter(data)
				local = appendFileMatches(local, data, relTo(root, path), m)
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
// palette lists: the scan continues behind the hit's line. That jump is also
// what keeps an empty regex match like a* from standing still, the offset
// always leaves the line it hit.
func appendFileMatches(into []SearchMatch, data []byte, rel string, m matcher) []SearchMatch {
	line := 1
	counted := 0 // offset up to which newlines have already been counted
	from := 0
	for from <= len(data) {
		start, end := m.find(from)
		if start < 0 {
			return into
		}
		line += bytes.Count(data[counted:start], newline)
		counted = start

		lineStart := bytes.LastIndexByte(data[:start], '\n') + 1
		lineEnd := len(data)
		if nl := bytes.IndexByte(data[start:], '\n'); nl >= 0 {
			lineEnd = start + nl
		}
		text, markStart, markLen := searchSnippet(data[lineStart:lineEnd], start-lineStart, end-lineStart)
		into = append(into, SearchMatch{Path: rel, Line: line, Text: text, MatchStart: markStart, MatchLen: markLen})

		if lineEnd >= len(data) {
			return into
		}
		from = lineEnd + 1
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

// searchSnippet trims a matching line for transport and answers where the
// match [start, end) landed inside the snippet, in UTF-16 units, the
// coordinate the browser slices strings in. The pieces around the match are
// sanitized one by one so the mapping survives bytes that are not valid UTF-8.
func searchSnippet(line []byte, start, end int) (string, int, int) {
	cut, dropped := snippetCut(string(line), start)
	start -= dropped
	end -= dropped
	if start < 0 {
		start = 0
	}
	if start > len(cut) {
		start = len(cut)
	}
	if end < start {
		end = start
	}
	if end > len(cut) {
		end = len(cut)
	}
	prefix := strings.ToValidUTF8(cut[:start], "�")
	marked := strings.ToValidUTF8(cut[start:end], "�")
	suffix := strings.ToValidUTF8(cut[end:], "�")
	return prefix + marked + suffix, utf16Len(prefix), utf16Len(marked)
}

// SnippetAround trims a line for transport, keeping a window around the byte
// offset idx when the line is longer than the snippet cap. The search cuts
// around its match and the LSP usage previews around the usage's column, so
// the two lists that share one look share one cutting rule; idx counts into
// the given text, leading whitespace included, and is clamped.
func SnippetAround(text string, idx int) string {
	cut, _ := snippetCut(text, idx)
	return strings.ToValidUTF8(cut, "�")
}

// snippetCut cuts the snippet window around the byte offset idx and reports
// how many bytes of the line fell away in front of it, which is what maps
// offsets in the line onto offsets in the snippet.
func snippetCut(text string, idx int) (string, int) {
	trimmed := strings.TrimSpace(text)
	dropped := strings.Index(text, trimmed)
	idx -= dropped
	text = trimmed
	if len(text) <= maxSnippetBytes {
		return text, dropped
	}
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
	window := text[start:end]
	windowTrimmed := strings.TrimSpace(window)
	return windowTrimmed, dropped + start + strings.Index(window, windowTrimmed)
}

// utf16Len counts UTF-16 units, the coordinate system the browser addresses
// string positions in.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xFFFF {
			n++
		}
	}
	return n
}
