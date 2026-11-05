package editorintelligence

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type seedFixture struct {
	t         *testing.T
	projects  string
	cacheRoot string
	php       *Profile
	indexedAt time.Time
}

func newSeedFixture(t *testing.T) *seedFixture {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !plainPath(base) {
		t.Skipf("temp dir %s holds runes a file URI encodes", base)
	}
	f := &seedFixture{
		t:         t,
		projects:  filepath.Join(base, "projects"),
		cacheRoot: filepath.Join(base, "cache"),
		php:       profileByID("php"),
		indexedAt: time.Now().Add(-time.Hour),
	}
	return f
}

func (f *seedFixture) write(path, content string) {
	f.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		f.t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *seedFixture) checkout(path, content string) {
	f.t.Helper()
	f.write(path, content)
	f.age(path, time.Now().Add(-10*time.Minute))
}

func (f *seedFixture) age(path string, at time.Time) {
	f.t.Helper()
	if err := os.Chtimes(path, at, at); err != nil {
		f.t.Fatal(err)
	}
}

// repo lays out a main working copy and its linked worktrees the way git
// writes them, each holding the given files checked out a while ago.
func (f *seedFixture) repo(main string, worktrees []string, files map[string]string) {
	f.t.Helper()
	mainDir := filepath.Join(f.projects, main)
	f.write(filepath.Join(mainDir, ".git", "HEAD"), "ref: refs/heads/master\n")
	for _, wt := range worktrees {
		gitDir := filepath.Join(mainDir, ".git", "worktrees", wt)
		wtDir := filepath.Join(f.projects, wt)
		f.write(filepath.Join(gitDir, "HEAD"), "ref: refs/heads/"+wt+"\n")
		f.write(filepath.Join(gitDir, "commondir"), "../..\n")
		f.write(filepath.Join(gitDir, "gitdir"), filepath.Join(wtDir, ".git")+"\n")
		f.write(filepath.Join(wtDir, ".git"), "gitdir: "+gitDir+"\n")
	}
	for _, project := range append([]string{main}, worktrees...) {
		for rel, content := range files {
			f.checkout(filepath.Join(f.projects, project, rel), content)
		}
	}
}

// index writes the index intelephense would have saved for the project
// after indexing rels at indexedAt, the source files aged to before it.
// The records file names the project, so a test sees which one a seed took.
func (f *seedFixture) index(project, version string, stamp int64, rels ...string) string {
	f.t.Helper()
	root := filepath.Join(f.projects, project)
	dir := filepath.Join(cacheDir(f.cacheRoot, f.php.Server, project), "storage", intelephenseIndexDir(root))
	var docs []byte
	for id, rel := range append([]string{""}, rels...) {
		uri := fileURI(intelephenseStubs) + "/Core/Core.php"
		if rel != "" {
			uri = fileURI(root) + "/" + rel
			f.age(filepath.Join(root, rel), f.indexedAt.Add(-time.Minute))
		}
		docs = appendTestDocument(docs, uri, uint64(id+1), uint64(f.indexedAt.UnixMilli()))
	}
	for n := 2; n <= intelephenseIndexFiles; n++ {
		content := []byte(fmt.Sprintf("records %d of %s", n, project))
		switch n {
		case 2:
			content = docs
		case 9:
			content = []byte(`{"base":{},"folderConfigMap":[["` + fileURI(root) + `",{}]]}`)
		}
		f.write(filepath.Join(dir, intelephenseIndexFile(n)), string(content))
	}
	f.write(filepath.Join(dir, intelephenseIndexFile(1)), fmt.Sprintf(
		`{"timestamp":%d,"counter":{"next":%d,"holes":[]},"environment":{"documentRoot":%q,"includePaths":[],"phpVersion":"8.5.0","shortOpenTag":false},"compatibility":{"correctForArrayAccessArrayAndTraversableArrayUnionTypes":true},"intelephenseVersion":%q}`,
		stamp, len(rels)+2, fileURI(root), version))
	return dir
}

