package web

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/filesystem"
	"github.com/marein/dev-cockpit/internal/project"
)

// held names a path with the token a client says it holds for it, which is what
// a watch carries.
func held(paths ...string) []watchedPath {
	out := make([]watchedPath, 0, len(paths))
	for _, p := range paths {
		out = append(out, watchedPath{Path: p, Token: "token-" + p})
	}
	return out
}

// The scope on the wire is one client's screen, and what the tick works on is
// the union over every client of the project. Two browsers on one project are
// one tick over both their screens, never two ticks and never one screen's.
func TestFileWatchScopeIsTheUnionOverTheClients(t *testing.T) {
	w := newFileWatchers()
	now := time.Now()

	if !w.watch("proj", "a", held("one.go", "two.go"), held("", "src"), now) {
		t.Fatal("the first client did not ask for a tick")
	}
	if w.watch("proj", "b", held("two.go", "three.go"), held("", "docs"), now) {
		t.Fatal("the second client started a second tick")
	}

	files, dirs, ok := w.scope("proj", now)
	if !ok {
		t.Fatal("the scope of two watching clients answered nothing")
	}
	if want := []string{"one.go", "three.go", "two.go"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("files %v, want %v", files, want)
	}
	if want := []string{"", "docs", "src"}; !reflect.DeepEqual(dirs, want) {
		t.Fatalf("dirs %v, want %v", dirs, want)
	}

	// A renewal replaces that client's scope whole: a closed tab has to leave
	// the union, and the only report of that is what the client sends next.
	w.watch("proj", "a", held("one.go"), held(""), now)
	files, _, _ = w.scope("proj", now)
	if want := []string{"one.go", "three.go", "two.go"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("after a renewal files %v, want %v", files, want)
	}
	w.watch("proj", "b", held("three.go"), held(""), now)
	files, _, _ = w.scope("proj", now)
	if want := []string{"one.go", "three.go"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("a closed tab stayed in the union: %v, want %v", files, want)
	}
}

// The tick lives as long as somebody renews. One client letting its window
// lapse takes nothing away from the other, and the round after the last one
// lapsed is the round that ends it.
func TestFileWatchEndsWithTheLastClient(t *testing.T) {
	w := newFileWatchers()
	start := time.Now()

	w.watch("proj", "a", held("one.go"), nil, start)
	w.watch("proj", "b", held("two.go"), nil, start.Add(20*time.Second))

	// Past the first client's window, inside the second one's.
	later := start.Add(fileWatchWindow + time.Second)
	files, _, ok := w.scope("proj", later)
	if !ok {
		t.Fatal("the tick ended although one client was still watching")
	}
	if want := []string{"two.go"}; !reflect.DeepEqual(files, want) {
		t.Fatalf("the lapsed client stayed in the union: %v, want %v", files, want)
	}

	// Past both.
	last := start.Add(2 * fileWatchWindow)
	if _, _, ok := w.scope("proj", last); ok {
		t.Fatal("the tick ran on with no client left")
	}
	// And the project is gone with it, so the next watch starts a fresh tick
	// instead of renewing one that has already returned.
	if !w.watch("proj", "a", held("one.go"), nil, last) {
		t.Fatal("the next watch did not start a tick")
	}
}

// A tick that stops on its own drops the project the same way, so the next
// watch is the one that starts the next tick.
func TestFileWatchReleaseFreesTheProject(t *testing.T) {
	w := newFileWatchers()
	now := time.Now()
	w.watch("proj", "a", nil, nil, now)
	if w.watch("proj", "a", nil, nil, now) {
		t.Fatal("a renewal started a second tick")
	}
	w.release("proj")
	if !w.watch("proj", "a", nil, nil, now) {
		t.Fatal("after the release the watch did not start a tick")
	}
}

