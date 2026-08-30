package filesystem

import (
	"bytes"
	"errors"
	"os"
	"regexp"
	"sort"
	"strings"
)

// ReplaceMatch is one line a replacement would change: the line as it stands
// and as it would read, each marked where the change sits, in UTF-16 units like
// the search marks its hits.
type ReplaceMatch struct {
	Path       string `json:"path"`
	Line       int    `json:"line"`
	Text       string `json:"text"`
	MatchStart int    `json:"start"`
	MatchLen   int    `json:"len"`
	After      string `json:"after"`
	AfterStart int    `json:"afterStart"`
	AfterLen   int    `json:"afterLen"`
}

// ReplaceReport is what a replacement would do, or did. Total and Files count
// every occurrence in the whole scope, not only the ones the preview carries:
// the button says what pressing it costs, and a capped list must never be
// mistaken for the size of the job.
type ReplaceReport struct {
	Matches   []ReplaceMatch `json:"matches"`
	Truncated bool           `json:"truncated"`
	Total     int            `json:"total"`
	Files     int            `json:"files"`
	Replaced  int            `json:"replaced"`
	Changed   []string       `json:"changed"`
	Blocked   []string       `json:"blocked"`
}

// ReplaceRequest is one replacement job: what to find, what to put in its
// place, and where to look. The scope is the search's own, so a replacement can
// only ever reach what the same query, folder and mask showed.
type ReplaceRequest struct {
	Query       string
	Replacement string
	UseRegex    bool
	Options     SearchOptions
	// OnlyPath and OnlyLine narrow the job to the occurrences on one line of
	// one file, which is what a single row's own replace asks for.
	OnlyPath string
	OnlyLine int
	// Dirty are project relative paths the browser holds unsaved. A job that
	// would touch one of them writes nothing at all: the file on disk and the
	// buffer would otherwise part ways without anybody being told.
	Dirty []string
}

// replacer finds the query in a file and writes the replacement over it. The
// two modes differ in one thing that matters: a regex expands $1, and a literal
// replacement does not, so a dollar stays a dollar.
type replacer struct {
	re     *regexp.Regexp
	needle []byte
	repl   []byte
	// fold says whether the literal path ignores case, which is what the
	// lowered copies below are for.
	fold bool
	// data and lowered are what the line walk stands on, filled by enter.
	data    []byte
	lowered []byte
	// scratch is count's and apply's own lowering, so neither disturbs the
	// walk that is running over the file at the time.
	scratch []byte
}

// haystack is what count and apply compare against: the bytes themselves when
// case counts, a lowered copy of them otherwise. It is their own buffer, so
// neither disturbs a line walk running over the file at the time.
func (r *replacer) haystack(data []byte) []byte {
	if !r.fold {
		return data
	}
	r.scratch = appendLowerBytes(r.scratch[:0], data)
	return r.scratch
}

// enter hands the replacer the file the line walk is about, which is what its
// find answers against. It is the matcher's own shape, so one loop walks the
// matching lines for a search and for a replacement alike.
func (r *replacer) enter(data []byte) {
	r.data = data
	if r.re == nil && r.fold {
		r.lowered = appendLowerBytes(r.lowered[:0], data)
		r.data = r.lowered
	}
}

// find answers the next occurrence at or after from, start -1 when there is
// none.
func (r *replacer) find(from int) (int, int) {
	if r.re != nil {
		loc := r.re.FindIndex(r.data[from:])
		if loc == nil {
			return -1, -1
		}
		return from + loc[0], from + loc[1]
	}
	idx := bytes.Index(r.data[from:], r.needle)
	if idx < 0 {
		return -1, -1
	}
	return from + idx, from + idx + len(r.needle)
}

func newReplacer(query, replacement string, useRegex, caseSensitive bool) (*replacer, error) {
	r := &replacer{repl: []byte(replacement), fold: !caseSensitive}
	if useRegex {
		re, err := compilePattern(query, caseSensitive)
		if err != nil {
			return nil, err
		}
		r.re = re
		return r, nil
	}
	r.needle = []byte(query)
	if !caseSensitive {
		r.needle = []byte(strings.ToLower(query))
	}
	return r, nil
}