// appendTestDocument encodes a DocumentIdentifier field by field the way
// the server's protobufjs writes it.
func appendTestDocument(out []byte, uri string, id, indexed uint64) []byte {
	msg := binary.AppendUvarint(nil, 1<<3|2)
	msg = binary.AppendUvarint(msg, uint64(len(uri)))
	msg = append(msg, uri...)
	msg = binary.AppendUvarint(msg, 2<<3)
	msg = binary.AppendUvarint(msg, id)
	msg = binary.AppendUvarint(msg, 3<<3)
	msg = binary.AppendUvarint(msg, 0)
	msg = binary.AppendUvarint(msg, 4<<3)
	msg = binary.AppendUvarint(msg, indexed)
	out = binary.AppendUvarint(out, uint64(len(msg)))
	return append(out, msg...)
}

func (f *seedFixture) seed(project string) {
	f.t.Helper()
	seedIntelephenseIndex(context.Background(), f.cacheRoot, f.php, project, filepath.Join(f.projects, project))
}

func (f *seedFixture) seededDir(project string) string {
	root := filepath.Join(f.projects, project)
	return filepath.Join(cacheDir(f.cacheRoot, f.php.Server, project), "storage", intelephenseIndexDir(root))
}

// serverDocument is one entry of an index the way intelephense 1.18.5 reads
// it back: DocumentIdentifier.decode deletes a time of 0, and
// readStateFromCache calls toObject without defaults, so such an entry, like
// one without the field, carries no time at all.
type serverDocument struct {
	id, flags uint64
	indexed   *int64
}

func serverDocuments(t *testing.T, data []byte) map[string]serverDocument {
	t.Helper()
	out := map[string]serverDocument{}
	for len(data) > 0 {
		size, n := binary.Uvarint(data)
		if n <= 0 || size > uint64(len(data)-n) {
			t.Fatal("the index is cut short")
		}
		msg := data[n : n+int(size)]
		data = data[n+int(size):]
		var uri string
		var doc serverDocument
		for len(msg) > 0 {
			tag, n := binary.Uvarint(msg)
			msg = msg[n:]
			switch tag & 7 {
			case 0:
				v, m := binary.Uvarint(msg)
				msg = msg[m:]
				switch tag >> 3 {
				case 2:
					doc.id = v
				case 3:
					doc.flags = v
				case 4:
					doc.indexed = nil
					if v != 0 {
						at := int64(v)
						doc.indexed = &at
					}
				}
			case 2:
				l, m := binary.Uvarint(msg)
				if tag>>3 == 1 {
					uri = string(msg[m : m+int(l)])
				}
				msg = msg[m+int(l):]
			default:
				t.Fatalf("wire type %d, which the server never writes", tag&7)
			}
		}
		out[uri] = doc
	}
	return out
}

func (f *seedFixture) serverIndex(project string) map[string]serverDocument {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.seededDir(project), intelephenseIndexFile(2)))
	if err != nil {
		f.t.Fatalf("no index for %s: %v", project, err)
	}
	return serverDocuments(f.t, data)
}

// serverQueues answers whether the server's scan over the project's index
// indexes the file again (Indexer.enqueueFolderFilesForIndexing): when its
// mtime in milliseconds is greater than its entry's time. A file without an
// entry reads 0, an entry without a time undefined, which no comparison
// passes.
func (f *seedFixture) serverQueues(project, rel string) bool {
	f.t.Helper()
	path := filepath.Join(f.projects, project, rel)
	fi, err := os.Stat(path)
	if err != nil {
		f.t.Fatal(err)
	}
	doc, known := f.serverIndex(project)[fileURI(path)]
	switch {
	case !known:
		return true
	case doc.indexed == nil:
		return false
	}
	return fi.ModTime().UnixMilli() > *doc.indexed
}

// serverDrops answers whether the server's scan removes the entry: it knows
// the file and the scan does not find it.
func (f *seedFixture) serverDrops(project, rel string) bool {
	f.t.Helper()
	path := filepath.Join(f.projects, project, rel)
	_, known := f.serverIndex(project)[fileURI(path)]
	_, err := os.Stat(path)
	return known && os.IsNotExist(err)
}

