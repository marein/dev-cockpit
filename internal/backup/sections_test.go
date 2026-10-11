package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The project tags and their colors are keyed by project name and tag, so
// they travel with the projects they name, under a source of their own.
func TestTheProjectTagsTravelWithTheProjects(t *testing.T) {
	src, dirs := testService(t)
	seedSource(t, dirs)
	const tags = `{"projects":{"demo":["backend"]},"colors":{"backend":"teal"}}`
	if err := os.WriteFile(filepath.Join(dirs.state, "project-tags.json"), []byte(tags), 0o644); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := src.Export(&buf, []string{"projects"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if names := archiveNames(t, buf.Bytes()); !slices.Contains(names, "data/projects/project-tags.json") {
		t.Fatalf("the tags did not travel: %v", names)
	}

	dst, dstDirs := testService(t)
	id, err := dst.SavePending(encrypted(t, buf.Bytes()), "test-pw")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if _, err := dst.Apply(id, []string{"projects"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dstDirs.state, "project-tags.json")); err != nil || string(data) != tags {
		t.Fatalf("the tags were not restored: %q, %v", data, err)
	}
}
