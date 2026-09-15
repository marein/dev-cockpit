package assistant

import (
	"os"
	"path/filepath"
	"testing"
)

// The sweep is what keeps the disk honest: an instance directory the index
// never mentions, with the workspace of an assistant that is gone inside it,
// is invisible from every surface, so nothing else would ever collect it.
func TestTheSweepRemovesWhatNoIndexKnows(t *testing.T) {
	dir := t.TempDir()
	index, instances, memory := Paths(dir)
	store := NewStoreAt(index, instances)

	live := "11111111-1111-4111-8111-111111111111"
	lost := "22222222-2222-4222-8222-222222222222"
	store.Save(Instance{Summary: Summary{ID: live, Title: "Still here", CoderID: "claude"}})

	// A transcript whose index entry vanished, with the workspace of that
	// assistant next to it.
	mustWrite(t, filepath.Join(instances, lost, "transcript.json"), `{"id":"`+lost+`"}`)
	mustWrite(t, filepath.Join(store.WorkspaceDir(lost), FilesDirName, "notes.md"), "gone")
	mustWrite(t, filepath.Join(store.WorkspaceDir(live), FilesDirName, "keep.md"), "kept")
	// The memory is nobody's in particular.
	mustWrite(t, filepath.Join(memory, "likes-go.md"), "shared")

	store.SweepOrphans()

	if _, err := os.Stat(filepath.Join(instances, lost)); !os.IsNotExist(err) {
		t.Fatalf("want the lost instance removed, got %v", err)
	}
	for _, path := range []string{
		filepath.Join(instances, live, "transcript.json"),
		filepath.Join(store.WorkspaceDir(live), FilesDirName, "keep.md"),
		filepath.Join(memory, "likes-go.md"),
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("want %s kept: %v", path, err)
		}
	}
}

// And the safety that makes the sweep something other than a data shredder: a
// state file that does not parse is quarantined and reads as absent afterwards,
// so an index that cannot be read means nothing is touched. The same holds for
// a missing one and an empty one.
func TestTheSweepTouchesNothingWithoutAReadableIndex(t *testing.T) {
	live := "11111111-1111-4111-8111-111111111111"
	for _, one := range []struct {
		name  string
		write func(path string)
	}{
		{"no index at all", func(string) {}},
		{"an empty index", func(path string) { mustWrite(t, path, "") }},
		{"an index that does not parse", func(path string) { mustWrite(t, path, "{not json") }},
		{"an index that lists nobody", func(path string) { mustWrite(t, path, "[]") }},
	} {
		t.Run(one.name, func(t *testing.T) {
			dir := t.TempDir()
			index, instances, _ := Paths(dir)
			store := NewStoreAt(index, instances)
			mustWrite(t, filepath.Join(instances, live, "transcript.json"), `{"id":"`+live+`"}`)
			mustWrite(t, filepath.Join(store.WorkspaceDir(live), FilesDirName, "keep.md"), "kept")
			one.write(index)

			store.SweepOrphans()

			for _, path := range []string{
				filepath.Join(instances, live, "transcript.json"),
				filepath.Join(store.WorkspaceDir(live), FilesDirName, "keep.md"),
			} {
				if _, err := os.Stat(path); err != nil {
					t.Fatalf("the sweep deleted %s without an index it could read: %v", path, err)
				}
			}
		})
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("create %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
