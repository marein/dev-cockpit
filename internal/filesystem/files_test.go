package filesystem

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func saveString(t *testing.T, dir, name, content string) File {
	t.Helper()
	file, err := SaveFile(dir, name, strings.NewReader(content))
	if err != nil {
		t.Fatalf("save %s: %v", name, err)
	}
	return file
}

func readString(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// A browser calls every clipboard image image.png, so the second paste used to
// replace the first. A taken name counts up in front of the extension, the
// first file keeps its bytes, and the File that comes back names what was
// written, which is what the callers list and adopt.
func TestSaveFileCountsUpInsteadOfReplacing(t *testing.T) {
	dir := t.TempDir()
	want := []string{"image.png", "image-2.png", "image-3.png"}
	for i, name := range want {
		file := saveString(t, dir, "image.png", "paste "+strconv.Itoa(i))
		if file.Name != name {
			t.Fatalf("save %d was written as %q, want %q", i, file.Name, name)
		}
		if file.Path != filepath.Join(dir, name) {
			t.Fatalf("save %d reports path %q", i, file.Path)
		}
	}
	for i, name := range want {
		if got := readString(t, filepath.Join(dir, name)); got != "paste "+strconv.Itoa(i) {
			t.Fatalf("%s holds %q, a later upload replaced it", name, got)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(want) {
		t.Fatalf("the directory holds %d entries, want %d (a temp file was left behind?)", len(entries), len(want))
	}
}

// The counter stands in front of the whole extension chain, so a second
// archive.tar.gz is archive-2.tar.gz and still opens as one. The chain is the
// last part plus every short word before it; a number or a longer word before
// the last dot belongs to the stem.
func TestSaveFileCountsInFrontOfTheExtensionChain(t *testing.T) {
	cases := map[string]string{
		"archive.tar.gz":                        "archive-2.tar.gz",
		"bundle.min.js":                         "bundle-2.min.js",
		"types.d.ts":                            "types-2.d.ts",
		"my.notes.txt":                          "my.notes-2.txt",
		"backup.2026.sql":                       "backup.2026-2.sql",
		"Screenshot 2026-09-17 at 10.23.45.png": "Screenshot 2026-09-17 at 10.23.45-2.png",
		"README":                                "README-2",
		".bashrc":                               ".bashrc-2",
	}
	for name, second := range cases {
		dir := t.TempDir()
		saveString(t, dir, name, "one")
		file := saveString(t, dir, name, "two")
		if file.Name != second {
			t.Errorf("a second %s was written as %q, want %q", name, file.Name, second)
		}
		if got := readString(t, filepath.Join(dir, name)); got != "one" {
			t.Errorf("%s was replaced, it holds %q", name, got)
		}
	}
}

// Two pastes can arrive in the same moment. The name is claimed and placed in
// one step, so every upload ends up in a file of its own with its own bytes.
func TestSaveFileParallelUploadsOfOneNameLandApart(t *testing.T) {
	dir := t.TempDir()
	const uploads = 16
	var wg sync.WaitGroup
	var mu sync.Mutex
	names := map[string]string{}
	for i := 0; i < uploads; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			content := "upload " + strconv.Itoa(i)
			file, err := SaveFile(dir, "image.png", strings.NewReader(content))
			if err != nil {
				t.Errorf("upload %d: %v", i, err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if other, taken := names[file.Name]; taken {
				t.Errorf("%q was handed to two uploads (%q and %q)", file.Name, other, content)
			}
			names[file.Name] = content
		}(i)
	}
	wg.Wait()
	if len(names) != uploads {
		t.Fatalf("%d uploads ended in %d files", uploads, len(names))
	}
	for name, content := range names {
		if got := readString(t, filepath.Join(dir, name)); got != content {
			t.Errorf("%s holds %q, want %q", name, got, content)
		}
	}
}
