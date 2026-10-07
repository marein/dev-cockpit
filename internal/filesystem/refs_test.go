package filesystem

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFileRefFinder(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(projects, "app")
	sibling := filepath.Join(projects, "app-wt")
	for _, name := range []string{"app/main.go", "app/internal/web/router.go", "app/Makefile", "app/~/home.txt", "app-wt/main.go", "other/secret.go"} {
		full := filepath.Join(projects, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(projects, "other/secret.go"), filepath.Join(root, "link.go")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(sibling, "main.go"), filepath.Join(root, "wt.go")); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		text string
		want []FileRef
	}{
		{"plain", "see main.go", []FileRef{{Start: 4, End: 11, Path: "main.go"}}},
		{"line and column", "internal/web/router.go:12:5: undefined", []FileRef{{Start: 0, End: 27, Path: "internal/web/router.go", Line: 12, Col: 5}}},
		{"line", "at ./main.go:7.", []FileRef{{Start: 3, End: 14, Path: "main.go", Line: 7}}},
		{"absolute inside", root + "/main.go:3", []FileRef{{Start: 0, End: len(root) + 10, Path: "main.go", Line: 3}}},
		{"absolute elsewhere", projects + "/other/secret.go", nil},
		{"look-alike sibling", sibling + "/main.go", nil},
		{"proc root", "/proc/self/root" + root + "/main.go", nil},
		{"symlink out of the root", "link.go", nil},
		{"symlink into another project", "wt.go", nil},
		{"escape", "../other/secret.go", nil},
		{"escape inside", "internal/../../other/secret.go", nil},
		{"encoded escape", "%2e%2e/other/secret.go", nil},
		{"backslash", `internal\web\router.go`, nil},
		{"home", "~/home.txt", nil},
		{"file url", "file://" + root + "/main.go", nil},
		{"directory", "internal/web", nil},
		{"missing", "nothere.go:4", nil},
		{"bare word", "Makefile is here", nil},
		{"bare word with line", "Makefile:9", []FileRef{{Start: 0, End: 10, Path: "Makefile", Line: 9}}},
		{"url", "https://example.com/main.go", nil},
		{"quoted", `"main.go"`, []FileRef{{Start: 1, End: 8, Path: "main.go"}}},
		{"in a tool line", "Update(main.go)", []FileRef{{Start: 7, End: 14, Path: "main.go"}}},
		{"utf16 offsets", "😀 main.go", []FileRef{{Start: 3, End: 10, Path: "main.go"}}},
		{"twice", "main.go main.go", []FileRef{{Start: 0, End: 7, Path: "main.go"}, {Start: 8, End: 15, Path: "main.go"}}},
	}
	find := FileRefFinder(root)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := find(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("FileRefFinder(%q) = %+v, want %+v", tc.text, got, tc.want)
			}
		})
	}
}
