package linecomment

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/statefile"
)

func forgeThread(note, author, text string, line int, quote string, replies ...ForgeNote) ForgeThread {
	return ForgeThread{
		Kind:  "gitlab",
		URL:   "https://gitlab.example.com/team/app/-/merge_requests/4#note_" + note,
		At:    Place{Path: "src/a.go", Line: line, Quote: quote, Quoted: true},
		Notes: append([]ForgeNote{{ID: note, Author: author, Text: text}}, replies...),
	}
}

func forgeOf(t *testing.T, s *Service, note string) Comment {
	t.Helper()
	for _, c := range s.List("p") {
		if c.Forge != nil && c.Forge.Note == note {
			return c
		}
	}
	t.Fatalf("no thread of the forge opened by the note %s in %+v", note, s.List("p"))
	return Comment{}
}

// A read of the forge lands each thread as one comment in the order it lists
// them, its first note the comment's, the others its replies, beside the
// threads people wrote, which it never touches. A read that finds the same
// changes nothing; a thread keeps its id and the line it followed the file to
// while the forge keeps its place, and takes the forge's new place once the
// forge moves it, its notes and resolved state from every read.
func TestSyncForgeKeepsTheThreadsOfARead(t *testing.T) {
	root := t.TempDir()
	s := New(t.TempDir())
	own, _, _ := s.Save("p", Comment{Path: "src/a.go", Line: 3, LineText: "c := 3", Text: "mine"}, Writer{})
	opened := time.Date(2026, 10, 30, 9, 0, 0, 0, time.FixedZone("CET", 3600))
	answered := opened.Add(time.Hour)
	read := []ForgeThread{
		forgeThread("29", "root", "New name?", 4, "name := b", ForgeNote{ID: "33", Author: "alice", Text: "Yes.", Created: answered}),
		forgeThread("30", "root", "Is c needed?", 5, "c := 3"),
	}
	read[0].ID, read[1].ID = "d1", "d2"
	read[0].Notes[0].Created = opened
	if !s.SyncForge("p", root, read, true) {
		t.Fatal("the first read changed nothing")
	}
	if s.SyncForge("p", root, read, true) {
		t.Fatal("the same read changed something")
	}
	first := forgeOf(t, s, "29")
	if first.Path != "src/a.go" || first.Line != 4 || first.LineText != "name := b" || first.Author != "root" || first.Text != "New name?" || first.Link != "" || first.Forge.URL == "" || first.Forge.Thread != "d1" || first.Resolved {
		t.Fatalf("the forge's thread reads %+v %+v", first, first.Forge)
	}
	if !first.UpdatedAt.Equal(opened) || len(first.Replies) != 1 || first.Replies[0] != (Reply{ID: "33", Text: "Yes.", Author: "alice", UpdatedAt: answered.UTC()}) {
		t.Fatalf("the thread's notes read %v %+v", first.UpdatedAt, first.Replies)
	}
	list := s.List("p")
	if len(list) != 3 || list[0].ID != own.ID || list[1].ID != first.ID || list[2].Forge.Thread != "d2" {
		t.Fatalf("the list reads %+v", list)
	}
	if !s.Move("p", "src/a.go", []Comment{{ID: first.ID, Line: 6, LineText: "name := b"}}, Writer{}) {
		t.Fatal("the forge's thread did not follow its line")
	}
	read[0].Resolved = true
	read[0].Notes[1].Text = "Yes, done."
	read[0].Notes = append(read[0].Notes, ForgeNote{ID: "41", Author: "bob", Text: "Agreed."})
	if !s.SyncForge("p", root, read, true) {
		t.Fatal("a resolved thread changed nothing")
	}
	c := forgeOf(t, s, "29")
	if c.ID != first.ID || c.Line != 6 || !c.Resolved || len(c.Replies) != 2 || c.Replies[0].Text != "Yes, done." || c.Replies[1].Author != "bob" {
		t.Fatalf("a thread the forge kept in place reads %+v", c)
	}
	read[0].Notes = slices.Delete(read[0].Notes, 1, 2)
	read[0].At = Place{Path: "src/a.go", Line: 9, Quote: "name := bb", Quoted: true}
	s.SyncForge("p", root, read, true)
	if c := forgeOf(t, s, "29"); c.ID != first.ID || c.Line != 9 || c.LineText != "name := bb" || len(c.Replies) != 1 || c.Replies[0].ID != "41" {
		t.Fatalf("a thread the forge moved, its reply deleted, reads %+v", c)
	}
	if mine := s.List("p")[0]; mine.ID != own.ID || mine.Text != "mine" || mine.Forge != nil {
		t.Fatalf("the user's thread reads %+v", mine)
	}
}

