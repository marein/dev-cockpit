// Package claude reads what claude spent out of its transcripts. Every
// assistant line carries the usage of the API call it came from, so spend is
// booked per call, on the call's own timestamp and at the list price of the
// moment it is read. claude also writes a cost-state line with its running
// total when a process ends; the part of that total its calls do not explain
// (requests claude logs nowhere) is booked as a top-up, never the total again.
package claude

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/marein/dev-cockpit/internal/cost"
	"github.com/marein/dev-cockpit/internal/cost/price"
)

// Source reads the transcripts under one claude projects directory,
// ~/.claude/projects: one directory per launch directory, one
// <session>.jsonl per session in it, and the sub-agents of a session under
// <session>/subagents/.
type Source struct {
	root   string
	prices func() price.Table
	places func() []string

	// read is how far each file was read by the last collect, by path, for
	// a capture: it takes only what lies past that.
	mu   sync.Mutex
	read map[string]int64
}

// NewSource reads under root. prices is the table a call is priced with when
// it is read. places names the directories a session may have run in, for a
// transcript that names none itself, see placeOf.
func NewSource(root string, prices func() price.Table, places func() []string) *Source {
	return &Source{root: root, prices: prices, places: places}
}

func (s *Source) Coder() string { return "claude" }

// state is the source's cursor: how far each file was read, and per session
// what its calls booked, so a cost-state line tops up only the difference.
type state struct {
	Files    map[string]*fileCursor   `json:"files,omitempty"`
	Sessions map[string]*sessionState `json:"sessions,omitempty"`
}

type sessionState struct {
	// Messages are the calls this session booked first, while a file of it
	// still exists. A call is one API response; a fork copies its parent's
	// calls under the same ids, and the first session to book one keeps it.
	Messages []string `json:"messages,omitempty"`
	// Booked is USD per model of the calls this session booked.
	Booked map[string]float64 `json:"booked,omitempty"`
	// Copied is USD per model of calls in this session's files that another
	// session booked first: a fork's running total holds its parent's spend.
	Copied map[string]float64 `json:"copied,omitempty"`
	// TopUp is USD per model booked from cost-state lines.
	TopUp map[string]float64 `json:"top_up,omitempty"`
	// Unknown is set once claude said it priced a model it does not know
	// (an Ollama model at a claude price): its totals are not spend.
	Unknown bool `json:"unknown,omitempty"`
}

func addUSD(m *map[string]float64, model string, usd float64) {
	if *m == nil {
		*m = map[string]float64{}
	}
	(*m)[model] += usd
}

// transcript is one file to read and the session its calls belong to.
// modTime is the file's time, what a cost-state line without a time of its
// own is booked at.
type transcript struct {
	path    string
	dir     string
	session string
	sub     bool
	modTime time.Time
}

// captured is what a session's files held right before a delete: per path
// the size, the time, the head that fingerprints the file and the bytes from
// where the last collect stopped to the end.
type captured struct {
	session string
	files   map[string]capturedFile
}

type capturedFile struct {
	size    int64
	modTime time.Time
	head    []byte
	from    int64
	tail    []byte
}