// count answers how many occurrences sit in data, without building anything.
func (r *replacer) count(data []byte) int {
	if r.re != nil {
		return len(r.re.FindAllIndex(data, -1))
	}
	haystack := r.haystack(data)
	n, from := 0, 0
	for {
		idx := bytes.Index(haystack[from:], r.needle)
		if idx < 0 {
			return n
		}
		n++
		from += idx + len(r.needle)
	}
}

// apply rewrites data and reports how many occurrences fell and where the first
// replacement landed in the result, which is what the preview marks.
func (r *replacer) apply(data []byte) (out []byte, count, start, length int) {
	out = make([]byte, 0, len(data)+len(r.repl))
	start, length = -1, 0
	if r.re != nil {
		last := 0
		for _, loc := range r.re.FindAllSubmatchIndex(data, -1) {
			out = append(out, data[last:loc[0]]...)
			at := len(out)
			// Expand is what makes $1 a back reference; the literal path below
			// deliberately has no such step.
			out = r.re.Expand(out, r.repl, data, loc)
			if start < 0 {
				start, length = at, len(out)-at
			}
			count++
			last = loc[1]
		}
		out = append(out, data[last:]...)
		return out, count, start, length
	}
	haystack := r.haystack(data)
	last, from := 0, 0
	for {
		idx := bytes.Index(haystack[from:], r.needle)
		if idx < 0 {
			break
		}
		at := from + idx
		out = append(out, data[last:at]...)
		mark := len(out)
		out = append(out, r.repl...)
		if start < 0 {
			start, length = mark, len(r.repl)
		}
		count++
		last = at + len(r.needle)
		from = last
	}
	out = append(out, data[last:]...)
	return out, count, start, length
}

// PreviewReplace answers what the job would change without touching anything:
// the first MaxSearchMatches lines with their before and after, and the whole
// count behind them.
func PreviewReplace(root string, req ReplaceRequest, ex Exclusions) (ReplaceReport, error) {
	report := ReplaceReport{Matches: []ReplaceMatch{}, Changed: []string{}, Blocked: []string{}}
	err := walkReplace(root, req, ex, func(rel string, data []byte, r *replacer) error {
		n := r.count(data)
		if n == 0 {
			return nil
		}
		report.Total += n
		report.Files++
		if len(report.Matches) < MaxSearchMatches {
			report.Matches = appendReplaceLines(report.Matches, data, rel, r)
		}
		return nil
	})
	if err != nil {
		return ReplaceReport{}, err
	}
	if len(report.Matches) > MaxSearchMatches {
		report.Matches = report.Matches[:MaxSearchMatches]
	}
	report.Truncated = report.Total > len(report.Matches)
	sort.Slice(report.Matches, func(i, j int) bool {
		if report.Matches[i].Path != report.Matches[j].Path {
			return report.Matches[i].Path < report.Matches[j].Path
		}
		return report.Matches[i].Line < report.Matches[j].Line
	})
	return report, nil
}

// ApplyReplace performs the job. It reads the whole scope first and writes
// nothing until it knows that no file it would touch is held unsaved in the
// browser, so a refusal leaves the project exactly as it was.
func ApplyReplace(root string, req ReplaceRequest, ex Exclusions) (ReplaceReport, error) {
	report := ReplaceReport{Matches: []ReplaceMatch{}, Changed: []string{}, Blocked: []string{}}
	dirty := map[string]bool{}
	for _, path := range req.Dirty {
		if path = strings.Trim(strings.TrimSpace(path), "/"); path != "" {
			dirty[path] = true
		}
	}
	targets := []string{}
	err := walkReplace(root, req, ex, func(rel string, data []byte, r *replacer) error {
		n := countIn(data, req, rel, r)
		if n == 0 {
			return nil
		}
		report.Total += n
		report.Files++
		targets = append(targets, rel)
		if dirty[rel] {
			report.Blocked = append(report.Blocked, rel)
		}
		return nil
	})
	if err != nil {
		return ReplaceReport{}, err
	}
	if len(report.Blocked) > 0 {
		sort.Strings(report.Blocked)
		return report, nil
	}
	sort.Strings(targets)
	for _, rel := range targets {
		written, n, err := writeReplaced(root, rel, req)
		if err != nil {
			return report, err
		}
		if !written {
			continue
		}
		report.Replaced += n
		report.Changed = append(report.Changed, rel)
	}
	return report, nil
}