// One client cannot make the tick do arbitrary work: what it sends is capped
// and deduplicated before it is ever probed.
func TestFileWatchCapsOneClientsScope(t *testing.T) {
	w := newFileWatchers()
	now := time.Now()
	many := make([]string, 0, maxWatchedFiles+50)
	for i := 0; i < maxWatchedFiles+50; i += 1 {
		many = append(many, "f"+string(rune('a'+i%26))+string(rune('a'+i/26)))
	}
	w.watch("proj", "a", held(append(many, many...)...), held("x", "x", "y"), now)
	files, dirs, _ := w.scope("proj", now)
	if len(files) > maxWatchedFiles {
		t.Fatalf("the scope holds %d files, the cap is %d", len(files), maxWatchedFiles)
	}
	if want := []string{"x", "y"}; !reflect.DeepEqual(dirs, want) {
		t.Fatalf("dirs %v, want %v", dirs, want)
	}
}

// One round of the tick: a path nobody has said anything about is recorded and
// never reported, a file whose content moved is reported, a file that was only
// touched is not, and a directory answers for its own entries alone.
func TestRoundProjectFilesReportsTheSecondRound(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	// The directories go an hour into the past before the baseline is taken, so
	// the kernel's own move to now is visible however coarse its clock is. Two
	// seconds lie between two rounds in the running server and nothing there
	// would ever notice; a test that changes a directory microseconds after
	// reading it would.
	age := func(rel string) {
		t.Helper()
		past := time.Now().Add(-time.Hour).Truncate(time.Second)
		if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(rel)), past, past); err != nil {
			t.Fatalf("chtimes %s: %v", rel, err)
		}
	}
	write("src/one.go", "package one")
	write("src/two.go", "package two")
	age("")
	age("src")

	s := &Server{fileWatchers: newFileWatchers()}
	p := project.Project{Name: "proj", Path: root}
	// A client with no tokens at all: every path starts as a plain baseline.
	s.fileWatchers.watch(p.Name, "a",
		[]watchedPath{{Path: "src/one.go"}, {Path: "src/two.go"}},
		[]watchedPath{{Path: ""}, {Path: "src"}}, time.Now())
	files := []string{"src/one.go", "src/two.go"}
	dirs := []string{"", "src"}

	// The first round is the baseline and says nothing.
	if f, d := s.roundProjectFiles(p, files, dirs); len(f) > 0 || len(d) > 0 {
		t.Fatalf("the first round reported %v / %v", f, d)
	}

	// A write into an open file, which is what a coder does and what the git
	// status stops reporting after the first time.
	write("src/one.go", "package one // and more")
	moved, movedDirs := s.roundProjectFiles(p, files, dirs)
	if want := []string{"src/one.go"}; !reflect.DeepEqual(moved, want) {
		t.Fatalf("files %v, want %v", moved, want)
	}
	if len(movedDirs) > 0 {
		t.Fatalf("writing into a file moved the directories %v", movedDirs)
	}

	// Nothing at all: an idle round costs the browsers nothing.
	if f, d := s.roundProjectFiles(p, files, dirs); len(f) > 0 || len(d) > 0 {
		t.Fatalf("an idle round reported %v / %v", f, d)
	}

	// A deleted file is the tab's movement and its folder's at once.
	if err := os.Remove(filepath.Join(root, "src", "two.go")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	moved, movedDirs = s.roundProjectFiles(p, files, dirs)
	if want := []string{"src/two.go"}; !reflect.DeepEqual(moved, want) {
		t.Fatalf("files %v, want %v", moved, want)
	}
	if want := []string{"src"}; !reflect.DeepEqual(movedDirs, want) {
		t.Fatalf("dirs %v, want %v", movedDirs, want)
	}

	// A file created in another watched folder is that folder's movement and no
	// open tab's, and it does not touch the folder next to it either.
	write("fresh.go", "package fresh")
	moved, movedDirs = s.roundProjectFiles(p, files, dirs)
	if len(moved) > 0 {
		t.Fatalf("a created file reported the open tabs %v", moved)
	}
	if want := []string{""}; !reflect.DeepEqual(movedDirs, want) {
		t.Fatalf("dirs %v, want %v", movedDirs, want)
	}
}

