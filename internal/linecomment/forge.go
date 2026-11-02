package linecomment

import (
	"errors"
	"io/fs"
	"log"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/statefile"
)

// Forge is where a review thread came from: the kind of forge, the id of
// the note that opens it and its page there, the thread's own id, "" until a
// read named it and for a note the forge keeps alone, and At, where the forge
// put it when it was last read, nothing for a thread on the project.
// Unresolvable says the forge does not resolve it; a thread stored before
// that was told is one on a line, which the forge resolves. The comment's own
// line follows the file like any other; only a move of the forge's place sets
// it again.
type Forge struct {
	Kind         string `json:"kind"`
	Note         string `json:"note"`
	URL          string `json:"url,omitempty"`
	Thread       string `json:"thread,omitempty"`
	At           Place  `json:"at"`
	Unresolvable bool   `json:"unresolvable,omitempty"`
}

// Place is where the forge puts a thread: the file, the line and the code
// line's text there. Quoted is false when the text could not be read, and
// the comment then stands without a quote.
type Place struct {
	Path   string `json:"path"`
	Line   int    `json:"line"`
	Quote  string `json:"quote,omitempty"`
	Quoted bool   `json:"quoted,omitempty"`
}

// ForgeThread is one review thread the way a read of the forge found it: the
// kind of forge, the thread's id there, "" where the read named none, its
// page, whether the forge resolves it and whether it is resolved, where the
// forge puts it, on a line or with Project on the project, a general thread
// that names no place, and its notes, oldest first, the first one opening it.
type ForgeThread struct {
	Kind         string
	ID           string
	URL          string
	Unresolvable bool
	Resolved     bool
	Project      bool
	At           Place
	Notes        []ForgeNote
}

// ForgeNote is one note of a review thread on the forge: its id there, who
// wrote it, what and when.
type ForgeNote struct {
	ID      string
	Author  string
	Text    string
	Created time.Time
}

// The bounds of what a forge hands over beside the notes themselves: an id,
// an author's name and a page.
const (
	maxForgeID     = 255
	maxForgeAuthor = 255
	maxForgeURL    = 2 << 10
)

