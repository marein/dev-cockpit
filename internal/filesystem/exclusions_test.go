package filesystem

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestParseExclusionsAcceptsWhatAFormProduces(t *testing.T) {
	// Newlines are what the textarea sends; commas and stray whitespace are what
	// someone pasting a list sends.
	ex := ParseExclusions(" vendor \n\nnode_modules,var\r\n/tests/_output/\n\tbuild\n")
	want := []string{"build", "node_modules", "var", "vendor", "tests/_output"}
	if got := ex.List(); !reflect.DeepEqual(got, want) {
		t.Errorf("List() = %v, want %v", got, want)
	}
	if ex.Len() != 5 {
		t.Errorf("Len() = %d, want 5", ex.Len())
	}
}

func TestParseExclusionsDropsNonsense(t *testing.T) {
	// Nothing here may become an entry: an empty list, the project root itself,
	// or a path climbing out of it.
	for _, raw := range []string{"", "   ", "\n\n", ".", "..", "/", "///", "../outside", "a/../../b"} {
		if got := ParseExclusions(raw); got.Len() != 0 {
			t.Errorf("ParseExclusions(%q) = %v, want empty", raw, got.List())
		}
	}
}

func TestParseExclusionsCollapsesDuplicates(t *testing.T) {
	ex := ParseExclusions("vendor\nvendor\n/vendor/\nvendor")
	if got := ex.List(); !reflect.DeepEqual(got, []string{"vendor"}) {
		t.Errorf("List() = %v, want [vendor]", got)
	}
}

func TestExclusionsSkipDir(t *testing.T) {
	ex := ParseExclusions("vendor\ntests/_output")
	cases := []struct {
		rel, name string
		skip      bool
	}{
		{"vendor", "vendor", true},              // bare name at the root
		{"src/api/vendor", "vendor", true},      // same name deeper down
		{"tests/_output", "_output", true},      // exact path
		{"src/tests/_output", "_output", false}, // same name, different path
		{"src", "src", false},                   // untouched
		{"vendorish", "vendorish", false},       // no prefix matching
	}
	for _, c := range cases {
		if got := ex.SkipDir(c.rel, c.name); got != c.skip {
			t.Errorf("SkipDir(%q, %q) = %v, want %v", c.rel, c.name, got, c.skip)
		}
	}
}

func TestExclusionsEqual(t *testing.T) {
	a := ParseExclusions("vendor\nvar\ntests/_output")
	if !a.Equal(ParseExclusions("var,vendor\n/tests/_output")) {
		t.Error("same entries in another order must be equal")
	}
	if a.Equal(ParseExclusions("vendor\nvar")) {
		t.Error("a missing entry must not be equal")
	}
	if a.Equal(ParseExclusions("vendor\nvar\ntests/_output\nbuild")) {
		t.Error("an extra entry must not be equal")
	}
	if a.Equal(ParseExclusions("vendor\nvar\n_output")) {
		t.Error("a path and a bare name are not the same rule")
	}
}

func TestDefaultExclusionsAreGitOnly(t *testing.T) {
	// The default excludes git's storage and nothing else. Dependency folders are
	// searchable out of the box: what they cost is relevance, not time, so
	// dropping them is a per-project choice on the settings form.
	ex := DefaultExclusionSet()
	if !ex.SkipDir(".git", ".git") {
		t.Error("default exclusions must skip .git")
	}
	for _, name := range []string{"node_modules", "vendor", "var", ".worktrees"} {
		if ex.SkipDir(name, name) {
			t.Errorf("%q must not be excluded by default; that is a choice, not an accident", name)
		}
	}
	if ex.Len() != 1 {
		t.Errorf("default exclusions = %v, want exactly [.git]", ex.List())
	}
}

// TestExclusionsChangeRebuildsTheIndex is the reason the index remembers what it
// was built with: saving the setting has to take effect without any hook.
func TestExclusionsChangeRebuildsTheIndex(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"src/app.php", "vendor/lib/app.php"} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cache := NewQuickOpenCache()
	clock := time.Now()
	cache.now = func() time.Time { return clock }

	res, err := cache.Query(root, "app", "", DefaultExclusionSet(), QuickOpenLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 2 {
		t.Fatalf("with default exclusions got %v, want both files", res.Paths)
	}

	// Same clock, same project: only the setting changed, and that is enough.
	res, err = cache.Query(root, "app", "", ParseExclusions("vendor"), QuickOpenLimit)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Paths, []string{"src/app.php"}) {
		t.Errorf("after excluding vendor got %v, want only src/app.php", res.Paths)
	}

	// And back again, without the stale index leaking through.
	res, err = cache.Query(root, "app", "", DefaultExclusionSet(), QuickOpenLimit)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Paths) != 2 {
		t.Errorf("switching back got %v, want both files", res.Paths)
	}
}

func TestSearchRespectsConfiguredExclusions(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"src/keep.php", "vendor/drop.php", "tests/_output/drop.php"} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("needle\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	matches, _, err := SearchFiles(root, "needle", false, ParseExclusions("vendor\ntests/_output"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 || matches[0].Path != "src/keep.php" {
		t.Errorf("matches = %v, want only src/keep.php", matches)
	}
}

// TestQuickOpenScopeNarrowsToAFolder covers the ?path= the files endpoint takes:
// one index serves the whole project, a scope just narrows the answer.
func TestQuickOpenScopeNarrowsToAFolder(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{"src/a/app.php", "src/b/app.php", "lib/app.php"} {
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := buildQuickOpenIndex(root, DefaultExclusionSet(), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if got := ix.query("app", "", QuickOpenLimit); len(got.Paths) != 3 {
		t.Errorf("unscoped = %v, want all three", got.Paths)
	}
	got := ix.query("app", "src", QuickOpenLimit)
	want := []string{"src/a/app.php", "src/b/app.php"}
	if !reflect.DeepEqual(got.Paths, want) {
		t.Errorf("scoped to src = %v, want %v", got.Paths, want)
	}
	if got.Total != 2 {
		t.Errorf("scoped total = %d, want 2", got.Total)
	}
	// A scope must not match a folder that merely starts with the same letters.
	if got := ix.query("app", "sr", QuickOpenLimit); len(got.Paths) != 0 {
		t.Errorf("scope %q matched %v, want nothing", "sr", got.Paths)
	}
	// An empty query is still scoped.
	if got := ix.query("", "lib", QuickOpenLimit); !reflect.DeepEqual(got.Paths, []string{"lib/app.php"}) {
		t.Errorf("empty scoped query = %v", got.Paths)
	}
}
