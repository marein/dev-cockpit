package claude

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func writeTranscript(t *testing.T, root, project, id string, lines ...string) string {
	t.Helper()
	path := filepath.Join(root, project, id+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func cwdLine(cwd string) string {
	return `{"type":"user","cwd":"` + cwd + `","timestamp":"2026-07-01T10:00:00Z"}`
}

func titleLine(title string) string {
	return `{"type":"custom-title","customTitle":"` + title + `"}`
}

func sessionNames(t *testing.T, r *sessionRepository) []string {
	t.Helper()
	var names []string
	for _, s := range r.List() {
		names = append(names, s.Name)
	}
	return names
}

func TestListReadsTranscripts(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	r := &sessionRepository{stateRoot: root}
	writeTranscript(t, root, "p1", "aaa", cwdLine(cwd), titleLine("one"))
	writeTranscript(t, root, "p2", "bbb", cwdLine(cwd), titleLine("two"))
	writeTranscript(t, root, "p2", "ccc", `{"type":"user"}`, "not json")

	names := sessionNames(t, r)
	sort.Strings(names)
	if strings.Join(names, ",") != "one,two" {
		t.Fatalf("names = %v, want one and two", names)
	}
}

func TestListCachesUnchangedTranscripts(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	r := &sessionRepository{stateRoot: root}
	path := writeTranscript(t, root, "p1", "aaa", cwdLine(cwd), titleLine("before"))
	if names := sessionNames(t, r); len(names) != 1 || names[0] != "before" {
		t.Fatalf("names = %v, want before", names)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, root, "p1", "aaa", cwdLine(cwd), titleLine("after1"))
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	if names := sessionNames(t, r); len(names) != 1 || names[0] != "before" {
		t.Fatalf("names = %v, want cached before", names)
	}

	writeTranscript(t, root, "p1", "aaa", cwdLine(cwd), titleLine("changed"))
	if names := sessionNames(t, r); len(names) != 1 || names[0] != "changed" {
		t.Fatalf("names = %v, want changed", names)
	}
}

func TestListDropsDeletedTranscript(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	r := &sessionRepository{stateRoot: root}
	path := writeTranscript(t, root, "p1", "aaa", cwdLine(cwd), titleLine("one"))
	if names := sessionNames(t, r); len(names) != 1 {
		t.Fatalf("names = %v, want one entry", names)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if names := sessionNames(t, r); len(names) != 0 {
		t.Fatalf("names = %v, want none", names)
	}
	if len(r.cache) != 0 {
		t.Fatalf("cache has %d entries, want none", len(r.cache))
	}
}

func TestListDropsVanishedCWD(t *testing.T) {
	root, cwd := t.TempDir(), t.TempDir()
	r := &sessionRepository{stateRoot: root}
	writeTranscript(t, root, "p1", "aaa", cwdLine(cwd), titleLine("one"))
	if names := sessionNames(t, r); len(names) != 1 {
		t.Fatalf("names = %v, want one entry", names)
	}
	if err := os.RemoveAll(cwd); err != nil {
		t.Fatal(err)
	}
	if names := sessionNames(t, r); len(names) != 0 {
		t.Fatalf("names = %v, want none", names)
	}
}
