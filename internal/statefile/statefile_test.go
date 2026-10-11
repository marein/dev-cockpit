package statefile

import (
	"os"
	"path/filepath"
	"testing"
)

// A caller that writes back what it read has to tell a file it could not read
// apart from one that is not there, or its write replaces state it never saw.
func TestReadTellsAFailedReadFromAnAbsentFile(t *testing.T) {
	dir := t.TempDir()
	got := map[string]int{}
	if err := Read(filepath.Join(dir, "missing.json"), &got); err != nil || len(got) != 0 {
		t.Fatalf("a missing file answered %v, %v", got, err)
	}
	unreadable := filepath.Join(dir, "unreadable.json")
	if err := os.Mkdir(unreadable, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Read(unreadable, &got); err == nil {
		t.Fatal("a file that cannot be read answered no error")
	}
}

// A corrupt file is quarantined and reads as absent, the save that follows
// cannot overwrite what is still recoverable from the broken copy.
func TestReadQuarantinesACorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := map[string]int{}
	if err := Read(path, &got); err != nil || len(got) != 0 {
		t.Fatalf("a corrupt file answered %v, %v", got, err)
	}
	if _, err := os.Stat(path + ".broken"); err != nil {
		t.Fatalf("the corrupt file was not quarantined: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the corrupt file is still in place: %v", err)
	}
}