func (f *seedFixture) pending(project string) string {
	data, err := os.ReadFile(filepath.Join(cacheDir(f.cacheRoot, f.php.Server, project), seedPendingFile))
	if err != nil {
		return ""
	}
	return string(data)
}

func (f *seedFixture) seededFrom(project string) string {
	f.t.Helper()
	data, err := os.ReadFile(filepath.Join(f.seededDir(project), intelephenseIndexFile(3)))
	if err != nil {
		f.t.Fatalf("no seeded index for %s: %v", project, err)
	}
	return strings.TrimPrefix(string(data), "records 3 of ")
}

func (f *seedFixture) notSeeded(project string) {
	f.t.Helper()
	if _, err := os.Stat(f.seededDir(project)); !os.IsNotExist(err) {
		f.t.Errorf("%s got an index, want a cold start: %v", project, err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(f.seededDir(project)), ".seed-*")); len(leftovers) > 0 {
		f.t.Errorf("a seed left %v behind", leftovers)
	}
}

func TestIntelephenseIndexDirMatchesTheServer(t *testing.T) {
	for root, want := range map[string]string{
		"/root/projects/php-gaming-website":                "71f8a3a2",
		"/root/projects/php-gaming-website-lsp-cache-seed": "6e05494d",
	} {
		if got := intelephenseIndexDir(root); got != want {
			t.Errorf("intelephenseIndexDir(%s) = %s, want %s, the directory the server keeps its index in", root, got, want)
		}
	}
}

func TestIntelephenseSeedKnowsThePinnedServer(t *testing.T) {
	if !strings.Contains(intelephenseDockerfile, "intelephense@"+intelephenseSeedVersion+" ") {
		t.Errorf("the image pins another intelephense than %s, the seed's format: verify the index format of the new version, then move intelephenseSeedVersion", intelephenseSeedVersion)
	}
}

