package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFileRefs(t *testing.T) {
	projects := t.TempDir()
	root := filepath.Join(projects, "app")
	sibling := filepath.Join(projects, "app-wt")
	for _, name := range []string{"app/main.go", "app/internal/web/router.go", "app/Makefile", "app/12.5", "app/1.2.3", "app/~/home.txt", "app-wt/main.go", "other/secret.go"} {
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
	for _, link := range [][2]string{{"internal", "alias"}, {"../other", "outdir"}, {"../app-wt", "wtdir"}} {
		if err := os.Symlink(link[0], filepath.Join(root, link[1])); err != nil {
			t.Fatal(err)
		}
	}

	cases := []struct {
		name string
		text string
		want []FileRef
	}{
		{"plain", "see main.go", []FileRef{{Start: 4, End: 11, Path: "main.go"}}},
		{"line and column", "internal/web/router.go:12:5: undefined", []FileRef{{Start: 0, End: 27, Path: "internal/web/router.go", Line: 12, Col: 5}}},
		{"line", "at ./main.go:7.", []FileRef{{Start: 3, End: 14, Path: "main.go", Line: 7}}},
		{"a grep line", "main.go:7:func main() {", []FileRef{{Start: 0, End: 9, Path: "main.go", Line: 7}}},
		{"a name with a colon", "main.go:x", nil},
		{"digits glued to a location", "main.go:7:5x", nil},
		{"absolute inside", root + "/main.go:3", []FileRef{{Start: 0, End: len(root) + 10, Path: "main.go", Line: 3}}},
		{"absolute elsewhere", projects + "/other/secret.go", nil},
		{"look-alike sibling", sibling + "/main.go", nil},
		{"proc root", "/proc/self/root" + root + "/main.go", nil},
		{"symlink out of the root", "link.go", nil},
		{"symlink into another project", "wt.go", nil},
		{"a folder symlinked inside", "alias/web/router.go", []FileRef{{Start: 0, End: 19, Path: "alias/web/router.go"}}},
		{"a folder symlinked out", "outdir/secret.go", nil},
		{"a folder symlinked into another project", "wtdir/main.go", nil},
		{"escape", "../other/secret.go", nil},
		{"escape inside", "internal/../../other/secret.go", nil},
		{"encoded escape", "%2e%2e/other/secret.go", nil},
		{"backslash", `internal\web\router.go`, nil},
		{"home", "~/home.txt", nil},
		{"file url", "file://" + root + "/main.go", nil},
		{"directory", "internal/web", nil},
		{"missing", "nothere.go:4", nil},
		{"bare word", "Makefile is here", nil},
		{"no letter", "45:58.25 12.5 ./1.2.3", nil},
		{"bare word with line", "Makefile:9", []FileRef{{Start: 0, End: 10, Path: "Makefile", Line: 9}}},
		{"url", "https://example.com/main.go", nil},
		{"quoted", `"main.go"`, []FileRef{{Start: 1, End: 8, Path: "main.go"}}},
		{"in a tool line", "Update(main.go)", []FileRef{{Start: 7, End: 14, Path: "main.go"}}},
		{"bytes of a wide glyph before", "😀 main.go", []FileRef{{Start: 5, End: 12, Path: "main.go"}}},
		{"control bytes apart", "main.go\x07main.go", []FileRef{{Start: 0, End: 7, Path: "main.go"}, {Start: 8, End: 15, Path: "main.go"}}},
		{"padded with no-break spaces", "\u00a0main.go\u00a0", []FileRef{{Start: 2, End: 9, Path: "main.go"}}},
		{"twice", "main.go main.go", []FileRef{{Start: 0, End: 7, Path: "main.go"}, {Start: 8, End: 15, Path: "main.go"}}},
	}
	refs := NewFileRefs(root)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := refs.Find(tc.text); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Find(%q) = %+v, want %+v", tc.text, got, tc.want)
			}
		})
	}
}

// A file a program writes after its name was looked up links once the answer
// expired.
func TestFileRefsForgetAfterTheirTime(t *testing.T) {
	root := t.TempDir()
	refs := NewFileRefs(root)
	if got := refs.Find("new.go"); got != nil {
		t.Fatalf("a missing file linked: %+v", got)
	}
	if err := os.WriteFile(filepath.Join(root, "new.go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	refs.since = refs.since.Add(-fileRefsTTL - 1)
	if got := refs.Find("new.go"); len(got) != 1 {
		t.Fatalf("the written file did not link: %+v", got)
	}
}

// BenchmarkFileRefsDistinct reads output naming thousands of files once each,
// as find or git status over a large tree writes it.
func BenchmarkFileRefsDistinct(b *testing.B) {
	root := b.TempDir()
	var text strings.Builder
	for i := range 4000 {
		name := fmt.Sprintf("pkg/d%d/sub/f%d.go", i%40, i)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o644); err != nil {
			b.Fatal(err)
		}
		text.WriteString(name + "\n")
	}
	for b.Loop() {
		if refs := NewFileRefs(root).Find(text.String()); len(refs) != 4000 {
			b.Fatalf("%d refs", len(refs))
		}
	}
}