// SyncForge makes the project's threads of the forge the ones a read found,
// in the order the read lists them, and reports whether that changed
// anything. Each is one comment, its first note the comment's, the others its
// replies, its resolved state the comment's, a general thread a project
// comment, which stands on no file. A thread it holds already, known
// by the thread's id or by the note that opens it, keeps its id and the line
// it followed the file to, unless the forge moved it, and takes the rest from
// the read. One it does not hold comes back under the id statefile.DerivedID
// gives the project and the note that opens it, the one key a publish knows
// before any read names the thread, so a published comment takes that id at
// once; a thread whose opening note went comes back under the next one's. A
// read that could not read the text of the thread's line, a commit the
// repository has not fetched yet, tells nothing of it: the thread keeps its
// place in the same file, a fetch brings the commit for the next read. A
// thread the read lacks goes when the read is complete, and stays when it
// read the first ones of more. A thread whose file does not resolve under
// root is left out. The threads people wrote are not touched.
func (s *Service) SyncForge(project, root string, threads []ForgeThread, complete bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(project)
	if err != nil {
		return false
	}
	next := make([]Comment, 0, len(list)+len(threads))
	var held []Comment
	at := map[string]int{}
	ids := map[string]bool{}
	for _, c := range list {
		ids[c.ID] = true
		if c.Forge == nil {
			next = append(next, c)
			continue
		}
		for _, key := range c.Forge.keys() {
			if _, ok := at[key]; !ok {
				at[key] = len(held)
			}
		}
		held = append(held, c)
	}
	now := time.Now().UTC()
	seen := map[string]bool{}
	used := map[int]bool{}
	taken := 0
	for _, t := range threads {
		if taken == MaxPerProject || !t.fits(root) {
			continue
		}
		keys := t.keys()
		if slices.ContainsFunc(keys, func(key string) bool { return seen[key] }) {
			continue
		}
		var c Comment
		ok := false
		for _, key := range keys {
			seen[key] = true
			if i, found := at[key]; found && !ok && !used[i] {
				c, ok = held[i], true
				used[i] = true
			}
		}
		t.At.Quote = FileLine(t.At.Quote)
		if ok && !t.At.Quoted && c.Forge.At.Quoted && c.Forge.At.Path == t.At.Path {
			t.At = c.Forge.At
		}
		updated := c
		if !ok || c.Forge.At != t.At || c.Project != t.Project {
			updated.Path, updated.Line, updated.LineText = t.At.Path, t.At.Line, t.At.Quote
		}
		updated.Project = t.Project
		if !ok {
			updated.ID = statefile.DerivedID(ids, project, t.idKey())
			ids[updated.ID] = true
		}
		first := t.Notes[0]
		updated.Text, updated.Author = clamp(first.Text, MaxText), clamp(first.Author, maxForgeAuthor)
		updated.Replies = forgeReplies(t.Notes[1:], MaxPerProject-taken-1)
		updated.Resolved = t.Resolved
		updated.Forge = t.origin()
		switch {
		case !first.Created.IsZero():
			updated.UpdatedAt = first.Created.UTC()
		case !reflect.DeepEqual(updated, c):
			updated.UpdatedAt = now
		}
		taken += 1 + len(updated.Replies)
		next = append(next, updated)
	}
	if !complete {
		for i, c := range held {
			if !used[i] && !slices.ContainsFunc(c.Forge.keys(), func(key string) bool { return seen[key] }) {
				next = append(next, c)
			}
		}
	}
	if reflect.DeepEqual(list, next) || (len(list) == 0 && len(next) == 0) {
		return false
	}
	s.store(project, next)
	return true
}

// forgeReplies are the replies notes make, at most room of them, a note
// without an id or with one longer than any a forge hands out left out.
func forgeReplies(notes []ForgeNote, room int) []Reply {
	var replies []Reply
	for _, n := range notes {
		if len(replies) >= room {
			break
		}
		if !forgeID(n.ID) {
			continue
		}
		replies = append(replies, Reply{ID: n.ID, Text: clamp(n.Text, MaxText), Author: clamp(n.Author, maxForgeAuthor), UpdatedAt: n.Created.UTC()})
	}
	return replies
}

// Published makes each comment the owner wrote that a publish posted the
// review thread of the forge it opened, keyed by the comment's id: the
// comment keeps its own line, takes the id its thread comes back under after
// a drop and keeps the one it had as Was, and takes the forge's thread with
// its place there the way a read of it finds it, so the next read keeps it as
// it stands, its resolved state the forge's from then on. It moves behind the
// other threads of the forge, where a read lists a new one. A thread of the
// forge a read already brought for it goes, none stands twice. A comment
// deleted, changed to another's or replied to meanwhile is left to the next
// read, and so is one the forge named no note of a line comment could stand
// for, and one the forge took on a line for a project comment or the other
// way round. It reports whether anything changed.
func (s *Service) Published(project string, published map[string]ForgeThread) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	list, err := s.read(project)
	if err != nil {
		return false
	}
	var became []Comment
	took := map[string]bool{}
	for _, c := range list {
		t, ok := published[c.ID]
		if !ok || !Publishable(c) || t.Project != c.Project || t.Kind == "" || len(t.Notes) == 0 || !forgeID(t.Notes[0].ID) {
			continue
		}
		t.At.Quote = FileLine(t.At.Quote)
		first := t.Notes[0]
		c.Text, c.Author = clamp(first.Text, MaxText), clamp(first.Author, maxForgeAuthor)
		c.Forge, c.Resolved = t.origin(), t.Resolved
		c.UpdatedAt = first.Created.UTC()
		if first.Created.IsZero() {
			c.UpdatedAt = time.Now().UTC()
		}
		became = append(became, c)
		for _, key := range t.keys() {
			took[key] = true
		}
	}
	if len(became) == 0 {
		return false
	}
	next := make([]Comment, 0, len(list))
	ids := map[string]bool{}
	for _, c := range list {
		_, publishedNow := published[c.ID]
		switch {
		case Publishable(c) && publishedNow:
		case c.Forge != nil && slices.ContainsFunc(c.Forge.keys(), func(key string) bool { return took[key] }):
		default:
			next = append(next, c)
			ids[c.ID] = true
		}
	}
	for i, c := range became {
		became[i].Was, became[i].ID = c.ID, statefile.DerivedID(ids, project, c.Forge.idKey())
		ids[became[i].ID] = true
	}
	s.store(project, append(next, became...))
	return true
}

