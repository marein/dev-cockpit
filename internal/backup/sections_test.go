package backup

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

// The project comments stand in the line comments' file of their project,
// so they travel with the projects the way the line comments do.
func TestTheProjectCommentsTravelWithTheLineComments(t *testing.T) {
	src, dirs := testService(t)
	seedSource(t, dirs)
	const comments = `[{"id":"pc1","project":true,"path":"","line":0,"text":"rename","updatedAt":"2026-11-02T08:00:00Z"},{"id":"lc1","path":"a.txt","line":1,"text":"fix","updatedAt":"2026-11-02T08:00:00Z"}]`
	file := filepath.Join("line-comments", "demo.json")
	if err := os.MkdirAll(filepath.Join(dirs.state, "line-comments"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.state, file), []byte(comments), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := src.Export(&buf, []string{"projects"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	dst, dstDirs := testService(t)
	id, err := dst.SavePending(encrypted(t, buf.Bytes()), "test-pw")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if _, err := dst.Apply(id, []string{"projects"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dstDirs.state, file)); err != nil || string(data) != comments {
		t.Fatalf("the comments were not restored: %q, %v", data, err)
	}
}

// The forge hosts the user added and the projects' bindings travel in a
// section of their own, never with the settings, and whoever listens hears
// the sections an import wrote into once all of it is on disk.
func TestTheForgeHostsTravelInTheForgesSection(t *testing.T) {
	src, dirs := testService(t)
	seedSource(t, dirs)
	const hosts = `{"hosts":[{"id":"a1","kind":"gitlab","webURL":"https://git.example.com","aliases":["gl"]}]}`
	const bindings = `{"projects":{"app":{"how":"hand","host":"a1","path":"team/app"},"lab":{"how":"off"}}}`
	if err := os.MkdirAll(filepath.Join(dirs.state, "forge"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.state, "forge", "hosts.json"), []byte(hosts), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.state, "forge", "bindings.json"), []byte(bindings), 0o600); err != nil {
		t.Fatal(err)
	}
	if sec := src.section("forges"); sec == nil || sec.Group != "Cockpit" {
		t.Fatalf("the section %+v", sec)
	}

	var settings bytes.Buffer
	if err := src.Export(&settings, []string{"settings"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if names := archiveNames(t, settings.Bytes()); slices.ContainsFunc(names, func(n string) bool { return strings.Contains(n, "forge") }) {
		t.Fatalf("the settings carry the forges: %v", names)
	}
	var buf bytes.Buffer
	if err := src.Export(&buf, []string{"forges", "settings", "projects"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if names := archiveNames(t, buf.Bytes()); !slices.Contains(names, "data/forges/hosts.json") || !slices.Contains(names, "data/forges/bindings.json") {
		t.Fatalf("the hosts and their bindings did not travel: %v", names)
	}

	dst, dstDirs := testService(t)
	var heard [][]string
	dst.SetRestored(func(sections []string) {
		if _, err := os.Stat(filepath.Join(dstDirs.projects, "demo", "readme.md")); err != nil {
			t.Errorf("heard before the projects were on disk: %v", err)
		}
		heard = append(heard, sections)
	})
	id, err := dst.SavePending(encrypted(t, buf.Bytes()), "test-pw")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if _, err := dst.Apply(id, []string{"forges", "projects"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if len(heard) != 1 || strings.Join(heard[0], " ") != "forges projects" {
		t.Fatalf("heard %v", heard)
	}
	if data, err := os.ReadFile(filepath.Join(dstDirs.state, "forge", "hosts.json")); err != nil || string(data) != hosts {
		t.Fatalf("the hosts were not restored: %q, %v", data, err)
	}
	if data, err := os.ReadFile(filepath.Join(dstDirs.state, "forge", "bindings.json")); err != nil || string(data) != bindings {
		t.Fatalf("the bindings were not restored: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dstDirs.state, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("a section not picked was restored: %v", err)
	}
}

// The forge tokens travel in a section of their own in the Host group, never
// with the settings, and come back readable by the owner alone.
func TestTheForgeTokensTravelInASectionOfTheirOwn(t *testing.T) {
	src, dirs := testService(t)
	seedSource(t, dirs)
	const tokens = `{"tokens":{"github.com":"ghp_16C7e42F292c6912E7710c838347Ae178B4a"}}`
	if err := os.MkdirAll(filepath.Join(dirs.state, "forge"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.state, "forge", "tokens.json"), []byte(tokens), 0o600); err != nil {
		t.Fatal(err)
	}
	if sec := src.section("forge-tokens"); sec == nil || sec.Group != "Host" {
		t.Fatalf("the section %+v", sec)
	}

	var settings bytes.Buffer
	if err := src.Export(&settings, []string{"settings"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if names := archiveNames(t, settings.Bytes()); slices.ContainsFunc(names, func(n string) bool { return strings.Contains(n, "tokens") }) {
		t.Fatalf("the settings carry the tokens: %v", names)
	}
	var buf bytes.Buffer
	if err := src.Export(&buf, []string{"forge-tokens"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if names := archiveNames(t, buf.Bytes()); !slices.Contains(names, "data/forge-tokens/tokens.json") {
		t.Fatalf("the tokens did not travel: %v", names)
	}

	dst, dstDirs := testService(t)
	id, err := dst.SavePending(encrypted(t, buf.Bytes()), "test-pw")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if _, err := dst.Apply(id, []string{"forge-tokens"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	path := filepath.Join(dstDirs.state, "forge", "tokens.json")
	if data, err := os.ReadFile(path); err != nil || string(data) != tokens {
		t.Fatalf("the tokens were not restored: %q, %v", data, err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("the restored tokens are %v, %v", info, err)
	}
}
