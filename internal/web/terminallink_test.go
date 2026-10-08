package web

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marein/dev-cockpit/internal/project"
)

func TestTerminalLinkTarget(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	demo := filepath.Join(root, "demo")
	for _, dir := range []string{filepath.Join(demo, "src"), filepath.Join(root, "other")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, link := range [][2]string{
		{"/etc", filepath.Join(demo, "esc")},
		{filepath.Join(root, "other"), filepath.Join(demo, "sibling")},
		{filepath.Join(demo, "src"), filepath.Join(demo, "alias")},
	} {
		if err := os.Symlink(link[0], link[1]); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{projects: project.NewRepository(root, nil)}

	for _, tc := range []struct{ url, want string }{
		{"file://" + demo + "/src/main.go", "/projects/demo/editor?file=src%2Fmain.go"},
		{"file://" + demo + "/src/main.go#12", "/projects/demo/editor?file=src%2Fmain.go&line=12"},
		{"file://" + demo + "/src/main.go#12:3", "/projects/demo/editor?col=3&file=src%2Fmain.go&line=12"},
		{"file://" + demo + "/src/main.go#L12", "/projects/demo/editor?file=src%2Fmain.go"},
		{"file://" + demo + "/src/main.go#0:3", "/projects/demo/editor?file=src%2Fmain.go"},
		{"file://" + demo + `/src\main.go`, ""},
		{"file://" + demo + "/my%20file%231.go", "/projects/demo/editor?file=my+file%231.go"},
		{"FILE://" + demo + "/a.go", "/projects/demo/editor?file=a.go"},
		{"file://localhost" + demo + "/a.go", "/projects/demo/editor?file=a.go"},
		{"file://LOCALHOST" + demo + "/a.go", "/projects/demo/editor?file=a.go"},
		{"file://" + demo + "/..foo.go", "/projects/demo/editor?file=..foo.go"},
		{"file://" + demo + "/alias/main.go", "/projects/demo/editor?file=alias%2Fmain.go"},
		{"file://" + demo + "/esc/passwd", ""},
		{"file://" + demo + "/sibling/x.go", ""},
		{"file://" + demo + "/../other/x.go", "/projects/other/editor?file=x.go"},
		{"file://" + demo, ""},
		{"file://example.com" + demo + "/a.go", ""},
		{"file:///etc/passwd", ""},
		{"http://localhost" + demo + "/a.go", ""},
	} {
		if got := s.terminalLinkTarget(tc.url); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.url, got, tc.want)
		}
	}
}