// The case the baseline alone cannot carry, and the reason the tokens travel: a
// path joins the watch and is written before the tick ever looks at it. A
// baseline taken by that first round would already hold the write and would
// hide it for good; the token the client sent is from before it, so the first
// round reports it.
func TestRoundProjectFilesReportsWhatMovedBeforeTheFirstRound(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "one.go")
	if err := os.WriteFile(path, []byte("package one"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, held, err := filesystem.ReadFileText(root, "one.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	listed, err := filesystem.ListDir(root, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	sig := filesystem.DirSignature(listed)

	s := &Server{fileWatchers: newFileWatchers()}
	p := project.Project{Name: "proj", Path: root}
	s.fileWatchers.watch(p.Name, "a",
		[]watchedPath{{Path: "one.go", Token: held}},
		[]watchedPath{{Path: "", Token: sig}}, time.Now())

	// Both move in the moment between the client saying what it holds and the
	// first round of the tick.
	if err := os.WriteFile(path, []byte("package one // theirs"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "two.go"), []byte("package two"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	moved, movedDirs := s.roundProjectFiles(p, []string{"one.go"}, []string{""})
	if want := []string{"one.go"}; !reflect.DeepEqual(moved, want) {
		t.Fatalf("files %v, want %v", moved, want)
	}
	if want := []string{""}; !reflect.DeepEqual(movedDirs, want) {
		t.Fatalf("dirs %v, want %v", movedDirs, want)
	}
	// And once the client has caught up, the same round says nothing again.
	if f, d := s.roundProjectFiles(p, []string{"one.go"}, []string{""}); len(f) > 0 || len(d) > 0 {
		t.Fatalf("the round after reported %v / %v", f, d)
	}
}

// A client that says it holds what the disk holds starts quiet: the seed is a
// comparison, not an announcement.
func TestRoundProjectFilesIsQuietWhenTheClientIsCurrent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "one.go"), []byte("package one"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, version, err := filesystem.ReadFileText(root, "one.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	listed, err := filesystem.ListDir(root, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	s := &Server{fileWatchers: newFileWatchers()}
	p := project.Project{Name: "proj", Path: root}
	s.fileWatchers.watch(p.Name, "a",
		[]watchedPath{{Path: "one.go", Token: version}},
		[]watchedPath{{Path: "", Token: filesystem.DirSignature(listed)}}, time.Now())
	if f, d := s.roundProjectFiles(p, []string{"one.go"}, []string{""}); len(f) > 0 || len(d) > 0 {
		t.Fatalf("a client that is up to date was told about %v / %v", f, d)
	}
}

// What left the scope leaves the stamps, so a tab closed and opened again
// starts from what that client then says it holds and never reports a move that
// happened while nobody was looking at it.
func TestRoundProjectFilesForgetsWhatLeftTheScope(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "one.go")
	if err := os.WriteFile(path, []byte("package one"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	s := &Server{fileWatchers: newFileWatchers()}
	p := project.Project{Name: "proj", Path: root}
	now := time.Now()
	s.fileWatchers.watch(p.Name, "a", []watchedPath{{Path: "one.go"}}, nil, now)
	s.roundProjectFiles(p, []string{"one.go"}, nil)

	// The tab closes: the client's next scope no longer names it, and the round
	// after that finds nothing to compare it against.
	s.fileWatchers.watch(p.Name, "a", nil, nil, now)
	if _, _, ok := s.fileWatchers.scope(p.Name, now); !ok {
		t.Fatal("the client stopped watching")
	}
	if held := s.fileWatchers.stamps(p.Name); len(held) != 0 {
		t.Fatalf("the closed tab left %d stamps behind", len(held))
	}
	// It changed while nobody watched, and the reopened tab read it fresh, so
	// what it now says it holds is the disk.
	if err := os.WriteFile(path, []byte("package one // later"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, version, err := filesystem.ReadFileText(root, "one.go")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	s.fileWatchers.watch(p.Name, "a", []watchedPath{{Path: "one.go", Token: version}}, nil, now)
	if moved, _ := s.roundProjectFiles(p, []string{"one.go"}, nil); len(moved) > 0 {
		t.Fatalf("the reopened tab was told about %v", moved)
	}
}