// A read that could not read the text of a thread's line, its commit not
// fetched yet, keeps the place the thread has in the same file, its quote and
// the line it followed the file to; one the forge moved to another file, and
// one never quoted, take the forge's place.
func TestSyncForgeKeepsThePlaceAReadCouldNotQuote(t *testing.T) {
	root := t.TempDir()
	s := New(t.TempDir())
	read := []ForgeThread{forgeThread("29", "root", "New name?", 4, "name := b"), forgeThread("33", "alice", "Yes.", 7, "")}
	read[1].At.Quoted = false
	s.SyncForge("p", root, read, true)
	first := forgeOf(t, s, "29")
	s.Move("p", "src/a.go", []Comment{{ID: first.ID, Line: 6, LineText: "name := b"}}, Writer{})
	read[0].At, read[0].Notes[0].Text = Place{Path: "src/a.go", Line: 5}, "New name, please?"
	s.SyncForge("p", root, read, true)
	if c := forgeOf(t, s, "29"); c.Line != 6 || c.LineText != "name := b" || c.Text != "New name, please?" || c.Forge.At != (Place{Path: "src/a.go", Line: 4, Quote: "name := b", Quoted: true}) {
		t.Fatalf("a thread a read could not quote reads %+v %+v", c, c.Forge)
	}
	if c := forgeOf(t, s, "33"); c.Line != 7 || c.LineText != "" {
		t.Fatalf("a thread never quoted reads %+v", c)
	}
	read[0].At = Place{Path: "src/b.go", Line: 2}
	s.SyncForge("p", root, read, true)
	if c := forgeOf(t, s, "29"); c.Path != "src/b.go" || c.Line != 2 || c.LineText != "" {
		t.Fatalf("a thread the forge moved to another file reads %+v", c)
	}
}

// A complete read takes the threads it lacks away, a read of the first ones
// of more keeps them, and a read of none leaves only the people's threads.
func TestSyncForgeDropsWhatACompleteReadLacks(t *testing.T) {
	root := t.TempDir()
	s := New(t.TempDir())
	s.Save("p", Comment{Path: "b.go", Line: 1, Text: "mine"}, Writer{})
	s.SyncForge("p", root, []ForgeThread{forgeThread("1", "root", "one", 1, "a"), forgeThread("2", "root", "two", 2, "b")}, true)
	s.SyncForge("p", root, []ForgeThread{forgeThread("2", "root", "two", 2, "b")}, false)
	if len(s.List("p")) != 3 {
		t.Fatalf("a partial read took a thread away: %+v", s.List("p"))
	}
	if s.SyncForge("p", root, []ForgeThread{forgeThread("2", "root", "two", 2, "b")}, false) {
		t.Fatal("the same partial read changed something")
	}
	if !s.SyncForge("p", root, []ForgeThread{forgeThread("2", "root", "two", 2, "b")}, true) || len(s.List("p")) != 2 {
		t.Fatalf("a complete read kept a thread it lacks: %+v", s.List("p"))
	}
	s.SyncForge("p", root, nil, true)
	if list := s.List("p"); len(list) != 1 || list[0].Text != "mine" {
		t.Fatalf("a read of none left %+v", list)
	}
	if s.SyncForge("q", root, nil, true) {
		t.Fatal("a read of none in a project without threads changed something")
	}
	if _, err := os.Stat(filepath.Join(s.dir, "q.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a read of none wrote a file: %v", err)
	}
}

