package recent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// clock hands out one second per call, ascending, so the entries of a test
// stand in the order they were touched in.
func clock(store *Store, start int64) {
	next := start
	store.now = func() time.Time {
		next++
		return time.Unix(next, 0)
	}
}

// A capped store holds the newest entries and nothing else, and the oldest
// leaves as soon as a new name arrives.
func TestCappedStoreKeepsTheNewestEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "recent.json")
	store := NewCapped(path, 3)
	clock(store, 1000)

	for _, name := range []string{"one", "two", "three", "four"} {
		store.Touch(name)
	}

	names := store.Names()
	if len(names) != 3 || names[0] != "four" || names[1] != "three" || names[2] != "two" {
		t.Fatalf("expected the three newest names, got %v", names)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	m := map[string]int64{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["one"]; ok || len(m) != 3 {
		t.Fatalf("the file kept more than the cap: %v", m)
	}
}

// Touching a name again moves it to the front and costs no slot.
func TestCappedStoreMovesATouchedNameUp(t *testing.T) {
	store := NewCapped(filepath.Join(t.TempDir(), "recent.json"), 2)
	clock(store, 1000)

	store.Touch("one")
	store.Touch("two")
	store.Touch("one")

	names := store.Names()
	if len(names) != 2 || names[0] != "one" || names[1] != "two" {
		t.Fatalf("expected one before two, got %v", names)
	}
}

// Two touches inside one second share a timestamp. The order between them is
// then the name's, and every read answers the same way.
func TestNamesAnswerTheSameOnEveryRead(t *testing.T) {
	store := NewCapped(filepath.Join(t.TempDir(), "recent.json"), 5)
	store.now = func() time.Time { return time.Unix(1000, 0) }

	store.Touch("beta")
	store.Touch("alpha")

	first := store.Names()
	if len(first) != 2 {
		t.Fatalf("expected both names, got %v", first)
	}
	for i := 0; i < 5; i += 1 {
		if names := store.Names(); names[0] != first[0] || names[1] != first[1] {
			t.Fatalf("the order moved between reads: %v then %v", first, names)
		}
	}
}

// The unbounded store is what the projects list sorts by: nothing it was ever
// told may drop out of it.
func TestUnboundedStoreKeepsEverything(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "recent.json"))
	clock(store, 1000)

	for _, name := range []string{"one", "two", "three", "four", "five", "six"} {
		store.Touch(name)
	}

	if names := store.Names(); len(names) != 6 || names[0] != "six" {
		t.Fatalf("expected all six names newest first, got %v", names)
	}
}
