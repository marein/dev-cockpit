package filesystem

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func stampWrite(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// The stat is the prefilter, so a file whose size and mtime stand still is not
// read at all. The proof is a last stamp carrying a token no read could ever
// answer: it comes back unchanged, which it only can when nothing was read.
func TestStampFileSkipsTheReadWhileTheStatStandsStill(t *testing.T) {
	root := t.TempDir()
	stampWrite(t, root, "a.txt", "one")

	first := StampFile(root, "a.txt", Stamp{})
	if !first.Exists || first.Version == "" {
		t.Fatalf("the first round read nothing: %+v", first)
	}

	pretend := first
	pretend.Version = "notatokenaread"
	again := StampFile(root, "a.txt", pretend)
	if again.Version != "notatokenaread" {
		t.Fatalf("the file was read although the stat had not moved: %+v", again)
	}
	if !again.Same(pretend) {
		t.Fatalf("a file nobody touched reads as moved: %+v vs %+v", again, pretend)
	}
}

// A moved stat is only ever a reason to look. A rewrite of the same bytes is
// what a git checkout does, and the token is what keeps it from waking every
// open editor for nothing.
func TestStampFileReadsOnAMovedStatAndTheTokenDecides(t *testing.T) {
	root := t.TempDir()
	stampWrite(t, root, "a.txt", "one")
	first := StampFile(root, "a.txt", Stamp{})

	touch := func(at time.Time) {
		t.Helper()
		if err := os.Chtimes(filepath.Join(root, "a.txt"), at, at); err != nil {
			t.Fatalf("chtimes: %v", err)
		}
	}

	// The same content, an hour later: the stat moved, so the file is read, and
	// the token says that nothing happened. This is the git checkout.
	stampWrite(t, root, "a.txt", "one")
	touch(time.Now().Add(time.Hour))
	rewritten := StampFile(root, "a.txt", first)
	if rewritten.ModTime == first.ModTime {
		t.Fatal("the timestamp did not move, the case under test never happened")
	}
	if !rewritten.Same(first) {
		t.Fatalf("a rewrite of identical bytes reads as a change: %+v vs %+v", rewritten, first)
	}

	// Other content of the same length, so the size says nothing and only the
	// token can tell the two apart.
	stampWrite(t, root, "a.txt", "two")
	touch(time.Now().Add(2 * time.Hour))
	changed := StampFile(root, "a.txt", rewritten)
	if changed.Size != rewritten.Size {
		t.Fatalf("the sizes were meant to be equal here: %+v vs %+v", changed, rewritten)
	}
	if changed.Same(rewritten) {
		t.Fatalf("a same sized write with a fresh token reads as unchanged: %+v", changed)
	}
}

// A file that is gone is a movement like any other, and the zero stamp is what
// says so. It is also what a path outside the project answers, because the tab
// cannot read that one either.
func TestStampFileAnswersTheZeroStampForWhatItCannotRead(t *testing.T) {
	root := t.TempDir()
	stampWrite(t, root, "a.txt", "one")
	held := StampFile(root, "a.txt", Stamp{})

	if err := os.Remove(filepath.Join(root, "a.txt")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	gone := StampFile(root, "a.txt", held)
	if gone.Exists || gone.Same(held) {
		t.Fatalf("a deleted file reads as %+v", gone)
	}
	// Two rounds of the same absence say nothing, so it is published once.
	if !StampFile(root, "a.txt", gone).Same(gone) {
		t.Fatal("a file that stays deleted reads as moving every round")
	}

	if s := StampFile(root, "../outside.txt", Stamp{}); s.Exists {
		t.Fatalf("a path out of the project answered %+v", s)
	}
	if err := os.Mkdir(filepath.Join(root, "dir"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if s := StampFile(root, "dir", Stamp{}); s.Exists {
		t.Fatalf("a directory answered as a file: %+v", s)
	}
}

// age puts a path's timestamp an hour into the past, at a fixed instant so that
// aging twice is aging once. The kernel stamps a directory from a coarse clock
// that only moves with the timer tick, so two changes inside one tick carry one
// timestamp; two seconds lie between two rounds in the running server, and
// nothing here would ever see that, but a test that changes a directory
// microseconds after reading it would. Aging first makes the kernel's own move
// to now visible whatever that clock's granularity is.
func age(t *testing.T, root, rel string) {
	t.Helper()
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	if err := os.Chtimes(filepath.Join(root, filepath.FromSlash(rel)), past, past); err != nil {
		t.Fatalf("chtimes %s: %v", rel, err)
	}
}

// A directory carries the signature of its own listing: what the tree renders,
// and nothing about the files inside it.
func TestStampDirMovesOnItsOwnEntriesOnly(t *testing.T) {
	root := t.TempDir()
	stampWrite(t, root, "sub/a.txt", "one")
	age(t, root, "sub")

	before := StampDir(root, "sub", Stamp{})
	if !before.Exists || before.Version == "" {
		t.Fatalf("a directory answered no listing: %+v", before)
	}
	if !StampDir(root, "sub", before).Same(before) {
		t.Fatal("an untouched directory reads as moved")
	}

	// A write into a file it holds is not a change to the directory.
	stampWrite(t, root, "sub/a.txt", "one plus more")
	if !StampDir(root, "sub", before).Same(before) {
		t.Fatal("writing into a file moved the directory it lies in")
	}

	// A new entry is.
	stampWrite(t, root, "sub/b.txt", "two")
	if StampDir(root, "sub", before).Same(before) {
		t.Fatal("a created file did not move its directory")
	}

	if s := StampDir(root, "sub/a.txt", Stamp{}); s.Exists {
		t.Fatalf("a file answered as a directory: %+v", s)
	}
	if s := StampDir(root, "nothing/here", Stamp{}); s.Exists {
		t.Fatalf("a directory that is not there answered %+v", s)
	}
}

// The listing signature is what a client can hand back, so it has to be the
// same answer the listing route gives and it has to see a rename, which leaves
// a directory holding as many entries of the same size as before.
func TestDirSignatureFollowsTheListing(t *testing.T) {
	root := t.TempDir()
	stampWrite(t, root, "sub/a.txt", "one")
	stampWrite(t, root, "sub/b.txt", "two")

	listed, err := ListDir(root, "sub")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	before := DirSignature(listed)
	if before != StampDir(root, "sub", Stamp{}).Version {
		t.Fatal("the listing route and the watch would not agree on the signature")
	}

	if err := os.Rename(filepath.Join(root, "sub", "b.txt"), filepath.Join(root, "sub", "c.txt")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	listed, err = ListDir(root, "sub")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if DirSignature(listed) == before {
		t.Fatal("a rename left the signature standing")
	}

	// A write into one of them does not, the rows are the same rows.
	stampWrite(t, root, "sub/a.txt", "one, much longer now")
	listed, err = ListDir(root, "sub")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if DirSignature(listed) == before {
		t.Fatal("the signature was meant to still describe the renamed listing")
	}
}

// A seed is what a client says it holds. Its stat is empty on purpose, so the
// prefilter cannot answer for it and the next probe really looks.
func TestSeedStampIsAlwaysLookedAt(t *testing.T) {
	root := t.TempDir()
	stampWrite(t, root, "a.txt", "one")
	real := StampFile(root, "a.txt", Stamp{})

	current := StampFile(root, "a.txt", SeedStamp(real.Version))
	if !current.Same(SeedStamp(real.Version)) {
		t.Fatal("a client holding what the disk holds reads as behind")
	}
	behind := SeedStamp("a token from an older read")
	current = StampFile(root, "a.txt", behind)
	if current.Same(behind) {
		t.Fatal("a client holding older content reads as up to date")
	}
	if current.Version != real.Version {
		t.Fatalf("the probe answered %q, the disk holds %q", current.Version, real.Version)
	}
}

// The project root is watched like every other directory, and the empty path is
// what names it.
func TestStampDirTakesTheEmptyPathAsTheRoot(t *testing.T) {
	root := t.TempDir()
	age(t, root, "")
	before := StampDir(root, "", Stamp{})
	if !before.Exists {
		t.Fatalf("the project root answered %+v", before)
	}
	stampWrite(t, root, "fresh.txt", "one")
	if StampDir(root, "", before).Same(before) {
		t.Fatal("a file created in the project root did not move it")
	}
}