// A thread is known by its id, so one whose opening note went on the forge
// keeps its comment, and by its opening note until a read names it; the notes
// of one thread stored apart, one comment each, become that one thread on the
// next read, the first one's comment keeping its id and line.
func TestSyncForgeKnowsAThreadByItsIDOrItsOpeningNote(t *testing.T) {
	root := t.TempDir()
	s := New(t.TempDir())
	thread := forgeThread("29", "root", "New name?", 4, "name := b", ForgeNote{ID: "33", Author: "alice", Text: "Yes."})
	s.SyncForge("p", root, []ForgeThread{thread}, true)
	held := forgeOf(t, s, "29")
	if held.Forge.Thread != "" {
		t.Fatalf("a thread read without its id reads %+v", held.Forge)
	}
	thread.ID = "d1"
	if !s.SyncForge("p", root, []ForgeThread{thread}, true) || forgeOf(t, s, "29").Forge.Thread != "d1" || forgeOf(t, s, "29").ID != held.ID {
		t.Fatalf("a read that named the thread reads %+v", s.List("p"))
	}
	thread.Notes = thread.Notes[1:]
	s.SyncForge("p", root, []ForgeThread{thread}, true)
	if c := forgeOf(t, s, "33"); c.ID != held.ID || c.Text != "Yes." || c.Author != "alice" || len(c.Replies) != 0 || len(s.List("p")) != 1 {
		t.Fatalf("a thread whose opening note went reads %+v", s.List("p"))
	}
	thread.ID = strings.Repeat("d", maxForgeID+1)
	s.SyncForge("p", root, []ForgeThread{thread}, true)
	if c := forgeOf(t, s, "33"); c.Forge.Thread != "" || c.ID != held.ID {
		t.Fatalf("a thread with an absurd id reads %+v %+v", c, c.Forge)
	}

	apart := New(t.TempDir())
	if err := os.MkdirAll(apart.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	stored := `[{"id":"n29","path":"src/a.go","line":6,"lineText":"name := b","text":"New name?","author":"root","forge":{"kind":"gitlab","note":"29","thread":"d1","resolved":true,"at":{"path":"src/a.go","line":4,"quote":"name := b","quoted":true}},"updatedAt":"2026-10-29T10:00:00Z"},` +
		`{"id":"n33","path":"src/a.go","line":6,"lineText":"name := b","text":"Yes.","author":"alice","forge":{"kind":"gitlab","note":"33","thread":"d1","resolved":true,"at":{"path":"src/a.go","line":4,"quote":"name := b","quoted":true}},"updatedAt":"2026-10-29T10:00:00Z"}]`
	if err := os.WriteFile(apart.file("p"), []byte(stored), 0o644); err != nil {
		t.Fatal(err)
	}
	thread = forgeThread("29", "root", "New name?", 4, "name := b", ForgeNote{ID: "33", Author: "alice", Text: "Yes."})
	thread.ID, thread.Resolved = "d1", true
	for _, complete := range []bool{false, true} {
		apart.SyncForge("p", root, []ForgeThread{thread}, complete)
		list := apart.List("p")
		if len(list) != 1 || list[0].ID != "n29" || list[0].Line != 6 || !list[0].Resolved || len(list[0].Replies) != 1 || list[0].Replies[0].ID != "33" {
			t.Fatalf("the notes stored apart, read complete %v, read %+v", complete, list)
		}
	}
}

// A thread of the forge stands under an id derived from the project and its
// id there, so it comes back under the same one after a drop, also once the
// note that opens it went, and one a read names no id of under the one of its
// opening note. Another thread, another project bound to the same change
// request and an id a person's thread holds give another one.
func TestAThreadOfTheForgeComesBackUnderItsID(t *testing.T) {
	root := t.TempDir()
	s := New(t.TempDir())
	thread := forgeThread("29", "root", "New name?", 4, "name := b", ForgeNote{ID: "33", Author: "alice", Text: "Yes."})
	thread.ID = "d1"
	bare := forgeThread("30", "root", "Is c needed?", 5, "c := 3")
	s.SyncForge("p", root, []ForgeThread{thread, bare}, true)
	first, second := forgeOf(t, s, "29").ID, forgeOf(t, s, "30").ID
	shape := regexp.MustCompile(`^[0-9a-f]{16}$`)
	if !shape.MatchString(first) || !shape.MatchString(second) || first == second {
		t.Fatalf("the threads stand under %q and %q", first, second)
	}
	s.DropForge(func(string) bool { return true })
	thread.Notes = thread.Notes[1:]
	other := forgeThread("40", "root", "And d?", 6, "d := 4")
	other.ID = "d2"
	s.SyncForge("p", root, []ForgeThread{thread, bare, other}, true)
	if forgeOf(t, s, "33").ID != first || forgeOf(t, s, "30").ID != second || !shape.MatchString(forgeOf(t, s, "40").ID) || forgeOf(t, s, "40").ID == first {
		t.Fatalf("the threads came back as %+v", s.List("p"))
	}
	s.SyncForge("q", root, []ForgeThread{thread}, true)
	if list := s.List("q"); len(list) != 1 || !shape.MatchString(list[0].ID) || list[0].ID == first {
		t.Fatalf("another project bound to the change request holds %+v", list)
	}

	mine := statefile.DerivedID(nil, "r", thread.keys()[0])
	if err := os.WriteFile(s.file("r"), []byte(`[{"id":"`+mine+`","path":"src/a.go","line":1,"text":"mine","updatedAt":"2026-10-29T10:00:00Z"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	moved := ""
	for range 2 {
		s.SyncForge("r", root, []ForgeThread{thread}, true)
		list := s.List("r")
		if len(list) != 2 || list[0].ID != mine || list[0].Forge != nil || !shape.MatchString(list[1].ID) || list[1].ID == mine || (moved != "" && list[1].ID != moved) {
			t.Fatalf("a thread whose id a person's thread holds reads %+v after %q", list, moved)
		}
		moved = list[1].ID
		s.DropForge(func(string) bool { return true })
	}
}

// What a forge hands over is bounded: a thread outside the project, without a
// line or an opening note with an id stays out, a reply without an id or with
// an absurd one too, a body and an author are cut, a page that is no http or
// https address is dropped, and only MaxPerProject notes land, replies
// counted.
func TestSyncForgeBoundsWhatTheForgeHandsOver(t *testing.T) {
	root := t.TempDir()
	s := New(t.TempDir())
	long := forgeThread("long", strings.Repeat("a", 300), strings.Repeat("ä", MaxText), 1, strings.Repeat("q", maxQuote+10)+"\r",
		ForgeNote{Text: "no id"}, ForgeNote{ID: strings.Repeat("9", maxForgeID+1), Text: "absurd id"}, ForgeNote{ID: "r1", Author: strings.Repeat("b", 300), Text: strings.Repeat("ö", MaxText)})
	evil := forgeThread("evil", "root", "x", 1, "")
	evil.URL = "javascript:alert(1)"
	out := forgeThread("out", "root", "x", 1, "")
	out.At.Path = "../outside.go"
	noLine := forgeThread("noline", "root", "x", 0, "")
	noID := forgeThread("", "root", "x", 1, "")
	empty := forgeThread("empty", "root", "x", 1, "")
	empty.Notes = nil
	s.SyncForge("p", root, []ForgeThread{long, evil, out, noLine, noID, empty}, true)
	list := s.List("p")
	if len(list) != 2 {
		t.Fatalf("the read landed %+v", list)
	}
	c := forgeOf(t, s, "long")
	if len(c.Text) > MaxText || len(c.Author) > maxForgeAuthor || len(c.LineText) != maxQuote || !strings.HasSuffix(c.Text, "ä") {
		t.Fatalf("the long thread holds %d bytes of text, %d of author, %d of quote", len(c.Text), len(c.Author), len(c.LineText))
	}
	if len(c.Replies) != 1 || c.Replies[0].ID != "r1" || len(c.Replies[0].Text) > MaxText || len(c.Replies[0].Author) > maxForgeAuthor {
		t.Fatalf("the long thread's replies read %d, %+v", len(c.Replies), c.Replies[0].ID)
	}
	if c := forgeOf(t, s, "evil"); c.Forge.URL != "" {
		t.Fatalf("a script address stands as the page: %q", c.Forge.URL)
	}
	many := make([]ForgeThread, 0, MaxPerProject+5)
	for i := range MaxPerProject + 5 {
		many = append(many, forgeThread(strings.Repeat("n", i%7+1)+string(rune('a'+i%26))+strings.Repeat("x", i/26), "root", "x", 1, ""))
	}
	s.SyncForge("p", root, many, true)
	if n := len(s.List("p")); n != MaxPerProject {
		t.Fatalf("%d threads of the forge landed", n)
	}
	crowded := forgeThread("crowded", "root", "x", 1, "")
	for i := range MaxPerProject + 5 {
		crowded.Notes = append(crowded.Notes, ForgeNote{ID: "r" + string(rune('a'+i%26)) + strings.Repeat("x", i/26), Text: "r"})
	}
	s.SyncForge("p", root, []ForgeThread{crowded, forgeThread("after", "root", "x", 2, "")}, true)
	if list := s.List("p"); len(list) != 1 || len(list[0].Replies) != MaxPerProject-1 {
		t.Fatalf("a thread of %d notes landed as %d threads with %d replies", len(crowded.Notes), len(list), len(list[0].Replies))
	}
	if _, _, err := s.Save("p", Comment{Path: "a.go", Line: 1, Text: "still room for mine"}, Writer{}); err != nil {
		t.Fatalf("the threads of the forge took the user's room: %v", err)
	}
}

// A thread of the forge changes there alone: an edit, a reply and a resolve
// here are refused to everybody, a delete of any kind passes it by, and it
// follows a renamed file like every comment.
func TestAThreadOfTheForgeChangesThereAlone(t *testing.T) {
	root := t.TempDir()
	s := New(t.TempDir())
	s.SyncForge("p", root, []ForgeThread{forgeThread("29", "root", "New name?", 4, "name := b")}, true)
	thread := forgeOf(t, s, "29")
	edit := thread
	edit.Text = "rewritten"
	for _, w := range []Writer{{}, {Link: "l1", Name: "Anna"}} {
		if _, _, err := s.Save("p", edit, w); !errors.Is(err, ErrForgeThread) {
			t.Fatalf("an edit by %+v: %v", w, err)
		}
		if _, _, err := s.Reply("p", thread.ID, Reply{Text: "x"}, w); !errors.Is(err, ErrForgeThread) {
			t.Fatalf("a reply by %+v: %v", w, err)
		}
		if _, _, err := s.Resolve("p", thread.ID, true, w); !errors.Is(err, ErrForgeThread) {
			t.Fatalf("a resolve by %+v: %v", w, err)
		}
	}
	if n := s.Remove("p", func(Comment) bool { return true }); n != 0 {
		t.Fatalf("a delete of everything took %d threads of the forge", n)
	}
	if !s.Rename("p", "src", "lib") || forgeOf(t, s, "29").Path != "lib/a.go" {
		t.Fatalf("the thread did not follow the rename: %+v", forgeOf(t, s, "29"))
	}
	if n := s.Clear("p"); n != 1 {
		t.Fatalf("a deleted project's clear took %d", n)
	}
}

// Once nothing reads the review threads of a project, its threads of the
// forge go, the ones people wrote stay, and a project left with none loses its
// file; a project not picked keeps its threads, one without any is not named.
func TestDropForgeTakesTheThreadsOfTheForgeOfTheProjectsPicked(t *testing.T) {
	root := t.TempDir()
	s := New(t.TempDir())
	own, _, _ := s.Save("p", Comment{Path: "src/a.go", Line: 3, Text: "mine"}, Writer{})
	s.Save("mine", Comment{Path: "src/a.go", Line: 1, Text: "mine too"}, Writer{})
	thread := []ForgeThread{forgeThread("29", "root", "New name?", 4, "name := b")}
	for _, project := range []string{"p", "q", "s"} {
		s.SyncForge(project, root, thread, true)
	}
	dropped := s.DropForge(func(project string) bool { return project != "q" })
	slices.Sort(dropped)
	if !slices.Equal(dropped, []string{"p", "s"}) {
		t.Fatalf("dropped the threads of %q", dropped)
	}
	if list := s.List("p"); len(list) != 1 || list[0].ID != own.ID {
		t.Fatalf("p holds %+v", list)
	}
	if list := s.List("q"); len(list) != 1 || list[0].Forge == nil {
		t.Fatalf("q holds %+v", list)
	}
	if _, err := os.Stat(s.file("s")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("s kept its file: %v", err)
	}
	if dropped := s.DropForge(func(string) bool { return true }); !slices.Equal(dropped, []string{"q"}) {
		t.Fatalf("the second drop named %q", dropped)
	}
	if dropped := New(filepath.Join(t.TempDir(), "none")).DropForge(func(string) bool { return true }); dropped != nil {
		t.Fatalf("a state directory without comments dropped %q", dropped)
	}
}

// A comment the owner published becomes the thread of the forge it opened,
// keeping its id and the line it followed the file to, and the next read of
// the thread keeps it as it stands, also once it names the thread's id; a
// thread a read brought for it first goes, a guest's comment, a thread of the
// forge, an id nobody holds and a note id longer than any a forge hands out
// change nothing.
func TestAPublishedCommentBecomesItsThreadOfTheForge(t *testing.T) {
	root := t.TempDir()
	s := New(t.TempDir())
	own, _, _ := s.Save("p", Comment{Path: "src/a.go", Line: 6, LineText: "name := b", Text: "New name?"}, Writer{})
	other, _, _ := s.Save("p", Comment{Path: "src/a.go", Line: 7, LineText: "c := 3", Text: "Raced"}, Writer{})
	guest, _, _ := s.Save("p", Comment{Path: "src/a.go", Line: 8, Text: "a guest's"}, Writer{Link: "l1", Name: "Ann"})
	s.SyncForge("p", root, []ForgeThread{forgeThread("40", "root", "Raced", 5, "c := 3")}, true)
	raced := forgeOf(t, s, "40")
	if s.Published("p", map[string]ForgeThread{"gone": forgeThread("50", "root", "x", 1, "")}) {
		t.Fatal("an id nobody holds changed something")
	}
	if s.Published("p", map[string]ForgeThread{guest.ID: forgeThread("51", "root", "x", 1, ""), raced.ID: forgeThread("52", "root", "x", 1, ""), own.ID: forgeThread(strings.Repeat("9", maxForgeID+1), "root", "x", 1, "")}) {
		t.Fatal("a guest's comment, a thread of the forge or a note id no comment takes was published")
	}
	published := map[string]ForgeThread{own.ID: forgeThread("39", "root", "New name?", 4, "name := b\r"), other.ID: forgeThread("40", "root", "Raced", 5, "c := 3")}
	if !s.Published("p", published) {
		t.Fatal("the publish changed nothing")
	}
	list := s.List("p")
	if len(list) != 3 {
		t.Fatalf("the list reads %+v", list)
	}
	c := forgeOf(t, s, "39")
	want := Forge{Kind: "gitlab", Note: "39", URL: "https://gitlab.example.com/team/app/-/merge_requests/4#note_39", At: Place{Path: "src/a.go", Line: 4, Quote: "name := b", Quoted: true}}
	if c.ID != own.ID || c.Line != 6 || c.LineText != "name := b" || c.Author != "root" || c.Text != "New name?" || *c.Forge != want {
		t.Fatalf("the published comment reads %+v %+v", c, c.Forge)
	}
	if c := forgeOf(t, s, "40"); c.ID != other.ID || c.Line != 7 {
		t.Fatalf("the thread a read brought first stands in place of the published one: %+v", c)
	}
	read := []ForgeThread{forgeThread("39", "root", "New name?", 4, "name := b"), forgeThread("40", "root", "Raced", 5, "c := 3")}
	if s.SyncForge("p", root, read, true) {
		t.Fatalf("the read of the published threads changed something: %+v", s.List("p"))
	}
	read[0].ID = "p39"
	read[0].Notes = append(read[0].Notes, ForgeNote{ID: "44", Author: "alice", Text: "Sure."})
	s.SyncForge("p", root, read, true)
	if c := forgeOf(t, s, "39"); c.ID != own.ID || c.Line != 6 || c.Forge.Thread != "p39" || len(c.Replies) != 1 {
		t.Fatalf("after the read the published comment reads %+v", c)
	}
}
