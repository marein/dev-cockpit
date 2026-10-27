package filesystem

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func browseIndex(t *testing.T) *quickOpenIndex {
	t.Helper()
	ix, err := buildQuickOpenIndex(quickOpenTree(t), DefaultExclusionSet(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return ix
}

func entryNames(entries []QuickOpenEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Path
		if e.Folder {
			out[i] += "/"
		}
	}
	return out
}

// Every folder above an indexed file is a folder of the index, once, and an
// excluded one is not, since no file of its is indexed.
func TestTheIndexHoldsTheFoldersOfItsFiles(t *testing.T) {
	ix := browseIndex(t)
	want := []string{
		"config/", "config/chat/", "config/connect-four/", "node_modules/", "node_modules/pkg/",
		"src/", "src/Chat/", "src/Chat/Application/", "src/ConnectFour/", "src/ConnectFour/Domain/", "src/ConnectFour/Domain/Game/",
		"tests/", "tests/unit/", "tests/unit/ConnectFour/", "tests/unit/ConnectFour/Domain/", "tests/unit/ConnectFour/Domain/Game/",
	}
	if !reflect.DeepEqual(ix.folders, want) {
		t.Fatalf("folders %v, want %v", ix.folders, want)
	}
	if ix.folderLower[6] != "src/chat/" {
		t.Fatalf("the lowercased copy is %v", ix.folderLower)
	}
}

// An empty query lists what stands directly in the folder, folders first and
// each by name regardless of case, the way the tree lists it, and counts the
// files below it at any depth.
func TestBrowseWithoutAQueryListsTheFolderLikeTheTree(t *testing.T) {
	ix := browseIndex(t)
	got := ix.browse("", 50)
	if want := []string{"config/", "node_modules/", "src/", "tests/"}; !reflect.DeepEqual(entryNames(got.Entries), want) || got.Folder != "" || got.Total != 4 || got.Files != 7 {
		t.Fatalf("the root lists %v (%+v)", entryNames(got.Entries), got)
	}
	got = ix.browse("src/", 50)
	if want := []string{"src/Chat/", "src/ConnectFour/", "src/Kernel.php"}; !reflect.DeepEqual(entryNames(got.Entries), want) || got.Folder != "src" || got.Query != "" || got.Files != 3 {
		t.Fatalf("src lists %v (%+v)", entryNames(got.Entries), got)
	}
	got = ix.browse("src/", 2)
	if len(got.Entries) != 2 || got.Total != 3 {
		t.Fatalf("a cut listing answered %v of %d", entryNames(got.Entries), got.Total)
	}
}

// A query is matched below the folder, folders and files alike, by the quick
// open's rule: a name the folder's own path holds does not match everything in
// it, and a folder whose name starts with the query stands above the files
// that only lie in it.
func TestBrowseMatchesFilesAndFoldersBelowTheFolder(t *testing.T) {
	ix := browseIndex(t)
	got := ix.browse("game", 50)
	want := []string{"src/ConnectFour/Domain/Game/", "tests/unit/ConnectFour/Domain/Game/", "src/ConnectFour/Domain/Game/Game.php", "tests/unit/ConnectFour/Domain/Game/GameTest.php"}
	if !reflect.DeepEqual(entryNames(got.Entries), want) || got.Total != 4 {
		t.Fatalf("game answered %v (%d)", entryNames(got.Entries), got.Total)
	}
	got = ix.browse("src/ConnectFour/dom", 50)
	want = []string{"src/ConnectFour/Domain/", "src/ConnectFour/Domain/Game/", "src/ConnectFour/Domain/Game/Game.php"}
	if !reflect.DeepEqual(entryNames(got.Entries), want) || got.Folder != "src/ConnectFour" || got.Query != "dom" {
		t.Fatalf("dom in src/ConnectFour answered %v (%+v)", entryNames(got.Entries), got)
	}
	if got = ix.browse("tests/unit", 50); got.Folder != "tests" || got.Query != "unit" || entryNames(got.Entries)[0] != "tests/unit/" {
		t.Fatalf("a folder name without its slash is a query in its parent: %+v", got)
	}
	if got = ix.browse("src/connect", 50); got.Total != 4 || entryNames(got.Entries)[0] != "src/ConnectFour/" {
		t.Fatalf("connect in src answered %v", entryNames(got.Entries))
	}
}

// A typed path that names no folder reads as the plain query it was before
// folders, so a fragment of a path still finds what holds it, and a folder is
// found by its name with the slash.
func TestBrowseReadsAPathThatNamesNoFolderAsAQuery(t *testing.T) {
	ix := browseIndex(t)
	got := ix.browse("domain/game/", 50)
	want := []string{"src/ConnectFour/Domain/Game/", "tests/unit/ConnectFour/Domain/Game/", "src/ConnectFour/Domain/Game/Game.php", "tests/unit/ConnectFour/Domain/Game/GameTest.php"}
	if got.Folder != "" || got.Query != "domain/game/" || !reflect.DeepEqual(entryNames(got.Entries), want) {
		t.Fatalf("domain/game/ answered %v (%+v)", entryNames(got.Entries), got)
	}
	got = ix.browse("src/Domain/game", 50)
	if got.Folder != "src" || got.Query != "Domain/game" || len(got.Entries) != 2 {
		t.Fatalf("the deepest folder is not where the answer stands: %v (%+v)", entryNames(got.Entries), got)
	}
	for _, typed := range []string{"/src/", "../src/", ".git/", "Src/"} {
		if got := ix.browse(typed, 50); got.Folder != "" {
			t.Fatalf("%q walked into %q", typed, got.Folder)
		}
	}
}

// The limit bounds the answer, never the count.
func TestBrowseRespectsTheLimit(t *testing.T) {
	ix := browseIndex(t)
	got := ix.browse("o", 3)
	if len(got.Entries) != 3 || got.Total <= 3 {
		t.Fatalf("a bounded answer holds %d of %d", len(got.Entries), got.Total)
	}
}

// A folder answers through the cache the same way, from the index the quick
// open builds, so a project's walk is paid once for both.
func TestBrowseAnswersThroughTheCache(t *testing.T) {
	c := NewQuickOpenCache()
	root := quickOpenTree(t)
	if _, err := c.Query(root, "game", "", DefaultExclusionSet(), QuickOpenLimit); err != nil {
		t.Fatal(err)
	}
	first := c.entries[root].index.Load()
	got, err := c.Browse(root, "config/", DefaultExclusionSet(), 50)
	if err != nil {
		t.Fatal(err)
	}
	if c.entries[root].index.Load() != first || fmt.Sprint(entryNames(got.Entries)) != "[config/chat/ config/connect-four/]" {
		t.Fatalf("browse answered %v from another index", entryNames(got.Entries))
	}
}

func BenchmarkBrowse(b *testing.B) {
	paths := make([]string, 0, 24000)
	for m := 0; len(paths) < 24000; m++ {
		for s := 0; s < 20 && len(paths) < 24000; s++ {
			for f := 0; f < 10 && len(paths) < 24000; f++ {
				paths = append(paths, fmt.Sprintf("pkg/m%d/sub%d/file%d.txt", m, s, f))
			}
		}
	}
	ix := &quickOpenIndex{paths: paths, lower: paths, folders: foldersOf(paths)}
	ix.folderLower = make([]string, len(ix.folders))
	for i, f := range ix.folders {
		ix.folderLower[i] = strings.ToLower(f)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix.browse([]string{"", "sub1 file", "pkg/m7/", "pkg/m7/sub", "needle"}[i%5], 50)
	}
}