// Publishable reports whether c is a thread a publish turns into a review
// thread of the forge, a project comment into a general one: the owner's own,
// without a reply.
func Publishable(c Comment) bool {
	return c.Forge == nil && c.Link == "" && len(c.Replies) == 0
}

// DropForge takes the threads of the forge from every project drop picks,
// the way a complete read without any does, and answers the projects that
// held some. A directory or a file it cannot read keeps what it holds.
func (s *Service) DropForge(drop func(project string) bool) []string {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("line comments: %v", err)
		}
		return nil
	}
	var dropped []string
	for _, entry := range entries {
		project, ok := strings.CutSuffix(entry.Name(), ".json")
		if ok && entry.Type().IsRegular() && drop(project) && s.SyncForge(project, "", nil, true) {
			dropped = append(dropped, project)
		}
	}
	return dropped
}

// keys are what a thread of the forge is known by: its id where a read named
// it, and the note that opens it.
func (f *Forge) keys() []string {
	return forgeKeys(f.Kind, f.Thread, f.Note)
}

func (t ForgeThread) keys() []string {
	return forgeKeys(t.Kind, t.thread(), t.Notes[0].ID)
}

// idKey is the key the id of a thread's comment is derived from, the note
// that opens it.
func (f *Forge) idKey() string {
	return forgeKeys(f.Kind, "", f.Note)[0]
}

func (t ForgeThread) idKey() string {
	return forgeKeys(t.Kind, "", t.Notes[0].ID)[0]
}

func forgeKeys(kind, thread, note string) []string {
	keys := make([]string, 0, 2)
	if thread != "" {
		keys = append(keys, "thread\x00"+kind+"\x00"+thread)
	}
	return append(keys, "note\x00"+kind+"\x00"+note)
}

// thread is the thread's id, none when it is longer than any a forge hands
// out.
func (t ForgeThread) thread() string {
	if len(t.ID) > maxForgeID {
		return ""
	}
	return t.ID
}

// origin is where the thread came from.
func (t ForgeThread) origin() *Forge {
	return &Forge{Kind: t.Kind, Note: t.Notes[0].ID, URL: pageURL(t.URL), Thread: t.thread(), At: t.At, Unresolvable: t.Unresolvable}
}

// fits reports whether the thread can stand as a line comment under root, or
// as a project comment, which names no place.
func (t ForgeThread) fits(root string) bool {
	if t.Kind == "" || len(t.Notes) == 0 || !forgeID(t.Notes[0].ID) {
		return false
	}
	if t.Project {
		return t.At == Place{}
	}
	if t.At.Line < 1 || t.At.Path == "" || len(t.At.Path) > MaxPath {
		return false
	}
	_, err := filesystem.ResolveUnder(root, t.At.Path)
	return err == nil
}

func forgeID(id string) bool {
	return id != "" && len(id) <= maxForgeID
}

// pageURL is a thread's page when it is one a link may open, an http or https
// address, else nothing.
func pageURL(raw string) string {
	if len(raw) > maxForgeURL {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	return raw
}