func TestRewrittenDocumentsReadBackByTheServer(t *testing.T) {
	raw := appendTestDocument(nil, "file:///p/a.php", 7, 1234567890123)
	raw = appendTestDocument(raw, "file:///p/b.php", 8, 1234567890123)
	docs, err := parseIndexDocuments(raw)
	if err != nil {
		t.Fatal(err)
	}
	var out []byte
	out = appendIndexDocument(out, docs[0], "file:///q/a.php", 1234567890999)
	out = appendIndexDocument(out, docs[1], "file:///q/b.php", seedStale)
	got := serverDocuments(t, out)
	if a := got["file:///q/a.php"]; len(got) != 2 || a.id != 7 || a.indexed == nil || *a.indexed != 1234567890999 {
		t.Errorf("kept entry reads %+v of %d, want id 7 under its new URI with the seed's time", a, len(got))
	}
	if b := got["file:///q/b.php"]; b.id != 8 || b.indexed == nil || *b.indexed >= time.Date(1971, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() {
		t.Errorf("stale entry reads %+v, want id 8 with a time every file is newer than", b)
	}
	for _, broken := range [][]byte{raw[:len(raw)-1], {0x05, 0x0a, 0x09}, {0x02, 0x10, 0x01}} {
		if _, err := parseIndexDocuments(broken); err == nil {
			t.Errorf("parseIndexDocuments(%x) = nil error, want the unknown shape refused", broken)
		}
	}
}

func TestSeedTakesTheFreshestIndexOfTheSameVersion(t *testing.T) {
	f := newSeedFixture(t)
	f.repo("app", []string{"app-a", "app-b", "app-new", "app-c"}, map[string]string{"src/A.php": "<?php class A {}"})
	f.index("app-a", intelephenseSeedVersion, 100, "src/A.php")
	f.index("app-b", intelephenseSeedVersion, 300, "src/A.php")
	f.index("app-new", "1.18.6", 900, "src/A.php")
	config := filepath.Join(f.seededDir("app-b"), intelephenseIndexFile(9))
	f.age(config, time.Now().Add(time.Second))

	f.seed("app-c")
	if got := f.seededFrom("app-c"); got != "app-b" {
		t.Errorf("seeded from %s, want app-b, the freshest index of the pinned version, its config saved after the rest; the main project has none and is no better than a worktree", got)
	}
	if f.serverQueues("app-c", "src/A.php") {
		t.Error("the server indexes the identical A.php again, want it kept from the seed")
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestSeedKeepsOnlyProvablyValidEntries(t *testing.T) {
	f := newSeedFixture(t)
	f.repo("shop", []string{"shop-wt"}, map[string]string{
		"src/Same.php":       "<?php class Same {}",
		"src/Edited.php":     "<?php class Edited {}",
		"src/Stale.php":      "<?php class Stale {}",
		"src/Fresh.php":      "<?php class Fresh {}",
		"src/Gone.php":       "<?php class Gone {}",
		"src/Resized.php":    "<?php class Resized {}",
		"vendor/lib/L.php":   "<?php class L {}",
		"tools/vendor/T.php": "<?php class T {}",
	})
	f.index("shop", intelephenseSeedVersion, 100,
		"src/Same.php", "src/Edited.php", "src/Stale.php", "src/Fresh.php", "src/Gone.php", "src/Resized.php", "vendor/lib/L.php", "tools/vendor/T.php")
	wt := filepath.Join(f.projects, "shop-wt")
	main := filepath.Join(f.projects, "shop")
	f.checkout(filepath.Join(wt, "src/Edited.php"), "<?php class Editex {}")
	f.age(filepath.Join(main, "src/Stale.php"), f.indexedAt.Add(time.Minute))
	f.age(filepath.Join(wt, "src/Fresh.php"), time.Now().Add(time.Second))
	f.checkout(filepath.Join(wt, "src/Resized.php"), "<?php class Resized { }")
	if err := os.Remove(filepath.Join(wt, "src/Gone.php")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(wt, "vendor")); err != nil {
		t.Fatal(err)
	}

	f.seed("shop-wt")
	if f.serverQueues("shop-wt", "src/Same.php") {
		t.Error("the server indexes an identical file the source indexed unchanged again, want it kept")
	}
	if f.serverQueues("shop-wt", "tools/vendor/T.php") {
		t.Error("the server indexes an identical nested vendor file again, want it kept")
	}
	for rel, why := range map[string]string{
		"src/Edited.php":  "the target's bytes differ",
		"src/Resized.php": "the target's size differs",
		"src/Stale.php":   "the source changed after it was indexed",
		"src/Fresh.php":   "the target's file is newer than the seed",
	} {
		if !f.serverQueues("shop-wt", rel) {
			t.Errorf("the server keeps the source's entry of %s, want it indexed again: %s", rel, why)
		}
	}
	for _, rel := range []string{"src/Gone.php", "vendor/lib/L.php"} {
		if !f.serverDrops("shop-wt", rel) {
			t.Errorf("the server keeps an entry of %s, which the target lacks, want it dropped", rel)
		}
	}
	stub := f.serverIndex("shop-wt")[fileURI(intelephenseStubs)+"/Core/Core.php"]
	if stub.indexed == nil || *stub.indexed != f.indexedAt.UnixMilli() {
		t.Error("the stub entry changed, want it carried over as it was")
	}
	if got := f.pending("shop-wt"); got != "vendor\n" {
		t.Errorf("pending = %q, want vendor, the dependency folder the target still lacks", got)
	}
}

func TestSeedRewritesEveryPathToTheTarget(t *testing.T) {
	f := newSeedFixture(t)
	f.repo("blog", []string{"blog-wt"}, map[string]string{"src/Post.php": "<?php class Post {}", "vendor/x/X.php": "<?php class X {}", "vendor/autoload.php": "<?php"})
	src := f.index("blog", intelephenseSeedVersion, 100, "src/Post.php", "vendor/x/X.php")
	f.seed("blog-wt")
	main, wt := filepath.Join(f.projects, "blog"), filepath.Join(f.projects, "blog-wt")
	dir := f.seededDir("blog-wt")
	for n := 1; n <= intelephenseIndexFiles; n++ {
		data := readFile(t, filepath.Join(dir, intelephenseIndexFile(n)))
		if strings.Contains(data, main+"/") || strings.Contains(data, main+`"`) {
			t.Errorf("%s still names the source: %q", intelephenseIndexFile(n), data)
		}
		if n >= 3 && n <= 8 && data != readFile(t, filepath.Join(src, intelephenseIndexFile(n))) {
			t.Errorf("%s changed, want the records by id carried over as they are", intelephenseIndexFile(n))
		}
	}
	var meta struct {
		Timestamp   int64
		Counter     map[string]any
		Environment map[string]any
		Version     string `json:"intelephenseVersion"`
	}
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(dir, intelephenseIndexFile(1)))), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Environment["documentRoot"] != fileURI(wt) || meta.Environment["phpVersion"] != "8.5.0" || meta.Counter["next"] != float64(4) || meta.Version != intelephenseSeedVersion {
		t.Errorf("data_1 = %+v, want the target's root and every other key kept", meta)
	}
	if got := readFile(t, filepath.Join(dir, intelephenseIndexFile(9))); got != `{"base":{},"folderConfigMap":[["`+fileURI(wt)+`",{}]]}` {
		t.Errorf("data_9 = %s, want the target's folder", got)
	}
	for uri := range f.serverIndex("blog-wt") {
		if !strings.HasPrefix(uri, fileURI(wt)+"/") && !strings.HasPrefix(uri, fileURI(intelephenseStubs)+"/") {
			t.Errorf("entry %s points outside the target", uri)
		}
	}
	if _, err := os.Stat(filepath.Join(cacheDir(f.cacheRoot, f.php.Server, "blog-wt"), seedPendingFile)); !os.IsNotExist(err) {
		t.Error("a seed with every dependency in place left a pending marker")
	}
	for _, rel := range []string{"src/Post.php", "vendor/x/X.php"} {
		if _, err := os.Stat(filepath.Join(main, rel)); err != nil {
			t.Errorf("the source's %s: %v", rel, err)
		}
	}
	if entries, _ := os.ReadDir(wt); len(entries) != 3 {
		t.Errorf("the target's working copy holds %d entries, want .git, src and vendor alone", len(entries))
	}
}

func TestSeedRefusesAnotherServerVersion(t *testing.T) {
	f := newSeedFixture(t)
	f.repo("api", []string{"api-wt"}, map[string]string{"src/A.php": "<?php"})
	f.index("api", "1.17.0", 100, "src/A.php")
	f.seed("api-wt")
	f.notSeeded("api-wt")
}

func TestSeedSkipsASourceItCannotCheck(t *testing.T) {
	f := newSeedFixture(t)
	f.repo("cms", []string{"cms-gone", "cms-torn", "cms-ok", "cms-wt", "cms-cold"}, map[string]string{"src/A.php": "<?php class A {}"})
	f.index("cms-ok", intelephenseSeedVersion, 100, "src/A.php")
	f.index("cms-gone", intelephenseSeedVersion, 300, "src/A.php")
	if err := os.RemoveAll(filepath.Join(f.projects, "cms-gone", "src")); err != nil {
		t.Fatal(err)
	}
	torn := f.index("cms-torn", intelephenseSeedVersion, 200, "src/A.php")
	f.age(filepath.Join(torn, intelephenseIndexFile(1)), time.Now().Add(-time.Hour))

	f.seed("cms-wt")
	if got := f.seededFrom("cms-wt"); got != "cms-ok" {
		t.Errorf("seeded from %s, want cms-ok: the newer cms-gone's files are gone and cms-torn's server is writing its index", got)
	}

	if err := os.RemoveAll(filepath.Join(f.projects, "cms-ok", "src")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(f.seededDir("cms-wt"), intelephenseIndexFile(5))); err != nil {
		t.Fatal(err)
	}
	f.seed("cms-cold")
	f.notSeeded("cms-cold")
}

func TestSeedLeavesAnIndexOfItsOwnAlone(t *testing.T) {
	f := newSeedFixture(t)
	f.repo("lib", []string{"lib-wt"}, map[string]string{"src/A.php": "<?php"})
	f.index("lib", intelephenseSeedVersion, 100, "src/A.php")
	f.index("lib-wt", intelephenseSeedVersion, 50, "src/A.php")
	f.seed("lib-wt")
	if got := f.seededFrom("lib-wt"); got != "lib-wt" {
		t.Errorf("records of %s, want the index the target's own server wrote kept", got)
	}
}

func TestSeedAgainOnceComposerInstalledTheVendor(t *testing.T) {
	f := newSeedFixture(t)
	f.repo("site", []string{"site-wt"}, map[string]string{"src/A.php": "<?php class A {}", "vendor/v/V.php": "<?php class V {}", "vendor/autoload.php": "<?php"})
	f.index("site", intelephenseSeedVersion, 100, "src/A.php", "vendor/v/V.php")
	wt := filepath.Join(f.projects, "site-wt")
	if err := os.RemoveAll(filepath.Join(wt, "vendor")); err != nil {
		t.Fatal(err)
	}
	f.seed("site-wt")
	if !f.serverDrops("site-wt", "vendor/v/V.php") {
		t.Fatal("the server keeps the vendor entry without vendor, want it dropped")
	}
	first := readFile(t, filepath.Join(f.seededDir("site-wt"), intelephenseIndexFile(2)))

	f.checkout(filepath.Join(wt, "vendor/v/V.php"), "<?php class V {}")
	f.seed("site-wt")
	if readFile(t, filepath.Join(f.seededDir("site-wt"), intelephenseIndexFile(2))) != first || f.pending("site-wt") != "vendor\n" {
		t.Fatal("seeded again while composer was still installing, want the seed to wait for its autoloader")
	}

	f.checkout(filepath.Join(wt, "vendor/autoload.php"), "<?php")
	f.seed("site-wt")
	if f.serverQueues("site-wt", "vendor/v/V.php") {
		t.Error("the server indexes the installed identical vendor file, want it kept from a second seed")
	}
	if f.pending("site-wt") != "" {
		t.Error("the pending marker outlived the seed that found every dependency")
	}
}

func TestSeedRetriesAFailedSecondSeed(t *testing.T) {
	f := newSeedFixture(t)
	f.repo("svc", []string{"svc-wt"}, map[string]string{"src/A.php": "<?php class A {}", "vendor/v/V.php": "<?php class V {}", "vendor/autoload.php": "<?php"})
	src := f.index("svc", intelephenseSeedVersion, 100, "src/A.php", "vendor/v/V.php")
	wt := filepath.Join(f.projects, "svc-wt")
	if err := os.RemoveAll(filepath.Join(wt, "vendor")); err != nil {
		t.Fatal(err)
	}
	f.seed("svc-wt")
	first := readFile(t, filepath.Join(f.seededDir("svc-wt"), intelephenseIndexFile(2)))
	f.checkout(filepath.Join(wt, "vendor/v/V.php"), "<?php class V {}")
	f.checkout(filepath.Join(wt, "vendor/autoload.php"), "<?php")

	f.age(filepath.Join(src, intelephenseIndexFile(1)), time.Now().Add(-time.Hour))
	f.seed("svc-wt")
	if readFile(t, filepath.Join(f.seededDir("svc-wt"), intelephenseIndexFile(2))) != first || f.pending("svc-wt") != "vendor\n" {
		t.Fatal("a second seed whose source was busy changed the index or dropped the marker, want both kept for the next start")
	}

	f.age(filepath.Join(src, intelephenseIndexFile(1)), time.Now())
	f.seed("svc-wt")
	if f.serverQueues("svc-wt", "vendor/v/V.php") || f.pending("svc-wt") != "" {
		t.Error("the next start did not seed again, want the vendor file kept and the marker gone")
	}
}

func TestSeedFallsBackToAnIndexWithoutVendor(t *testing.T) {
	f := newSeedFixture(t)
	f.repo("box", []string{"box-wt", "box-nov"}, map[string]string{"src/A.php": "<?php class A {}", "vendor/v/V.php": "<?php class V {}", "vendor/autoload.php": "<?php"})
	f.index("box", intelephenseSeedVersion, 100, "src/A.php")
	f.seed("box-wt")
	if f.serverQueues("box-wt", "src/A.php") || !f.serverQueues("box-wt", "vendor/v/V.php") {
		t.Error("no source holds vendor: want A.php kept from the newest one and vendor left to the server, not a cold start")
	}

	nov := filepath.Join(f.projects, "box-nov")
	if err := os.RemoveAll(filepath.Join(nov, "vendor")); err != nil {
		t.Fatal(err)
	}
	marked := filepath.Join(f.seededDir("box-wt"), intelephenseIndexFile(3))
	f.write(marked, "records 3 of box-wt")
	f.age(marked, f.indexedAt)
	f.seed("box-nov")
	if got := f.seededFrom("box-nov"); got != "box-wt" || f.serverQueues("box-nov", "src/A.php") {
		t.Errorf("box-nov seeded from %s, want box-wt, the newest index: a seed reads as a complete save", got)
	}
	own := readFile(t, filepath.Join(f.seededDir("box-nov"), intelephenseIndexFile(2)))
	f.checkout(filepath.Join(nov, "vendor/v/V.php"), "<?php class V {}")
	f.checkout(filepath.Join(nov, "vendor/autoload.php"), "<?php")
	f.seed("box-nov")
	if readFile(t, filepath.Join(f.seededDir("box-nov"), intelephenseIndexFile(2))) != own || f.pending("box-nov") != "vendor\n" {
		t.Error("an index of the target's own was replaced by one without vendor, want it kept and the marker waiting")
	}
}

func TestSeedPassesOverAnIndexWithoutTheTargetsVendor(t *testing.T) {
	f := newSeedFixture(t)
	files := map[string]string{"src/A.php": "<?php class A {}", "vendor/v/V.php": "<?php class V {}", "vendor/autoload.php": "<?php"}
	f.repo("game", []string{"game-bare", "game-wt", "game-nov"}, files)
	f.index("game", intelephenseSeedVersion, 100, "src/A.php", "vendor/v/V.php")
	f.index("game-bare", intelephenseSeedVersion, time.Now().Add(time.Hour).UnixMilli(), "src/A.php")
	f.seed("game-wt")
	if got := f.seededFrom("game-wt"); got != "game" {
		t.Errorf("seeded from %s, want game: the fresher game-bare holds no vendor, which the target has", got)
	}
	if err := os.RemoveAll(filepath.Join(f.projects, "game-nov", "vendor")); err != nil {
		t.Fatal(err)
	}
	f.seed("game-nov")
	if got := f.seededFrom("game-nov"); got != "game-bare" {
		t.Errorf("seeded from %s, want game-bare, the freshest: a target without vendor misses nothing in it", got)
	}
	f.checkout(filepath.Join(f.projects, "game-nov", "vendor/v/V.php"), "<?php class V {}")
	f.checkout(filepath.Join(f.projects, "game-nov", "vendor/autoload.php"), "<?php")
	f.seed("game-nov")
	if got := f.seededFrom("game-nov"); got != "game" {
		t.Errorf("seeded from %s once vendor arrived, want the records of game, game-bare holds no vendor", got)
	}
	if f.serverQueues("game-nov", "vendor/v/V.php") {
		t.Error("the server indexes the installed identical vendor file, want it kept from a source that holds vendor")
	}
}

func TestSeedIgnoresAWorkingCopyOutsideTheProjects(t *testing.T) {
	f := newSeedFixture(t)
	f.repo("tool", []string{"tool-wt"}, map[string]string{"src/A.php": "<?php"})
	f.index("tool", intelephenseSeedVersion, 100, "src/A.php")
	elsewhere := filepath.Join(filepath.Dir(f.projects), "elsewhere")
	if err := os.Rename(filepath.Join(f.projects, "tool"), elsewhere); err != nil {
		t.Fatal(err)
	}
	f.write(filepath.Join(f.projects, "tool-wt", ".git"), "gitdir: "+filepath.Join(elsewhere, ".git", "worktrees", "tool-wt")+"\n")
	f.seed("tool-wt")
	f.notSeeded("tool-wt")
}
