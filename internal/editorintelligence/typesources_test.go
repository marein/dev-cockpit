package editorintelligence

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A type search reads a project file from the project, a source under a
// root on this host from there and one in an image out of the image, all
// of an answer in one container and kept for the next answer. A path under
// no root, and a file the image does not hold, answer no line.
func TestSourceLinesReadsEveryRoot(t *testing.T) {
	work := t.TempDir()
	argsFile := filepath.Join(work, "docker-args")
	installFakeDocker(t, argsFile, filepath.Join(work, "marker"), "", "")
	installFakeImage(t, fakeImageFiles)
	root, mod := t.TempDir(), t.TempDir()
	for p, content := range map[string]string{filepath.Join(root, "own.go"): "package own\ntype Own struct{}\n", filepath.Join(mod, "dep.go"): "package dep\ntype Dep interface{}\n"} {
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	roots := []SourceRoot{{Path: mod}, {Path: "/usr/local/go/src", Image: "dev-cockpit-gopls:test"}}
	images := &imageSources{}
	lines := newSourceLines(root, roots, images, DockerLauncher(t.TempDir(), nil))

	if text, ok, missing := lines.line("own.go", 2); text != "type Own struct{}" || !ok || missing {
		t.Fatalf("a project file: %q %v %v", text, ok, missing)
	}
	for _, p := range []string{filepath.Join(mod, "dep.go"), "/usr/local/go/src/io/io.go", "/usr/local/go/src/gone.go"} {
		if _, ok, missing := lines.line(p, 2); ok || !missing {
			t.Fatalf("%s is read before the fetch: %v %v", p, ok, missing)
		}
	}
	if _, ok, missing := lines.line("/etc/passwd", 1); ok || missing {
		t.Fatal("a path under no root is no file to fetch")
	}
	lines.fetch(context.Background())
	if text, ok, _ := lines.line(filepath.Join(mod, "dep.go"), 2); text != "type Dep interface{}" || !ok {
		t.Fatalf("a source on this host: %q %v", text, ok)
	}
	if text, ok, _ := lines.line("/usr/local/go/src/io/io.go", 86); text != "type Reader interface {" || !ok {
		t.Fatalf("a source in the image: %q %v", text, ok)
	}
	if _, ok, missing := lines.line("/usr/local/go/src/gone.go", 1); ok || missing {
		t.Fatal("a file the image does not hold answers no line and is not fetched again")
	}
	args, _ := os.ReadFile(argsFile)
	if got := strings.Count(string(args), "--entrypoint tar"); got != 1 {
		t.Fatalf("%d reads out of the image:\n%s", got, args)
	}

	again := newSourceLines(root, roots, images, DockerLauncher(t.TempDir(), nil))
	if text, ok, missing := again.line("/usr/local/go/src/io/io.go", 86); text != "type Reader interface {" || !ok || missing {
		t.Fatalf("the next answer reads the image's file from the cache: %q %v %v", text, ok, missing)
	}
}

// The cache keeps the newest files within its bound and never one twice.
func TestImageSourcesKeepsTheNewestWithinItsBound(t *testing.T) {
	images := &imageSources{}
	half := strings.Repeat("x", imageSourcesBytes/2-1)
	images.put("img", "/a", half)
	images.put("img", "/a", "other")
	images.put("img", "/b", half)
	if lines, _ := images.get("img", "/a"); len(lines) != 1 || lines[0] != half {
		t.Fatal("a file read again replaced the kept one")
	}
	images.put("img", "/c", "small")
	if _, ok := images.get("img", "/a"); ok {
		t.Fatal("the oldest file outlived the bound")
	}
	for _, p := range []string{"/b", "/c"} {
		if _, ok := images.get("img", p); !ok {
			t.Fatalf("%s is gone", p)
		}
	}
	if _, ok := images.get("other-img", "/c"); ok {
		t.Fatal("a file of another image")
	}
	if images.bytes != len(half)+1+len("small")+1 {
		t.Fatalf("bytes %d", images.bytes)
	}
}
