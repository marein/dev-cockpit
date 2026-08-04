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
