package settings

import (
	"path/filepath"
	"testing"
)

// TestLookupSeparatesAbsentFromEmpty is why Lookup exists: a setting whose empty
// value is a real choice, such as a folder list someone emptied on purpose,
// cannot tell that apart from never having been set with Get alone.
func TestLookupSeparatesAbsentFromEmpty(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "settings.json"))

	if value, ok := store.Lookup("nothing-here"); ok || value != "" {
		t.Errorf("absent key = (%q, %v), want (\"\", false)", value, ok)
	}

	store.Set("chosen-empty", "")
	value, ok := store.Lookup("chosen-empty")
	if !ok {
		t.Error("a key stored as empty must be reported as present")
	}
	if value != "" {
		t.Errorf("value = %q, want empty", value)
	}
	// Get cannot make that distinction, which is the point.
	if store.Get("chosen-empty") != store.Get("nothing-here") {
		t.Error("Get was expected to be blind to the difference")
	}
}

func TestLookupReadsWhatSetWrote(t *testing.T) {
	store := New(filepath.Join(t.TempDir(), "settings.json"))
	store.Set("a", "one")
	store.Set("b", "two")
	store.Set("a", "three")

	if value, ok := store.Lookup("a"); !ok || value != "three" {
		t.Errorf("a = (%q, %v), want (three, true)", value, ok)
	}
	if value, ok := store.Lookup("b"); !ok || value != "two" {
		t.Errorf("b = (%q, %v), want (two, true)", value, ok)
	}
}

// TestDeleteTakesTheKeyOutOfTheFile is the other half of that distinction:
// putting a setting back to "never answered" means the key has to leave the
// file, because storing an empty value is itself an answer.
func TestDeleteTakesTheKeyOutOfTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	store := New(path)
	store.Set("kept", "value")
	store.Set("going", "[]")

	store.Delete("going")
	if value, ok := store.Lookup("going"); ok || value != "" {
		t.Errorf("deleted key = (%q, %v), want (\"\", false)", value, ok)
	}
	// Only that one goes, and the file really loses it: another process reads
	// the same absence.
	if value, ok := New(path).Lookup("kept"); !ok || value != "value" {
		t.Errorf("kept = (%q, %v), want (value, true)", value, ok)
	}
	if _, ok := New(path).Lookup("going"); ok {
		t.Error("the key is still in the file")
	}
	// Deleting what is not there changes nothing and is not an error.
	store.Delete("going")
	store.Delete("")
	if value, ok := store.Lookup("kept"); !ok || value != "value" {
		t.Errorf("kept = (%q, %v) after the no-op deletes", value, ok)
	}
}

// TestLookupSeesAnotherProcessesWrite covers the store's promise that several
// serve processes sharing a state dir see each other's changes.
func TestLookupSeesAnotherProcessesWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	first, second := New(path), New(path)
	first.Set("shared", "value")
	if value, ok := second.Lookup("shared"); !ok || value != "value" {
		t.Errorf("second store read (%q, %v), want (value, true)", value, ok)
	}
}
