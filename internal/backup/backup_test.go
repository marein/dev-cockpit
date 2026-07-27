package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

type testDirs struct {
	state    string
	projects string
	home     string
}

func testService(t *testing.T) (*Service, testDirs) {
	t.Helper()
	dirs := testDirs{state: t.TempDir(), projects: t.TempDir(), home: t.TempDir()}
	return newService(dirs.state, dirs.projects, dirs.home, "test"), dirs
}

func seedSource(t *testing.T, dirs testDirs) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dirs.state, "settings.json"), []byte(`{"terminal-restore":"on"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dirs.home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.home, ".ssh", "id_ed25519"), []byte("KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("id_ed25519", filepath.Join(dirs.home, ".ssh", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dirs.projects, "demo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirs.projects, "demo", "readme.md"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// archiveNames lists the entry names of an exported tar.gz, so a check can
// state which files traveled and which did not.
func archiveNames(t *testing.T, data []byte) []string {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	var names []string
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names = append(names, strings.TrimSuffix(hdr.Name, "/"))
	}
}

func encrypted(t *testing.T, data []byte) *bytes.Reader {
	t.Helper()
	var out bytes.Buffer
	enc, err := NewEncryptWriter(&out, "test-pw")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	return bytes.NewReader(out.Bytes())
}

func TestExportImportRoundtrip(t *testing.T) {
	src, srcDirs := testService(t)
	seedSource(t, srcDirs)

	var buf bytes.Buffer
	if err := src.Export(&buf, []string{"settings", "ssh", "projects", "unknown"}); err != nil {
		t.Fatalf("export: %v", err)
	}

	dst, dstDirs := testService(t)
	id, err := dst.SavePending(encrypted(t, buf.Bytes()), "test-pw")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	m, err := dst.Inspect(id)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(m.Sections) != 3 {
		t.Fatalf("want 3 manifest sections, got %+v", m.Sections)
	}

	res, err := dst.Apply(id, []string{"settings", "ssh", "projects"})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	if res.Sections != 3 || res.Files != 4 || res.Skipped != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}

	data, err := os.ReadFile(filepath.Join(dstDirs.state, "settings.json"))
	if err != nil || string(data) != `{"terminal-restore":"on"}` {
		t.Fatalf("settings not restored: %q, %v", data, err)
	}
	key := filepath.Join(dstDirs.home, ".ssh", "id_ed25519")
	info, err := os.Stat(key)
	if err != nil {
		t.Fatalf("key not restored: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("key mode not preserved: %v", info.Mode())
	}
	if target, err := os.Readlink(filepath.Join(dstDirs.home, ".ssh", "link")); err != nil || target != "id_ed25519" {
		t.Fatalf("symlink not restored: %q, %v", target, err)
	}
	if data, err := os.ReadFile(filepath.Join(dstDirs.projects, "demo", "readme.md")); err != nil || string(data) != "hello" {
		t.Fatalf("project file not restored into the current projects dir: %q, %v", data, err)
	}
}

func TestEncryptedRoundtrip(t *testing.T) {
	src, srcDirs := testService(t)
	seedSource(t, srcDirs)

	var buf bytes.Buffer
	enc, err := NewEncryptWriter(&buf, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if err := src.Export(enc, []string{"settings"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}

	dst, _ := testService(t)
	if _, err := dst.SavePending(bytes.NewReader(buf.Bytes()), "wrong"); err == nil {
		t.Fatal("wrong password accepted")
	}
	if _, err := dst.SavePending(bytes.NewReader(buf.Bytes()), ""); err == nil {
		t.Fatal("missing password accepted")
	}
	if _, err := dst.SavePending(bytes.NewReader(buf.Bytes()[:len(buf.Bytes())-8]), "secret"); err == nil {
		t.Fatal("truncated archive accepted")
	}
	id, err := dst.SavePending(bytes.NewReader(buf.Bytes()), "secret")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if _, err := dst.Inspect(id); err != nil {
		t.Fatalf("inspect: %v", err)
	}
}

func TestOverwriteReviewFlow(t *testing.T) {
	src, srcDirs := testService(t)
	seedSource(t, srcDirs)

	var buf bytes.Buffer
	if err := src.Export(&buf, []string{"settings"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	data := buf.Bytes()

	dst, dstDirs := testService(t)
	target := filepath.Join(dstDirs.state, "settings.json")
	if err := os.WriteFile(target, []byte(`{"terminal-restore":"off"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	importOnce := func() ReviewEntry {
		t.Helper()
		id, err := dst.SavePending(encrypted(t, data), "test-pw")
		if err != nil {
			t.Fatalf("save pending: %v", err)
		}
		if _, err := dst.Apply(id, []string{"settings"}); err != nil {
			t.Fatalf("apply: %v", err)
		}
		list := dst.ReviewList()
		if len(list) != 1 {
			t.Fatalf("want 1 review entry, got %d", len(list))
		}
		return list[0]
	}

	entry := importOnce()
	pre, err := os.ReadFile(target + preImportSuffix)
	if err != nil || string(pre) != `{"terminal-restore":"off"}` {
		t.Fatalf("pre-import copy wrong: %q, %v", pre, err)
	}

	view, err := dst.Merge(entry.ID)
	if err != nil || !view.Text {
		t.Fatalf("merge view: %+v, %v", view, err)
	}
	if view.Previous == view.Current {
		t.Fatal("merge view sides are equal")
	}

	if err := dst.ReviewRestore(entry.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, _ := os.ReadFile(target)
	if string(got) != `{"terminal-restore":"off"}` {
		t.Fatalf("restore did not bring the old file back: %q", got)
	}
	if _, err := os.Lstat(target + preImportSuffix); err == nil {
		t.Fatal("pre-import copy still there after restore")
	}
	if len(dst.ReviewList()) != 0 {
		t.Fatal("review list not empty after restore")
	}

	entry = importOnce()
	if err := dst.MergeSave(entry.ID, "merged\n"); err != nil {
		t.Fatalf("merge save: %v", err)
	}
	got, _ = os.ReadFile(target)
	if string(got) != "merged\n" {
		t.Fatalf("merge save wrote %q", got)
	}
	if len(dst.ReviewList()) != 0 {
		t.Fatal("review list not empty after merge save")
	}

	if err := os.WriteFile(target, []byte(`{"terminal-restore":"off"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	entry = importOnce()
	if err := dst.ReviewKeep(entry.ID); err != nil {
		t.Fatalf("keep: %v", err)
	}
	if _, err := os.Lstat(target + preImportSuffix); err == nil {
		t.Fatal("pre-import copy still there after keep")
	}

	// The target now equals the archive, a re-import must not open a review.
	id, err := dst.SavePending(encrypted(t, data), "test-pw")
	if err != nil {
		t.Fatal(err)
	}
	res, err := dst.Apply(id, []string{"settings"})
	if err != nil || res.Overwritten != 0 {
		t.Fatalf("identical re-import opened a review: %+v, %v", res, err)
	}
	if len(dst.ReviewList()) != 0 {
		t.Fatal("review list not empty after identical re-import")
	}
}

func TestBackupJobLifecycle(t *testing.T) {
	svc, dirs := testService(t)
	seedSource(t, dirs)

	if _, err := svc.StartBackup(nil, "", nil); err == nil {
		t.Fatal("empty selection accepted")
	}

	if _, err := svc.StartBackup([]string{"settings"}, "", nil); err == nil {
		t.Fatal("empty password accepted")
	}

	finished := make(chan StoredBackup, 1)
	entry, err := svc.StartBackup([]string{"settings", "unknown"}, "test-pw", func(b StoredBackup) { finished <- b })
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	final := <-finished
	if final.ID != entry.ID || !final.Done() || final.Bytes == 0 {
		t.Fatalf("unexpected final entry: %+v", final)
	}

	list := svc.ListBackups()
	if len(list) != 1 || !list[0].Done() || len(list[0].Sections) != 1 {
		t.Fatalf("unexpected list: %+v", list)
	}
	path, name, err := svc.BackupFile(final.ID)
	if err != nil || name != final.Name {
		t.Fatalf("backup file: %q %q %v", path, name, err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	dst, _ := testService(t)
	id, err := dst.SavePending(f, "test-pw")
	if err != nil {
		t.Fatalf("stored backup does not import: %v", err)
	}
	if _, err := dst.Inspect(id); err != nil {
		t.Fatalf("inspect: %v", err)
	}

	deleted, err := svc.DeleteBackup(final.ID)
	if err != nil || deleted.Name != final.Name {
		t.Fatalf("delete: %+v, %v", deleted, err)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatal("archive still on disk after delete")
	}
	if len(svc.ListBackups()) != 0 {
		t.Fatal("list not empty after delete")
	}
}

func TestSweepMarksInterruptedJobs(t *testing.T) {
	svc, dirs := testService(t)
	svc.saveBackups([]StoredBackup{{ID: "aaaabbbbccccdddd", Name: "x.tar.gz", State: backupStateRunning}})
	fresh := newService(dirs.state, dirs.projects, dirs.home, "test")
	list := fresh.ListBackups()
	if len(list) != 1 || list[0].State != backupStateFailed {
		t.Fatalf("interrupted job not marked failed: %+v", list)
	}
}

func TestDotfilesAreDynamic(t *testing.T) {
	src, srcDirs := testService(t)
	for name, content := range map[string]string{".bashrc": "alias ll='ls -l'", ".zshrc": "setopt", ".gitconfig": "[user]"} {
		if err := os.WriteFile(filepath.Join(srcDirs.home, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(srcDirs.home, ".config"), 0o755); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := src.Export(&buf, []string{"dotfiles"}); err != nil {
		t.Fatalf("export: %v", err)
	}

	dst, dstDirs := testService(t)
	id, err := dst.SavePending(encrypted(t, buf.Bytes()), "test-pw")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	res, err := dst.Apply(id, []string{"dotfiles"})
	if err != nil || res.Files != 2 || res.Skipped != 0 {
		t.Fatalf("apply: %+v, %v", res, err)
	}
	if data, err := os.ReadFile(filepath.Join(dstDirs.home, ".zshrc")); err != nil || string(data) != "setopt" {
		t.Fatalf("unknown dot file did not import dynamically: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dstDirs.home, ".gitconfig")); err == nil {
		t.Fatal("claimed .gitconfig traveled with the dotfiles section")
	}
	if _, err := os.Stat(filepath.Join(dstDirs.home, ".config")); err == nil {
		t.Fatal("a directory traveled with the dotfiles section")
	}
}

func TestExportSkipsPreImportCopies(t *testing.T) {
	src, srcDirs := testService(t)
	seedSource(t, srcDirs)
	if err := os.WriteFile(filepath.Join(srcDirs.home, ".ssh", "config"+preImportSuffix), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := src.Export(&buf, []string{"ssh"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	dst, _ := testService(t)
	id, err := dst.SavePending(encrypted(t, buf.Bytes()), "test-pw")
	if err != nil {
		t.Fatal(err)
	}
	m, err := dst.Inspect(id)
	if err != nil || len(m.Sections) != 1 {
		t.Fatalf("inspect: %+v, %v", m, err)
	}
	if m.Sections[0].Files != 2 {
		t.Fatalf("pre-import copy traveled, files: %d", m.Sections[0].Files)
	}
}

func TestRejectsUnencryptedArchives(t *testing.T) {
	src, srcDirs := testService(t)
	seedSource(t, srcDirs)

	var buf bytes.Buffer
	if err := src.Export(&buf, []string{"settings"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	tarGz := buf.Bytes()
	gz, err := gzip.NewReader(bytes.NewReader(tarGz))
	if err != nil {
		t.Fatal(err)
	}
	var tarBytes bytes.Buffer
	if _, err := tarBytes.ReadFrom(gz); err != nil {
		t.Fatal(err)
	}

	dst, _ := testService(t)
	if _, err := dst.SavePending(bytes.NewReader(tarGz), "test-pw"); err == nil {
		t.Fatal("plain tar.gz accepted")
	}
	if _, err := dst.SavePending(&tarBytes, "test-pw"); err == nil {
		t.Fatal("plain tar accepted")
	}
}

func TestRejectsForeignFile(t *testing.T) {
	dst, _ := testService(t)
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	hdr := &tar.Header{Name: "hello.txt", Typeflag: tar.TypeReg, Size: 5, Mode: 0o644}
	tw.WriteHeader(hdr)
	tw.Write([]byte("hello"))
	tw.Close()
	gz.Close()
	if _, err := dst.SavePending(&buf, ""); err == nil {
		t.Fatal("foreign tar.gz accepted")
	}
	if _, err := dst.SavePending(bytes.NewReader([]byte("plain text")), ""); err == nil {
		t.Fatal("plain text accepted")
	}
}

func TestApplyBlocksEscapes(t *testing.T) {
	dst, dstDirs := testService(t)
	dstHome := dstDirs.home

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(hdr *tar.Header, data []byte) {
		hdr.Size = int64(len(data))
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	write(&tar.Header{Name: "data/ssh/ssh/../../../../evil.txt", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("evil"))
	write(&tar.Header{Name: "data/ssh/ssh/", Typeflag: tar.TypeDir, Mode: 0o700}, nil)
	write(&tar.Header{Name: "data/ssh/ssh/x", Typeflag: tar.TypeSymlink, Linkname: "../../escape", Mode: 0o777}, nil)
	write(&tar.Header{Name: "data/ssh/ssh/x/pwned", Typeflag: tar.TypeReg, Mode: 0o644}, []byte("pwned"))
	manifest := []byte(`{"app":"dev-cockpit-backup","format":1,"sections":[{"id":"ssh","label":"SSH keys","files":2}]}`)
	write(&tar.Header{Name: "manifest.json", Typeflag: tar.TypeReg, Mode: 0o644}, manifest)
	tw.Close()
	gz.Close()

	id, err := dst.SavePending(encrypted(t, buf.Bytes()), "test-pw")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if _, err := dst.Apply(id, []string{"ssh"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dstHome, "..", "evil.txt")); err == nil {
		t.Fatal("dot dot escape wrote a file")
	}
	if _, err := os.Lstat(filepath.Join(dstHome, "..", "escape", "pwned")); err == nil {
		t.Fatal("symlink escape wrote a file")
	}
	if _, err := os.Lstat(filepath.Join(dstHome, ".ssh", "x", "pwned")); err == nil {
		t.Fatal("file written through a planted symlink")
	}
}

// New durable state is worth nothing in a backup that leaves it out: an import
// would bring the conversations back and the jobs they steer would be gone. The
// jobs file is state like any other, so it belongs in the section that carries
// the assistant.
func TestTheAssistantSectionCarriesTheSteeredJobs(t *testing.T) {
	sections := buildSections("/state", "/projects", "/home/user")
	var assistant *Section
	for i := range sections {
		if sections[i].ID == "assistant" {
			assistant = &sections[i]
		}
	}
	if assistant == nil {
		t.Fatal("there is no assistant section any more")
	}
	for _, source := range assistant.Sources {
		if source.Path == "/state/assistant/jobs.json" {
			return
		}
	}
	t.Fatalf("the steered jobs are not in the backup: %+v", assistant.Sources)
}

// An imported conversation only continues when the provider session travels
// with it, and the jobs it steers point at coder terminals in the projects.
func TestTheAssistantSectionDependsOnTheSessions(t *testing.T) {
	sections := buildSections("/state", "/projects", "/home/user")
	i := slices.IndexFunc(sections, func(sec Section) bool { return sec.ID == "assistant" })
	if i < 0 {
		t.Fatal("there is no assistant section any more")
	}
	want := []string{"projects", "claude-sessions", "copilot-sessions"}
	if !slices.Equal(sections[i].Requires, want) {
		t.Fatalf("requires %v, want %v", sections[i].Requires, want)
	}
	for _, id := range want {
		if !slices.ContainsFunc(sections, func(sec Section) bool { return sec.ID == id }) {
			t.Fatalf("the assistant section requires %q, which no section provides", id)
		}
	}
}

func seedAssistant(t *testing.T, dirs testDirs) {
	t.Helper()
	files := map[string]string{
		"assistant.json":                     `{"conversations":[]}`,
		"conversations/c1.json":              `{"id":"c1"}`,
		"jobs.json":                          `[]`,
		"workspace/CLAUDE.md":                "generated for /state/assistant/workspace",
		"workspace/AGENTS.md":                "generated for /state/assistant/workspace",
		"workspace/memory/likes-go.md":       "---\ntitle: Go\n---\nyes",
		"workspace/assistant-files/note.txt": "scratch",
		"workspace/user-upload/abc/pic.png":  "PNG",
	}
	for rel, content := range files {
		p := filepath.Join(dirs.state, "assistant", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// The instruction files are rebuilt from the memory before every turn and
// carry the exporting host's paths, so they are noise in an archive. Memory,
// the assistant's own files and the attachments a message carried are not.
func TestAssistantExportLeavesTheGeneratedInstructionsOut(t *testing.T) {
	src, srcDirs := testService(t)
	seedAssistant(t, srcDirs)

	var buf bytes.Buffer
	if err := src.Export(&buf, []string{"assistant"}); err != nil {
		t.Fatalf("export: %v", err)
	}
	names := archiveNames(t, buf.Bytes())
	for _, gone := range []string{"data/assistant/workspace/CLAUDE.md", "data/assistant/workspace/AGENTS.md"} {
		if slices.Contains(names, gone) {
			t.Fatalf("%s traveled: %v", gone, names)
		}
	}
	for _, want := range []string{
		"data/assistant/assistant.json",
		"data/assistant/conversations/c1.json",
		"data/assistant/jobs.json",
		"data/assistant/workspace/memory/likes-go.md",
		"data/assistant/workspace/assistant-files/note.txt",
		"data/assistant/workspace/user-upload/abc/pic.png",
	} {
		if !slices.Contains(names, want) {
			t.Fatalf("%s did not travel: %v", want, names)
		}
	}

	dst, dstDirs := testService(t)
	id, err := dst.SavePending(encrypted(t, buf.Bytes()), "test-pw")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if _, err := dst.Apply(id, []string{"assistant"}); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(dstDirs.state, "assistant", "workspace", "user-upload", "abc", "pic.png")); err != nil || string(data) != "PNG" {
		t.Fatalf("attachment not restored: %q, %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(dstDirs.state, "assistant", "workspace", "CLAUDE.md")); err == nil {
		t.Fatal("the generated instructions reached the target host")
	}
}

// Export side only: an archive written before the exclusion still carries the
// instruction files, and it must import unchanged. Sync overwrites them on the
// next start anyway.
func TestOldAssistantArchivesStillImport(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	write := func(name, content string) {
		t.Helper()
		hdr := &tar.Header{Name: name, Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(content))}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	write("data/assistant/assistant.json", `{"conversations":[]}`)
	write("data/assistant/workspace/CLAUDE.md", "old generated file")
	write("data/assistant/workspace/AGENTS.md", "old generated file")
	write("data/assistant/workspace/memory/likes-go.md", "yes")
	write("data/assistant/workspace/user-upload/abc/pic.png", "PNG")
	write(manifestName, `{"app":"dev-cockpit-backup","format":1,"sections":[{"id":"assistant","label":"Assistant","files":5}]}`)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	dst, dstDirs := testService(t)
	id, err := dst.SavePending(encrypted(t, buf.Bytes()), "test-pw")
	if err != nil {
		t.Fatalf("save pending: %v", err)
	}
	res, err := dst.Apply(id, []string{"assistant"})
	if err != nil || res.Skipped != 0 {
		t.Fatalf("apply: %+v, %v", res, err)
	}
	if res.Files != 5 {
		t.Fatalf("an old archive lost files on import: %+v", res)
	}
	for rel, want := range map[string]string{
		"assistant.json":                    `{"conversations":[]}`,
		"workspace/CLAUDE.md":               "old generated file",
		"workspace/memory/likes-go.md":      "yes",
		"workspace/user-upload/abc/pic.png": "PNG",
	} {
		p := filepath.Join(dstDirs.state, "assistant", filepath.FromSlash(rel))
		if data, err := os.ReadFile(p); err != nil || string(data) != want {
			t.Fatalf("%s: %q, %v", rel, data, err)
		}
	}
}
