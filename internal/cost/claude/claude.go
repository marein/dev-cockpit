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
	"io/fs"
	"log"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"syscall"
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
	keep   func() int
	// failed is the last read error per file, so one that stays unreadable
	// is logged once, not on every collect.
	failed map[string]string
}

// NewSource reads under root. prices is the table a call is priced with when
// it is read. places names the directories a session may have run in, for a
// transcript that names none itself, see placeOf. keep is how many months of
// bookings the ledger keeps.
func NewSource(root string, prices func() price.Table, places func() []string, keep func() int) *Source {
	return &Source{root: root, prices: prices, places: places, keep: keep, failed: map[string]string{}}
}

func (s *Source) Coder() string { return "claude" }

// goneKeep is how long a session is remembered after its last file went:
// a claude that is still exiting writes its total into a new file within
// seconds, which tops up only what the session did not book.
const goneKeep = time.Hour

// state is the source's cursor: how far each file was read, and per session
// what its calls booked, so a cost-state line tops up only the difference.
// Forgotten is when each session dropped from Sessions went: a file of it
// that comes back, a backup import restores them, was booked already. It is
// kept a month longer than the ledger keeps rows, the months are cut in the
// ledger's zone; after that, what comes back books into months that are gone.
type state struct {
	Files     map[string]*fileCursor   `json:"files,omitempty"`
	Sessions  map[string]*sessionState `json:"sessions,omitempty"`
	Forgotten map[string]time.Time     `json:"forgotten,omitempty"`
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
	// Gone is when the last file of the session was found missing.
	Gone time.Time `json:"gone,omitzero"`
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

func readState(raw json.RawMessage) state {
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
	if st.Forgotten == nil {
		st.Forgotten = map[string]time.Time{}
	}
	// A null entry is valid JSON the cockpit never writes; it is read as
	// absent, so a hand edited month file cannot take the server down.
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
	return st
}

// Collect reads the bytes each transcript gained since the cursor, of every
// session or of the named one. A session's sub-agent files are read before
// its own file: a cost-state line in the main file covers the sub-agents'
// calls, and they have to be booked first so the top-up does not take them.
// A read of every session drops the files that went from the cursor, and a
// session goneKeep after its last file went. A new file of a forgotten
// session is read without booking, so only what it gains later is booked.
// A listing that fails changes
// nothing, one that comes back empty drops nothing: neither says what is
// gone.
func (s *Source) Collect(raw json.RawMessage, session string, now time.Time) ([]cost.Entry, json.RawMessage, error) {
	st := readState(raw)
	files, err := s.transcripts()
	if err != nil {
		return nil, raw, err
	}
	if session != "" {
		files = slices.DeleteFunc(files, func(t transcript) bool { return t.session != session })
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
		cur := fileCursor{}
		prev := st.Files[t.path]
		if prev != nil {
			cur = *prev
		}
		n := len(r.entries)
		next, err := r.readFile(t, cur)
		if _, forgotten := st.Forgotten[t.session]; forgotten && prev == nil {
			r.entries = r.entries[:n]
		}
		if err != nil {
			if s.failed[t.path] != err.Error() {
				log.Printf("cost: %v", err)
			}
			s.failed[t.path] = err.Error()
			continue
		}
		delete(s.failed, t.path)
		st.Files[t.path] = &next
	}
	if session == "" && len(files) > 0 {
		for path := range st.Files {
			if !present[path] {
				delete(st.Files, path)
			}
		}
		maps.DeleteFunc(s.failed, func(path, _ string) bool { return !present[path] })
		for id, ss := range st.Sessions {
			switch {
			case live[id]:
				ss.Gone = time.Time{}
			case ss.Gone.IsZero():
				ss.Gone = now
			case now.Sub(ss.Gone) >= goneKeep:
				delete(st.Sessions, id)
				st.Forgotten[id] = ss.Gone
			}
		}
		cut := cost.AddMonths(now, -s.keep())
		for id, gone := range st.Forgotten {
			if live[id] || gone.Before(cut) {
				delete(st.Forgotten, id)
			}
		}
	}
	encoded, err := json.Marshal(st)
	if err != nil {
		return nil, raw, err
	}
	return r.entries, encoded, nil
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

// transcripts lists every transcript under the root. A directory that
// cannot be read fails the whole listing, a listing with a hole in it would
// read as files that are gone; one that went between two reads is gone. A
// root that is not there lists nothing: a host that never ran claude has
// none, and an empty listing drops nothing.
func (s *Source) transcripts() ([]transcript, error) {
	dirs, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []transcript
	for _, d := range dirs {
		dir := filepath.Join(s.root, d.Name())
		entries, err := os.ReadDir(dir)
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if session, ok := strings.CutSuffix(name, ".jsonl"); ok && !e.IsDir() {
				out = append(out, transcript{path: filepath.Join(dir, name), dir: d.Name(), session: session})
				continue
			}
			err := filepath.WalkDir(filepath.Join(dir, name, "subagents"), func(p string, f fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !f.IsDir() && strings.HasSuffix(p, ".jsonl") {
					out = append(out, transcript{path: p, dir: d.Name(), session: name, sub: true})
				}
				return nil
			})
			if err != nil && !errors.Is(err, fs.ErrNotExist) && !errors.Is(err, syscall.ENOTDIR) {
				return nil, err
			}
		}
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

// Rate is the list price of model now, the peak rate where it has two.
func (s *Source) Rate(model string) (price.Rate, bool) {
	return rateOf(s.prices(), model)
}

// rateOf finds a model claude ran: a Claude model at Anthropic's price, else
// one served through Ollama at Ollama Cloud's.
func rateOf(table price.Table, model string) (price.Rate, bool) {
	if r, ok := table.Lookup(price.Anthropic, model); ok {
		return r, true
	}
	return table.Lookup(price.Ollama, model)
}

// Reprice implements cost.Repricer for Ollama models only. A Claude model's
// spend that was booked unpriced is topped up from claude's own total once
// the model has a price, pricing its rows too would count it twice; claude
// knows no Ollama price and its sessions are never topped up.
func (s *Source) Reprice(model string, at time.Time, tokens price.Tokens) (float64, bool) {
	table := s.prices()
	if _, ok := table.Lookup(price.Anthropic, model); ok {
		return 0, false
	}
	r, ok := table.Lookup(price.Ollama, model)
	if !ok {
		return 0, false
	}
	return r.At(at).Cost(tokens, false), true
}