// countIn is the count of what this job would take out of one file: every
// occurrence, or the ones on the single line a row asked for.
func countIn(data []byte, req ReplaceRequest, rel string, r *replacer) int {
	if req.OnlyPath == "" {
		return r.count(data)
	}
	if rel != strings.Trim(req.OnlyPath, "/") {
		return 0
	}
	from, to, ok := lineRange(data, req.OnlyLine)
	if !ok {
		return 0
	}
	return r.count(data[from:to])
}

// writeReplaced rewrites one file and answers whether it changed. A file the
// query no longer matches is left alone, which is also what happens when the
// disk moved between the preview and the press.
func writeReplaced(root, rel string, req ReplaceRequest) (bool, int, error) {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return false, 0, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return false, 0, err
	}
	r, err := newReplacer(req.Query, req.Replacement, req.UseRegex, req.Options.CaseSensitive)
	if err != nil {
		return false, 0, err
	}
	var out []byte
	var n int
	if req.OnlyPath == "" {
		out, n, _, _ = r.apply(data)
	} else {
		from, to, ok := lineRange(data, req.OnlyLine)
		if !ok {
			return false, 0, nil
		}
		var line []byte
		line, n, _, _ = r.apply(data[from:to])
		out = append(append(append([]byte{}, data[:from]...), line...), data[to:]...)
	}
	if n == 0 {
		return false, 0, nil
	}
	if _, err := WriteFileText(root, rel, out); err != nil {
		return false, 0, err
	}
	return true, n, nil
}

// lineRange answers the byte range of the given one-based line, without its
// newline.
func lineRange(data []byte, line int) (int, int, bool) {
	if line < 1 {
		return 0, 0, false
	}
	from := 0
	for n := 1; n < line; n++ {
		idx := bytes.IndexByte(data[from:], '\n')
		if idx < 0 {
			return 0, 0, false
		}
		from += idx + 1
	}
	if from > len(data) {
		return 0, 0, false
	}
	to := len(data)
	if idx := bytes.IndexByte(data[from:], '\n'); idx >= 0 {
		to = from + idx
	}
	return from, to, true
}

// walkReplace walks the scope the search walks, through the same walkScope, and
// hands every readable text file to visit. One scope rule, one mask rule, one
// skip list for both, so a replacement can never reach what the search in front
// of it did not show.
func walkReplace(root string, req ReplaceRequest, ex Exclusions, visit func(rel string, data []byte, r *replacer) error) error {
	if strings.TrimSpace(req.Query) == "" {
		return errors.New("Nothing to replace.")
	}
	dir, err := searchFolder(root, req.Options.Folder)
	if err != nil {
		return err
	}
	r, err := newReplacer(strings.TrimSpace(req.Query), req.Replacement, req.UseRegex, req.Options.CaseSensitive)
	if err != nil {
		return err
	}
	only := strings.Trim(strings.TrimSpace(req.OnlyPath), "/")
	var data []byte
	return walkScope(root, dir, ex, req.Options.Mask, func(path, rel string) error {
		if only != "" && rel != only {
			return nil
		}
		var err error
		data, err = readInto(path, data)
		if err != nil {
			return nil
		}
		if bytes.IndexByte(data, 0) >= 0 {
			return nil // binary
		}
		return visit(rel, data, r)
	})
}

// appendReplaceLines records one row per matching line, through the same walk
// the search builds its rows with: the line as it stands and the same line with
// every occurrence on it replaced, which is exactly what this row's own replace
// would write.
func appendReplaceLines(into []ReplaceMatch, data []byte, rel string, r *replacer) []ReplaceMatch {
	r.enter(data)
	eachMatchingLine(data, r, func(line int, text []byte, start, end int) bool {
		snippet, markStart, markLen := searchSnippet(text, start, end)
		row := ReplaceMatch{Path: rel, Line: line, Text: snippet, MatchStart: markStart, MatchLen: markLen}
		after, _, at, length := r.apply(text)
		if at >= 0 {
			row.After, row.AfterStart, row.AfterLen = searchSnippet(after, at, at+length)
		} else {
			row.After = SnippetAround(string(after), 0)
		}
		into = append(into, row)
		return len(into) < MaxSearchMatches
	})
	return into
}