// ReadAt serves the head and the tail; the bytes between them were read
// before and a read that asks for them fails.
func (f capturedFile) ReadAt(p []byte, off int64) (int, error) {
	if off >= f.size {
		return 0, io.EOF
	}
	end := min(off+int64(len(p)), f.size)
	var n int
	switch {
	case end <= int64(len(f.head)):
		n = copy(p, f.head[off:end])
	case off >= f.from:
		n = copy(p, f.tail[off-f.from:end-f.from])
	default:
		return 0, errors.New("the captured file lacks the bytes asked for")
	}
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

// Capture implements cost.Source: it reads what the session's transcript and
// its sub-agents' hold past the last collect, and every head. Only the
// session's own files are read.
func (s *Source) Capture(session string) any {
	c := captured{session: session, files: map[string]capturedFile{}}
	files, err := s.transcripts()
	if err != nil {
		return c
	}
	s.mu.Lock()
	read := maps.Clone(s.read)
	s.mu.Unlock()
	for _, t := range files {
		if t.session != session {
			continue
		}
		if f, err := capture(t.path, read[t.path]); err == nil {
			c.files[t.path] = f
		}
	}
	return c
}

func capture(path string, from int64) (capturedFile, error) {
	f, err := os.Open(path)
	if err != nil {
		return capturedFile{}, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return capturedFile{}, err
	}
	size := info.Size()
	if from > size {
		from = 0
	}
	head := make([]byte, min(int64(headSize), size))
	if _, err := f.ReadAt(head, 0); err != nil && !errors.Is(err, io.EOF) {
		return capturedFile{}, err
	}
	tail := make([]byte, size-from)
	if _, err := f.ReadAt(tail, from); err != nil && !errors.Is(err, io.EOF) {
		return capturedFile{}, err
	}
	return capturedFile{size: size, modTime: info.ModTime(), head: head, from: from, tail: tail}, nil
}

// Collect reads the bytes each transcript gained since the cursor. A
// session's sub-agent files are read before its own file: a cost-state line in
// the main file covers the sub-agents' calls, and they have to be booked
// first so the top-up does not take them. A file that went is dropped from
// the cursor; its session keeps what it booked. With a capture only the
// captured files are read, out of memory, and nothing else moves.
func (s *Source) Collect(raw json.RawMessage, capt any, now time.Time) ([]cost.Entry, json.RawMessage, error) {
	var st state
	if len(raw) > 0 && json.Unmarshal(raw, &st) != nil {
		st = state{}
	}
	if st.Files == nil {
		st.Files = map[string]*fileCursor{}
	}
	if st.Sessions == nil {
		st.Sessions = map[string]*sessionState{}
	}
	// A null entry is valid JSON the cockpit never writes; it is read as
	// absent, so a hand edited cursors.json cannot take the server down.
	for path, c := range st.Files {
		if c == nil {
			delete(st.Files, path)
		}
	}
	for id, ss := range st.Sessions {
		if ss == nil {
			delete(st.Sessions, id)
		}
	}
	c, isCapture := capt.(captured)
	var files []transcript
	if isCapture {
		for path, f := range c.files {
			files = append(files, transcript{path: path, session: c.session, sub: isSubagent(s.root, path), dir: transcriptDir(s.root, path), modTime: f.modTime})
		}
		sortTranscripts(files)
	} else {
		var err error
		if files, err = s.transcripts(); err != nil {
			return nil, raw, err
		}
	}
	r := &reader{
		src:   s,
		state: &st,
		owner: owners(st.Sessions),
		table: s.prices(),
		now:   now,
	}
	present := map[string]bool{}
	live := map[string]bool{}
	for _, t := range files {
		present[t.path] = true
		live[t.session] = true
		cur := st.Files[t.path]
		if cur == nil {
			cur = &fileCursor{}
		}
		var next fileCursor
		var err error
		if isCapture {
			f := c.files[t.path]
			next, err = r.readFile(t, *cur, f, f.size)
		} else {
			next, err = r.readDisk(t, *cur)
		}
		if err != nil {
			continue
		}
		st.Files[t.path] = &next
	}
	if !isCapture {
		for path := range st.Files {
			if !present[path] {
				delete(st.Files, path)
			}
		}
		// A call id can only come back in a file that exists, so a session
		// with no file left forgets its ids and keeps its sums.
		for id, ss := range st.Sessions {
			if !live[id] {
				ss.Messages = nil
			}
		}
	}
	encoded, err := json.Marshal(st)
	if err != nil {
		return nil, raw, err
	}
	read := make(map[string]int64, len(st.Files))
	for path, cur := range st.Files {
		read[path] = cur.Offset
	}
	s.mu.Lock()
	s.read = read
	s.mu.Unlock()
	return r.entries, encoded, nil
}

func isSubagent(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && strings.Count(filepath.ToSlash(rel), "/") > 1
}

func transcriptDir(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return ""
	}
	return strings.SplitN(filepath.ToSlash(rel), "/", 2)[0]
}

func owners(sessions map[string]*sessionState) map[string]string {
	out := map[string]string{}
	for id, ss := range sessions {
		for _, key := range ss.Messages {
			out[key] = id
		}
	}
	return out
}

func (s *Source) transcripts() ([]transcript, error) {
	mains, err := filepath.Glob(filepath.Join(s.root, "*", "*.jsonl"))
	if err != nil {
		return nil, err
	}
	var out []transcript
	for _, p := range mains {
		out = append(out, transcript{
			path:    p,
			dir:     filepath.Base(filepath.Dir(p)),
			session: strings.TrimSuffix(filepath.Base(p), ".jsonl"),
		})
	}
	subs, err := filepath.Glob(filepath.Join(s.root, "*", "*", "subagents"))
	if err != nil {
		return nil, err
	}
	for _, d := range subs {
		session := filepath.Base(filepath.Dir(d))
		dir := filepath.Base(filepath.Dir(filepath.Dir(d)))
		_ = filepath.WalkDir(d, func(p string, e fs.DirEntry, err error) error {
			if err == nil && !e.IsDir() && strings.HasSuffix(p, ".jsonl") {
				out = append(out, transcript{path: p, dir: dir, session: session, sub: true})
			}
			return nil
		})
	}
	sortTranscripts(out)
	return out, nil
}

func sortTranscripts(out []transcript) {
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.dir != b.dir {
			return a.dir < b.dir
		}
		if a.session != b.session {
			return a.session < b.session
		}
		if a.sub != b.sub {
			return a.sub
		}
		return a.path < b.path
	})
}

// placeOf is the directory a session ran in, read off its transcript
// directory's name, for a transcript without a cwd line. claude names the
// directory after the launch directory with every character outside
// [A-Za-z0-9] replaced by a dash, which cannot be undone, so the known
// places are encoded and compared instead. A place may hold one "*" path
// element, which matches whatever the name holds there: a project or an
// assistant workspace that is gone keeps its name.
func (s *Source) placeOf(dir string) string {
	if s.places == nil {
		return ""
	}
	places := s.places()
	for _, p := range places {
		if !strings.Contains(p, "*") && encodeDir(p) == dir {
			return p
		}
	}
	for _, p := range places {
		i := strings.Index(p, "*")
		if i < 0 {
			continue
		}
		head, tail := encodeDir(p[:i]), encodeDir(p[i+1:])
		if len(dir) > len(head)+len(tail) && strings.HasPrefix(dir, head) && strings.HasSuffix(dir, tail) {
			return p[:i] + dir[len(head):len(dir)-len(tail)] + p[i+1:]
		}
	}
	return ""
}

func encodeDir(path string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			return r
		}
		return '-'
	}, path)
}

// Rate is the list price a call of model is booked with now.
func (s *Source) Rate(model string) (price.Rate, bool) {
	return s.prices().Lookup(price.Anthropic, model)
}
