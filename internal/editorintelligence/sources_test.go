package editorintelligence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The allowlist is the whole rule of the read route, so it is spelled out
// here: what a root holds, and everything that looks like it does.
func TestSourceRootHolds(t *testing.T) {
	root := SourceRoot{Path: "/state/editor-lsp/dev-cockpit-gopls-app/mod"}
	for _, p := range []string{
		"/state/editor-lsp/dev-cockpit-gopls-app/mod/example.com/dep@v1.0.0/dep.go",
		"/state/editor-lsp/dev-cockpit-gopls-app/mod/a",
	} {
		if !root.Holds(p) {
			t.Errorf("%q must be inside the root", p)
		}
	}
	for _, p := range []string{
		"",
		// The root is a directory, not a file in itself.
		"/state/editor-lsp/dev-cockpit-gopls-app/mod",
		"/state/editor-lsp/dev-cockpit-gopls-app/mod/",
		// A sibling that merely starts with the root's name.
		"/state/editor-lsp/dev-cockpit-gopls-app/models/x.go",
		// Another project's cache is another root's business.
		"/state/editor-lsp/dev-cockpit-gopls-other/mod/x.go",
		// Traversal, in every spelling: refused, never cleaned into
		// something that would then pass.
		"/state/editor-lsp/dev-cockpit-gopls-app/mod/../../../../etc/passwd",
		"/state/editor-lsp/dev-cockpit-gopls-app/mod/./x.go",
		"/state/editor-lsp/dev-cockpit-gopls-app/mod//x.go",
		"/etc/passwd",
		// A relative path has no place here at all: the project's own
		// files are the read route's other neighbour and are never this
		// one's business.
		"state/editor-lsp/dev-cockpit-gopls-app/mod/x.go",
		"mod/x.go",
	} {
		if root.Holds(p) {
			t.Errorf("%q must be outside the root", p)
		}
	}
	// An empty root holds nothing, or a profile without image roots would
	// answer for every absolute path there is.
	if (SourceRoot{}).Holds("/etc/passwd") {
		t.Error("the empty root must hold nothing")
	}
}

// FindSourceRoot answers the root a path belongs to, and nothing for a
// path belonging to none.
func TestFindSourceRoot(t *testing.T) {
	roots := []SourceRoot{{Path: "/cache/mod"}, {Path: "/usr/local/go/src", Image: "dev-cockpit-gopls:abc"}}
	if root, ok := FindSourceRoot(roots, "/usr/local/go/src/fmt/print.go"); !ok || root.Image == "" {
		t.Fatalf("stdlib root: %+v %v", root, ok)
	}
	if root, ok := FindSourceRoot(roots, "/cache/mod/x/y.go"); !ok || root.Image != "" {
		t.Fatalf("cache root: %+v %v", root, ok)
	}
	if _, ok := FindSourceRoot(roots, "/usr/local/go/bin/go"); ok {
		t.Fatal("a path beside a root is not in it")
	}
	if _, ok := FindSourceRoot(nil, "/cache/mod/x/y.go"); ok {
		t.Fatal("no roots means nothing is readable")
	}
}

// Reading a file inside a host root: the text comes back, and the root is
// the boundary a symlink cannot walk out of either.
func TestReadHostSource(t *testing.T) {
	dir := t.TempDir()
	mod := filepath.Join(dir, "mod", "example.com", "dep@v1.0.0")
	if err := os.MkdirAll(mod, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mod, "dep.go"), []byte("package dep\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("not yours\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(mod, "escape.go")); err != nil {
		t.Fatal(err)
	}
	root := SourceRoot{Path: filepath.Join(dir, "mod")}

	content, err := readHostSource(root, filepath.Join(mod, "dep.go"))
	if err != nil || content != "package dep\n" {
		t.Fatalf("read: %q %v", content, err)
	}
	if _, err := readHostSource(root, filepath.Join(mod, "escape.go")); err == nil {
		t.Fatal("a symlink out of the root must be refused")
	}
	if _, err := readHostSource(root, filepath.Join(dir, "secret.txt")); err == nil {
		t.Fatal("a path outside the root must be refused")
	}

	// Binary content is no editor buffer, here as little as anywhere.
	if err := os.WriteFile(filepath.Join(mod, "blob.go"), []byte("a\x00b"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := readHostSource(root, filepath.Join(mod, "blob.go")); err == nil {
		t.Fatal("binary content must be refused")
	}
}

// The Docker way's roots for one project: the module downloads inside the
// project's own cache directory, then the trees that live in the image,
// which carry the image they have to be read out of.
func TestDockerSourceRoots(t *testing.T) {
	cacheRoot := t.TempDir()
	goProfile, _, _ := ProfileForPath("main.go")
	php, _, _ := ProfileForPath("index.php")

	roots := dockerSourceRoots(cacheRoot, "my-app", goProfile)
	if len(roots) != 2 {
		t.Fatalf("go roots: %+v", roots)
	}
	wantCache := filepath.Join(cacheRoot, "dev-cockpit-gopls-my-app") + "/mod"
	if roots[0].Path != wantCache || roots[0].Image != "" {
		t.Fatalf("module cache root %+v, want %s on this host", roots[0], wantCache)
	}
	if roots[1].Path != "/usr/local/go/src" || !strings.HasPrefix(roots[1].Image, "dev-cockpit-gopls:") {
		t.Fatalf("standard library root %+v", roots[1])
	}
	// The file cache sits beside the downloads and holds no source, so it
	// is in no root: what the route may read is only ever what a jump can
	// land in.
	for _, root := range roots {
		if strings.HasSuffix(root.Path, "/cache") {
			t.Fatalf("the file cache must not be readable: %+v", root)
		}
	}
	// A PHP dependency lies in the project's own vendor folder, so only
	// the server's stubs stand outside the project.
	phpRoots := dockerSourceRoots(cacheRoot, "my-app", php)
	if len(phpRoots) != 1 || phpRoots[0].Image == "" || !strings.HasSuffix(phpRoots[0].Path, "/stub") {
		t.Fatalf("php roots: %+v", phpRoots)
	}
	// Two projects never share a root.
	other := dockerSourceRoots(cacheRoot, "other-app", goProfile)
	if other[0].Path == roots[0].Path {
		t.Fatal("two projects share one module cache")
	}
}

// A cache directory written the way a module cache is, read only all the
// way down, still comes off the disk when its project goes.
func TestRemoveCacheDirTakesAReadOnlyModuleCache(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "dev-cockpit-gopls-app")
	deep := filepath.Join(dir, "mod", "example.com", "dep@v1.0.0")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(deep, "dep.go"), []byte("package dep\n"), 0o444); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{deep, filepath.Dir(deep), filepath.Dir(filepath.Dir(deep))} {
		if err := os.Chmod(d, 0o555); err != nil {
			t.Fatal(err)
		}
	}
	if err := removeCacheDir(dir); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(dir); err == nil {
		t.Fatal("the cache directory is still there")
	}
}
