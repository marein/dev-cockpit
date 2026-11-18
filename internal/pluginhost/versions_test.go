package pluginhost

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// forgeStub serves the release feeds of github.com, GitHub Enterprise and
// GitLab hosts, keyed by Host header and path, and the release files.
type forgeStub struct {
	*httptest.Server
	mu       sync.Mutex
	releases map[string][]map[string]any
	files    map[string][]byte
	hosts    []string
}

func newForgeStub(t *testing.T) *forgeStub {
	t.Helper()
	f := &forgeStub{releases: map[string][]map[string]any{}, files: map[string][]byte{}}
	f.Server = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.Close)
	return f
}

func (f *forgeStub) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	path := r.URL.EscapedPath()
	f.hosts = append(f.hosts, r.Host+path)
	github := r.Host == "api.github.com" && strings.HasPrefix(path, "/repos/") || r.Host != "api.github.com" && strings.HasPrefix(path, "/api/v3/repos/")
	switch {
	case github && strings.HasSuffix(path, "/releases"):
		host := strings.TrimPrefix(r.Host, "api.")
		rels, ok := f.releases[host+"/"+strings.TrimSuffix(path[strings.Index(path, "/repos/")+len("/repos/"):], "/releases")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(rels)
	case strings.HasPrefix(path, "/api/v4/projects/") && strings.HasSuffix(path, "/releases"):
		repo := r.Host + "/" + strings.ReplaceAll(strings.TrimSuffix(strings.TrimPrefix(path, "/api/v4/projects/"), "/releases"), "%2F", "/")
		rels, ok := f.releases[repo]
		if !ok {
			http.NotFound(w, r)
			return
		}
		var out []map[string]any
		for _, rel := range rels {
			var links []map[string]string
			for _, a := range rel["assets"].([]map[string]string) {
				links = append(links, map[string]string{"name": a["name"], "direct_asset_url": a["browser_download_url"]})
			}
			out = append(out, map[string]any{"tag_name": rel["tag_name"], "name": rel["name"], "description": rel["body"], "assets": map[string]any{"links": links}})
		}
		_ = json.NewEncoder(w).Encode(out)
	default:
		body, ok := f.files[path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}
}

// publish releases version of repo with archives and their checksums file,
// tamper may change the files after.
func (f *forgeStub) publish(repo, version string, archives map[string][]byte, tamper func(files map[string][]byte)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sums strings.Builder
	var assets []map[string]string
	files := map[string][]byte{}
	for name, body := range archives {
		files[name] = body
		sum := sha256.Sum256(body)
		fmt.Fprintf(&sums, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	files["plugin_"+version+"_checksums.txt"] = []byte(sums.String())
	if tamper != nil {
		tamper(files)
	}
	for name, body := range files {
		at := "/files/" + strings.NewReplacer("/", "-", ":", "-").Replace(repo) + "/" + version + "/" + name
		f.files[at] = body
		assets = append(assets, map[string]string{"name": name, "browser_download_url": f.URL + at})
	}
	f.releases[repo] = append(f.releases[repo], map[string]any{"tag_name": "v" + version, "name": "v" + version, "body": "What " + version + " brings.", "assets": assets})
}

// pluginArchive packs a plugin in helper mode at version as a gzipped tar,
// under the directory top when set.
func pluginArchive(t *testing.T, id, version, mode, top string, change func(m map[string]any)) []byte {
	t.Helper()
	folder := t.TempDir()
	dir := writePlugin(t, folder, id, mode, func(m map[string]any) {
		m["version"] = version
		if change != nil {
			change(m)
		}
	})
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if rel == "." {
			return nil
		}
		info, _ := d.Info()
		hdr, _ := tar.FileInfoHeader(info, "")
		hdr.Name = filepath.ToSlash(filepath.Join(top, rel))
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.Type().IsRegular() {
			body, _ := os.ReadFile(path)
			_, err = tw.Write(body)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	return buf.Bytes()
}

func anyArchive(id, version string) string { return id + "_" + version + "_any.tar.gz" }

func newURLHost(t *testing.T, stub *forgeStub) *testHost {
	t.Helper()
	th := newTestHost(t)
	th.cfg.PluginAPI = stub.URL
	th.client = fetchClient(stub.URL)
	return th
}

func installFrom(t *testing.T, th *testHost, url, forge string, auto bool) string {
	t.Helper()
	offer, err := th.Look(context.Background(), url, forge)
	if err != nil {
		t.Fatalf("Look(%s) = %v", url, err)
	}
	id, err := th.Adopt(offer.Token, auto, offer.Replaces)
	if err != nil {
		t.Fatalf("Adopt() = %v", err)
	}
	return id
}

func waitVersion(t *testing.T, th *testHost, id, version string, state State) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, _ := th.Status(id)
		if st.Version == version && st.State == state {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin %s stands %s %q (%s), want %s %q", id, st.Version, st.State, st.Reason, version, state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitSettled(t *testing.T, th *testHost, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for th.origins()[id] != nil && th.origins()[id].Trial {
		if time.Now().After(deadline) {
			t.Fatalf("the trial of %s never ended", id)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestParseRepository(t *testing.T) {
	for _, tc := range []struct{ raw, forge, want string }{
		{"https://github.com/acme/hello", ForgeGitHub, "https://github.com/acme/hello"},
		{"github.com/Acme/hello.git", ForgeGitHub, "https://github.com/Acme/hello"},
		{"https://github.com/acme/hello/releases/tag/v1.0", ForgeGitHub, "https://github.com/acme/hello"},
		{"https://gitlab.com/group/sub/hello/-/releases", ForgeGitLab, "https://gitlab.com/group/sub/hello"},
		{"  https://www.gitlab.com/group/hello/  ", ForgeGitLab, "https://gitlab.com/group/hello"},
		{"https://github.com/acme/hello.js?tab=readme#top", ForgeGitHub, "https://github.com/acme/hello.js"},
		{"https://gitlab.com/group/hello.git/-/tree/master", ForgeGitLab, "https://gitlab.com/group/hello"},
		{"https://Git.Example.test:8443/group/sub/hello/-/tags", ForgeGitLab, "https://git.example.test:8443/group/sub/hello"},
		{"ghe.example.test:443/acme/hello/pulls", ForgeGitHub, "https://ghe.example.test/acme/hello"},
		{"https://10.0.0.5/acme/hello", ForgeGitLab, "https://10.0.0.5/acme/hello"},
		{"https://[fd01::101]:8443/acme/hello", ForgeGitHub, "https://[fd01::101]:8443/acme/hello"},
		{"https://github.com/acme/hello", ForgeGitLab, "https://github.com/acme/hello"},
	} {
		repo, err := ParseRepository(tc.raw, tc.forge)
		if err != nil || repo.URL != tc.want || repo.Forge != tc.forge {
			t.Errorf("ParseRepository(%q, %s) = %q %s, %v, want %q", tc.raw, tc.forge, repo.URL, repo.Forge, err, tc.want)
		}
	}
	for _, tc := range []struct{ raw, forge, want string }{
		{"https://github.com/acme/hello", "", "Pick GitHub or GitLab."},
		{"https://github.com/acme/hello", "gitea", "Pick GitHub or GitLab."},
		{"", ForgeGitHub, "Enter the repository address."},
		{"http://github.com/acme/hello", ForgeGitHub, "must use https"},
		{"https://user@github.com/acme/hello", ForgeGitHub, "no user name or password"},
		{"https://github.com/acme", ForgeGitHub, "Include the owner"},
		{"https://github.com/acme/he llo", ForgeGitHub, "not allowed in a repository name"},
		{"https://github.com/acme/..", ForgeGitHub, "not allowed in a repository name"},
		{"https://github.com/acme/hel\x00lo", ForgeGitHub, "plain text"},
		{"https://169.254.169.254/acme/hello", ForgeGitLab, "Link local"},
		{"https://[fe80::1%25eth0]/acme/hello", ForgeGitHub, "Link local"},
		{"https://git_lab.example/acme/hello", ForgeGitLab, "no valid host"},
		{"https://git.example.test:0/acme/hello", ForgeGitLab, "no valid port"},
	} {
		_, err := ParseRepository(tc.raw, tc.forge)
		if !IsInstallError(err) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("ParseRepository(%q, %q) = %v, want a refusal with %q", tc.raw, tc.forge, err, tc.want)
		}
	}
}

// The feed URL follows the picked forge and the host.
func TestTheFeedFollowsForgeAndHost(t *testing.T) {
	for _, tc := range []struct{ raw, forge, want string }{
		{"github.com/acme/hello", ForgeGitHub, "https://api.github.com/repos/acme/hello/releases?per_page=100"},
		{"ghe.example.test:8443/acme/hello", ForgeGitHub, "https://ghe.example.test:8443/api/v3/repos/acme/hello/releases?per_page=100"},
		{"gitlab.com/group/hello", ForgeGitLab, "https://gitlab.com/api/v4/projects/group%2Fhello/releases?per_page=100"},
		{"git.example.test/group/sub/hello", ForgeGitLab, "https://git.example.test/api/v4/projects/group%2Fsub%2Fhello/releases?per_page=100"},
	} {
		repo, err := ParseRepository(tc.raw, tc.forge)
		if err != nil {
			t.Fatal(err)
		}
		var asked string
		client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			asked = r.URL.String()
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("[]")), Request: r}, nil
		})}
		_, _ = repo.feed(client).Releases(context.Background(), true)
		if asked != tc.want {
			t.Errorf("the feed of %s on %s is %s, want %s", tc.raw, tc.forge, asked, tc.want)
		}
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// fetchClient refuses http outside the dev stub and link local addresses, on
// every hop.
func TestTheFetchClientGuardsEveryHop(t *testing.T) {
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data", http.StatusFound)
	}))
	t.Cleanup(redirect.Close)
	for _, tc := range []struct{ api, url string }{
		{"", redirect.URL + "/x"},
		{redirect.URL, redirect.URL + "/x"},
		{"", "https://169.254.169.254/x"},
	} {
		res, err := fetchClient(tc.api).Get(tc.url)
		if err == nil {
			res.Body.Close()
			t.Errorf("the fetch of %s with api %q went through", tc.url, tc.api)
		}
	}
}

func TestUnpackStaysInside(t *testing.T) {
	type entry struct {
		name, link string
		kind       byte
		body       string
	}
	archive := func(entries ...entry) *bytes.Buffer {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		for _, e := range entries {
			hdr := &tar.Header{Name: e.name, Linkname: e.link, Typeflag: e.kind, Mode: 0o755, Size: int64(len(e.body))}
			if e.kind != tar.TypeReg {
				hdr.Size = 0
			}
			_ = tw.WriteHeader(hdr)
			_, _ = tw.Write([]byte(e.body))
		}
		_ = tw.Close()
		_ = gz.Close()
		return &buf
	}
	manifest := entry{name: "plugin.json", kind: tar.TypeReg, body: "{}"}
	for name, tc := range map[string]struct {
		entries []entry
		want    string
	}{
		"a path up":            {[]entry{manifest, {name: "../evil", kind: tar.TypeReg, body: "x"}}, "leaves the plugin directory"},
		"an absolute path":     {[]entry{manifest, {name: "/etc/evil", kind: tar.TypeReg, body: "x"}}, ""},
		"an absolute link":     {[]entry{manifest, {name: "out", link: "/etc", kind: tar.TypeSymlink}}, "leads out"},
		"a link up":            {[]entry{manifest, {name: "a/out", link: "../../x", kind: tar.TypeSymlink}}, "leads out"},
		"a file through links": {[]entry{manifest, {name: "a", link: ".", kind: tar.TypeSymlink}, {name: "b", link: "a/..", kind: tar.TypeSymlink}, {name: "b/escaped", kind: tar.TypeReg, body: "x"}}, ""},
		"a hard link":          {[]entry{manifest, {name: "h", link: "plugin.json", kind: tar.TypeLink}}, "not a file, directory or link"},
		"a device":             {[]entry{manifest, {name: "dev", kind: tar.TypeChar}}, "not a file, directory or link"},
		"twice":                {[]entry{manifest, manifest}, "twice"},
		"no manifest":          {[]entry{{name: "a/x", kind: tar.TypeReg}, {name: "b/y", kind: tar.TypeReg}}, "no plugin.json"},
	} {
		dir := filepath.Join(t.TempDir(), "plugin")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		_, err := unpack(archive(tc.entries...), dir)
		if err == nil || !IsInstallError(err) || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: unpack() = %v, want a refusal with %q", name, err, tc.want)
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(dir), "evil")); err == nil {
			t.Errorf("%s: a file landed beside the plugin directory", name)
		}
		if _, err := os.Lstat(filepath.Join(filepath.Dir(dir), "escaped")); err == nil {
			t.Errorf("%s: a file landed beside the plugin directory through links", name)
		}
	}
	dir := t.TempDir()
	root, err := unpack(archive(entry{name: "hello-1.0/", kind: tar.TypeDir}, entry{name: "hello-1.0/plugin.json", kind: tar.TypeReg, body: "{}"}, entry{name: "hello-1.0/lib", link: "plugin.json", kind: tar.TypeSymlink}), dir)
	if err != nil || root != filepath.Join(dir, "hello-1.0") {
		t.Fatalf("unpack() of one top directory = %q, %v", root, err)
	}
}

// A plugin installs from a repository's newest fitting release and updates
// to a newer one, keeping the version before and its state.
func TestInstallAndUpdateFromARepository(t *testing.T) {
	stub := newForgeStub(t)
	stub.publish("github.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
	stub.publish("github.com/acme/hello", "1.1.0", map[string][]byte{"demo_1.1.0_darwin_s390.tar.gz": []byte("not for here")}, nil)
	th := newURLHost(t, stub)
	th.start()
	offer, err := th.Look(context.Background(), "github.com/acme/hello", ForgeGitHub)
	if err != nil {
		t.Fatalf("Look() = %v", err)
	}
	if offer.ID != "demo" || offer.Name != "Demo" || offer.Version != "1.0.0" || offer.Repository.URL != "https://github.com/acme/hello" || offer.Replaces != "" || offer.Updates != "" {
		t.Fatalf("the offer is %+v", offer)
	}
	if _, ok := th.Status("demo"); ok {
		t.Fatal("a look installed the plugin")
	}
	if id, err := th.Adopt(offer.Token, false, ""); err != nil || id != "demo" {
		t.Fatalf("Adopt() = %q, %v", id, err)
	}
	if _, err := th.Adopt(offer.Token, false, ""); !errors.Is(err, ErrOfferGone) {
		t.Fatalf("a second Adopt() of one offer = %v", err)
	}
	st := waitVersion(t, th, "demo", "1.0.0", StateRunning)
	if st.Origin.Repository != "https://github.com/acme/hello" || st.Origin.Auto {
		t.Fatalf("the origin reads %+v", st.Origin)
	}
	if at, _ := th.pointsAt("demo"); !strings.HasPrefix(at, "1.0.0-") {
		t.Fatalf("the plugin's entry points at %q", at)
	}
	kept := filepath.Join(th.stateDir, "plugins", "demo", "notes.json")
	if err := os.WriteFile(kept, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}

	stub.publish("github.com/acme/hello", "1.2.0", map[string][]byte{anyArchive("demo", "1.2.0"): pluginArchive(t, "demo", "1.2.0", "serve", "demo-1.2.0", nil)}, nil)
	th.CheckUpdates(context.Background())
	st, _ = th.Status("demo")
	if st.Origin.Step == nil || st.Origin.Step.From != "1.0.0" || st.Origin.Step.To != "1.2.0" || len(st.Origin.Step.Releases) != 1 || st.Origin.Step.Releases[0].Notes != "What 1.2.0 brings." {
		t.Fatalf("the step reads %+v", st.Origin.Step)
	}
	if err := th.Update(context.Background(), "demo", "1.1.0"); !IsInstallError(err) || !strings.Contains(err.Error(), "1.2.0 is out meanwhile") {
		t.Fatalf("an update to a version not the newest = %v", err)
	}
	if err := th.Update(context.Background(), "demo", "1.2.0"); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	waitVersion(t, th, "demo", "1.2.0", StateRunning)
	waitSettled(t, th, "demo")
	rec := th.origins()["demo"]
	if rec.Current.Version != "1.2.0" || rec.Previous == nil || rec.Previous.Version != "1.0.0" {
		t.Fatalf("the origin keeps %+v and %+v", rec.Current, rec.Previous)
	}
	if _, err := os.Stat(filepath.Join(th.versionsOf("demo"), rec.Previous.Dir, ManifestFile)); err != nil {
		t.Fatalf("the version before is gone: %v", err)
	}
	if got, _ := os.ReadFile(kept); string(got) != "mine" {
		t.Fatalf("the plugin's state reads %q after the update", got)
	}
	if st, _ := th.Status("demo"); st.Origin.Step != nil {
		t.Fatalf("a step stays after the update: %+v", st.Origin.Step)
	}
	if err := th.Update(context.Background(), "demo", ""); !IsInstallError(err) || !strings.Contains(err.Error(), "newest release") {
		t.Fatalf("an update without a newer release = %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(th.Dir(), ".fetch-*"))
	if len(leftovers) > 0 {
		t.Fatalf("the fetch left %v", leftovers)
	}

	if err := th.Remove("demo"); err != nil {
		t.Fatalf("Remove() = %v", err)
	}
	if _, err := os.Stat(th.versionsOf("demo")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Remove left the versions: %v", err)
	}
	if th.origins()["demo"] != nil {
		t.Fatal("Remove left the origin")
	}
	if got, _ := os.ReadFile(kept); string(got) != "mine" {
		t.Fatalf("the plugin's state reads %q after the removal", got)
	}
}

// A self hosted GitLab with a subgroup and a GitHub Enterprise host install
// and update over the API of the picked forge, the origin keeps both.
func TestOwnHostsInstallAndUpdate(t *testing.T) {
	stub := newForgeStub(t)
	th := newURLHost(t, stub)
	th.start()
	for _, tc := range []struct{ id, repo, raw, forge, feed string }{
		{"lab", "git.example.test:8443/group/sub/lab", "https://git.example.test:8443/group/sub/lab/-/releases", ForgeGitLab, "git.example.test:8443/api/v4/projects/group%2Fsub%2Flab/releases"},
		{"ghe", "ghe.example.test/acme/ghe", "ghe.example.test/acme/ghe", ForgeGitHub, "ghe.example.test/api/v3/repos/acme/ghe/releases"},
	} {
		stub.publish(tc.repo, "1.0.0", map[string][]byte{anyArchive(tc.id, "1.0.0"): pluginArchive(t, tc.id, "1.0.0", "serve", "", nil)}, nil)
		installFrom(t, th, tc.raw, tc.forge, false)
		waitVersion(t, th, tc.id, "1.0.0", StateRunning)
		rec := th.origins()[tc.id]
		if rec.Repository != "https://"+tc.repo || rec.Forge != tc.forge {
			t.Fatalf("the origin of %s keeps %s on %s", tc.id, rec.Repository, rec.Forge)
		}
		stub.publish(tc.repo, "1.1.0", map[string][]byte{anyArchive(tc.id, "1.1.0"): pluginArchive(t, tc.id, "1.1.0", "serve", "", nil)}, nil)
		th.CheckUpdates(context.Background())
		if st, _ := th.Status(tc.id); st.Origin.Step == nil || st.Origin.Step.To != "1.1.0" || st.Origin.Forge != tc.forge {
			t.Fatalf("the row of %s reads %+v", tc.id, st.Origin)
		}
		if err := th.Update(context.Background(), tc.id, "1.1.0"); err != nil {
			t.Fatalf("Update(%s) = %v", tc.id, err)
		}
		waitVersion(t, th, tc.id, "1.1.0", StateRunning)
		stub.mu.Lock()
		asked := 0
		for _, h := range stub.hosts {
			if h == tc.feed {
				asked++
			}
		}
		stub.mu.Unlock()
		if asked < 3 {
			t.Fatalf("the feed %s was asked %d times: %v", tc.feed, asked, stub.hosts)
		}
	}
	if offer, err := th.Look(context.Background(), "git.example.test:8443/group/sub/lab", ForgeGitHub); err == nil {
		t.Fatalf("the other forge on the same address offered %+v", offer)
	}
}

// An origin kept before Forge existed still updates from github.com or
// gitlab.com.
func TestAnOriginWithoutForgeKeepsUpdating(t *testing.T) {
	stub := newForgeStub(t)
	stub.publish("gitlab.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
	th := newURLHost(t, stub)
	th.start()
	installFrom(t, th, "https://gitlab.com/acme/hello", ForgeGitLab, false)
	waitVersion(t, th, "demo", "1.0.0", StateRunning)
	waitSettled(t, th, "demo")
	th.ops.Lock()
	err := th.changeState(func(st *hostState) { st.Origins["demo"].Forge = "" })
	th.ops.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	stub.publish("gitlab.com/acme/hello", "1.1.0", map[string][]byte{anyArchive("demo", "1.1.0"): pluginArchive(t, "demo", "1.1.0", "serve", "", nil)}, nil)
	th.CheckUpdates(context.Background())
	if st, _ := th.Status("demo"); st.Origin.Step == nil || st.Origin.Forge != ForgeGitLab {
		t.Fatalf("the row reads %+v", st.Origin)
	}
	if err := th.Update(context.Background(), "demo", "1.1.0"); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	waitVersion(t, th, "demo", "1.1.0", StateRunning)
}

// A missing repository or a release that does not fit, fails verification
// or holds a broken plugin is refused and leaves nothing behind.
func TestABadReleaseChangesNothing(t *testing.T) {
	stub := newForgeStub(t)
	good := pluginArchive(t, "demo", "1.0.0", "serve", "", nil)
	stub.publish("github.com/acme/none", "1.0.0", map[string][]byte{"demo_1.0.0_plan9_mips.tar.gz": good}, nil)
	stub.publish("github.com/acme/tampered", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): good}, func(files map[string][]byte) {
		files[anyArchive("demo", "1.0.0")] = append([]byte(nil), pluginArchive(t, "demo", "1.0.0", "exit", "", nil)...)
	})
	stub.publish("github.com/acme/broken", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", func(m map[string]any) { m["run"] = "../escape.sh" })}, nil)
	stub.publish("github.com/acme/mismatch", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "0.9.0", "serve", "", nil)}, nil)
	stub.publish("github.com/acme/notar", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): []byte("plain text")}, nil)
	th := newURLHost(t, stub)
	th.start()
	for repo, want := range map[string]string{
		"acme/missing":  "No public repository at github.com/acme/missing.",
		"acme/none":     "Release 1.0.0 has no archive for " + runtime.GOOS + "/" + runtime.GOARCH + " or any, or no checksums file.",
		"acme/tampered": "does not match its checksum",
		"acme/broken":   "The run path must be a relative path inside the plugin directory.",
		"acme/mismatch": "its plugin.json says version 0.9.0",
		"acme/notar":    "not a gzipped tar",
	} {
		_, err := th.Look(context.Background(), "https://github.com/"+repo, ForgeGitHub)
		if !IsInstallError(err) || !strings.Contains(err.Error(), want) {
			t.Errorf("Look(%s) = %v, want %q", repo, err, want)
		}
	}
	entries, _ := os.ReadDir(th.Dir())
	if len(entries) != 0 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("a refused look left %v in the folder", names)
	}
}

// An update that crashes rolls back: the version before runs again, the
// failed one is deleted and recorded, automatic updates skip it but take a
// newer one.
func TestARollbackGoesBackOnceAndStays(t *testing.T) {
	stub := newForgeStub(t)
	stub.publish("gitlab.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
	th := newURLHost(t, stub)
	th.start()
	installFrom(t, th, "https://gitlab.com/acme/hello", ForgeGitLab, true)
	waitVersion(t, th, "demo", "1.0.0", StateRunning)
	kept := filepath.Join(th.stateDir, "plugins", "demo", "kept")
	_ = os.WriteFile(kept, []byte("x"), 0o600)

	stub.publish("gitlab.com/acme/hello", "1.1.0", map[string][]byte{anyArchive("demo", "1.1.0"): pluginArchive(t, "demo", "1.1.0", "exit", "", nil)}, nil)
	th.CheckUpdates(context.Background())
	st := waitVersion(t, th, "demo", "1.0.0", StateRunning)
	rec := th.origins()["demo"]
	if rec.Failed == nil || rec.Failed.Version != "1.1.0" || rec.Failed.Back != "1.0.0" || !strings.Contains(rec.Failed.Reason, "status 3") || rec.Trial || rec.Previous != nil {
		t.Fatalf("the origin after the rollback is %+v, failed %+v", rec, rec.Failed)
	}
	if st.Origin.Failed == nil || st.Origin.Failed.Version != "1.1.0" || len(st.Origin.Failed.Tail) == 0 {
		t.Fatalf("the row reads %+v", st.Origin.Failed)
	}
	dirs, _ := os.ReadDir(th.versionsOf("demo"))
	if len(dirs) != 1 {
		t.Fatalf("the versions hold %d directories after the rollback", len(dirs))
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("the rollback took the plugin's state: %v", err)
	}

	th.CheckUpdates(context.Background())
	time.Sleep(200 * time.Millisecond)
	if st, _ := th.Status("demo"); st.Version != "1.0.0" || st.Origin.Step == nil || st.Origin.Step.To != "1.1.0" {
		t.Fatalf("an automatic update tried the failed release again: %s, step %+v", st.Version, st.Origin.Step)
	}

	stub.publish("gitlab.com/acme/hello", "1.2.0", map[string][]byte{"demo_1.2.0_" + runtime.GOOS + "_" + runtime.GOARCH + ".tar.gz": pluginArchive(t, "demo", "1.2.0", "serve", "", nil), anyArchive("demo", "1.2.0"): []byte("the any archive is not taken")}, nil)
	th.CheckUpdates(context.Background())
	waitVersion(t, th, "demo", "1.2.0", StateRunning)
	waitSettled(t, th, "demo")
	if rec := th.origins()["demo"]; rec.Failed != nil || rec.Previous == nil || rec.Previous.Version != "1.0.0" {
		t.Fatalf("after a kept update the origin is %+v", rec)
	}
}

// The periodic check installs where automatic updates are on and only shows
// the step where they are off.
func TestTheCheckRunsOnItsOwn(t *testing.T) {
	first := checkFirst
	checkFirst = 50 * time.Millisecond
	t.Cleanup(func() { checkFirst = first })
	stub := newForgeStub(t)
	stub.publish("github.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
	stub.publish("github.com/acme/other", "1.0.0", map[string][]byte{anyArchive("other", "1.0.0"): pluginArchive(t, "other", "1.0.0", "serve", "", nil)}, nil)
	th := newURLHost(t, stub)
	th.cfg.CheckEvery = 100 * time.Millisecond
	th.start()
	installFrom(t, th, "https://github.com/acme/hello", ForgeGitHub, true)
	installFrom(t, th, "https://github.com/acme/other", ForgeGitHub, false)
	stub.publish("github.com/acme/hello", "1.1.0", map[string][]byte{anyArchive("demo", "1.1.0"): pluginArchive(t, "demo", "1.1.0", "serve", "", nil)}, nil)
	stub.publish("github.com/acme/other", "1.1.0", map[string][]byte{anyArchive("other", "1.1.0"): pluginArchive(t, "other", "1.1.0", "serve", "", nil)}, nil)
	waitVersion(t, th, "demo", "1.1.0", StateRunning)
	deadline := time.Now().Add(5 * time.Second)
	for {
		st, _ := th.Status("other")
		if st.Origin.Step != nil && st.Origin.Step.To == "1.1.0" && st.Version == "1.0.0" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the plugin without automatic updates reads %s with step %+v", st.Version, st.Origin.Step)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := th.SetAuto("other", true); err != nil {
		t.Fatalf("SetAuto() = %v", err)
	}
	waitVersion(t, th, "other", "1.1.0", StateRunning)
}

// A restart after an interrupted install or update is consistent: leftovers
// go, a switch the origin names is finished, a trial resumes, and an entry
// removed or replaced by hand loses its origin.
func TestARestartInTheMiddleIsConsistent(t *testing.T) {
	stub := newForgeStub(t)
	stub.publish("github.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
	stub.publish("github.com/acme/bye", "1.0.0", map[string][]byte{anyArchive("bye", "1.0.0"): pluginArchive(t, "bye", "1.0.0", "serve", "", nil)}, nil)
	stub.publish("github.com/acme/hand", "1.0.0", map[string][]byte{anyArchive("hand", "1.0.0"): pluginArchive(t, "hand", "1.0.0", "serve", "", nil)}, nil)
	th := newURLHost(t, stub)
	th.start()
	for _, repo := range []string{"hello", "bye", "hand"} {
		installFrom(t, th, "https://github.com/acme/"+repo, ForgeGitHub, false)
	}
	waitVersion(t, th, "demo", "1.0.0", StateRunning)
	stub.publish("github.com/acme/hello", "1.1.0", map[string][]byte{anyArchive("demo", "1.1.0"): pluginArchive(t, "demo", "1.1.0", "exit", "", nil)}, nil)
	offer, err := th.Look(context.Background(), "https://github.com/acme/hello", ForgeGitHub)
	if err != nil || offer.Updates != "1.0.0" {
		t.Fatalf("Look() of a newer release = %+v, %v", offer, err)
	}
	staged := th.takeOffer(offer.Token).staged
	th.Close()

	// The origin names the update, the link still the release before. A
	// fetch is half done, bye lost its link during a switch, hand is now a
	// plain directory.
	in, err := th.adopt("demo", staged)
	if err == nil {
		err = th.changeState(func(st *hostState) {
			rec := st.Origins["demo"]
			rec.Previous, rec.Current, rec.Trial = rec.Current, in, true
			st.Origins["bye"].Trial = true
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(filepath.Join(th.Dir(), ".fetch-left"), 0o700)
	_ = os.Remove(filepath.Join(th.Dir(), "bye"))
	_ = os.Remove(filepath.Join(th.Dir(), "hand"))
	writePlugin(t, th.Dir(), "hand", "serve", nil)
	stray := filepath.Join(th.Dir(), versionsDir, "stray", "1.0.0-abc")
	_ = os.MkdirAll(stray, 0o700)

	again := newURLHost(t, stub)
	again.cfg.StateDir = th.stateDir
	again.start()
	waitVersion(t, again, "demo", "1.0.0", StateRunning)
	rec := again.origins()["demo"]
	if rec.Failed == nil || rec.Failed.Version != "1.1.0" || rec.Trial {
		t.Fatalf("the resumed trial did not go back: %+v", rec)
	}
	if at, _ := again.pointsAt("bye"); at != again.origins()["bye"].Current.Dir {
		t.Fatalf("the link of bye was not made again: %q", at)
	}
	waitVersion(t, again, "bye", "1.0.0", StateRunning)
	if again.origins()["hand"] != nil {
		t.Fatal("a plugin replaced by hand kept its origin")
	}
	if _, err := os.Stat(again.versionsOf("hand")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a plugin replaced by hand kept its versions")
	}
	for _, gone := range []string{filepath.Join(th.Dir(), ".fetch-left"), stray} {
		if _, err := os.Stat(gone); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s survived the start", gone)
		}
	}
}

// A plugin of the same id from another source is replaced only after the
// person confirms, the same source replaces it without asking.
func TestAnotherSourceAsksFirst(t *testing.T) {
	stub := newForgeStub(t)
	stub.publish("github.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
	th := newURLHost(t, stub)
	th.start()
	source := writePlugin(t, t.TempDir(), "demo", "serve", nil)
	if _, _, err := th.Install(source, false); err != nil {
		t.Fatalf("Install() = %v", err)
	}
	waitState(t, th, "demo", StateRunning)
	if _, replaced, err := th.Install(source, false); err != nil || !replaced {
		t.Fatalf("the same path again = %v, %v", replaced, err)
	}
	offer, err := th.Look(context.Background(), "https://github.com/acme/hello", ForgeGitHub)
	if err != nil || offer.Replaces != source {
		t.Fatalf("the offer replaces %q, %v", offer.Replaces, err)
	}
	var ask *ReplaceError
	if _, err := th.Adopt(offer.Token, false, ""); !errors.As(err, &ask) || ask.From != source {
		t.Fatalf("an install not told of the replace = %v", err)
	}
	if _, err := th.Adopt(offer.Token, false, source); err != nil {
		t.Fatalf("Adopt() told of the replace = %v", err)
	}
	waitVersion(t, th, "demo", "1.0.0", StateRunning)
	if st, _ := th.Status("demo"); st.Origin.Repository == "" {
		t.Fatal("the replaced plugin did not take the repository as its origin")
	}
	if _, _, err := th.Install(source, false); !errors.As(err, &ask) || ask.From != "github.com/acme/hello on GitHub" {
		t.Fatalf("a path over a repository's plugin = %v", err)
	}
	if _, _, err := th.Install(source, true); err != nil {
		t.Fatalf("Install() told of the replace = %v", err)
	}
	waitState(t, th, "demo", StateRunning)
	if _, err := os.Stat(th.versionsOf("demo")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("a path install kept the repository's versions")
	}
	if err := th.Update(context.Background(), "demo", ""); !IsInstallError(err) || !strings.Contains(err.Error(), "has no updates") {
		t.Fatalf("Update() of a plugin copied from a path = %v", err)
	}
}

// An unanswered offer expires with its staging.
func TestAnOfferExpires(t *testing.T) {
	ttl := offerTTL
	offerTTL = 200 * time.Millisecond
	t.Cleanup(func() { offerTTL = ttl })
	stub := newForgeStub(t)
	stub.publish("github.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
	th := newURLHost(t, stub)
	th.start()
	offer, err := th.Look(context.Background(), "https://github.com/acme/hello", ForgeGitHub)
	if err != nil {
		t.Fatalf("Look() = %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		left, _ := filepath.Glob(filepath.Join(th.Dir(), ".fetch-*"))
		if len(left) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the expired offer left %v", left)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := th.Adopt(offer.Token, false, ""); !errors.Is(err, ErrOfferGone) {
		t.Fatalf("Adopt() of an expired offer = %v", err)
	}
}

// A refused automatic update, of a tampered release here, shows why on the
// row and is not downloaded again on the next check.
func TestARefusedAutomaticUpdateIsNotRetried(t *testing.T) {
	stub := newForgeStub(t)
	stub.publish("github.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
	th := newURLHost(t, stub)
	th.start()
	installFrom(t, th, "https://github.com/acme/hello", ForgeGitHub, true)
	waitVersion(t, th, "demo", "1.0.0", StateRunning)
	downloads := 0
	stub.publish("github.com/acme/hello", "1.1.0", map[string][]byte{anyArchive("demo", "1.1.0"): pluginArchive(t, "demo", "1.1.0", "serve", "", nil)}, func(files map[string][]byte) {
		files[anyArchive("demo", "1.1.0")] = []byte("tampered")
	})
	counted := stub.Config.Handler
	stub.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "_any.tar.gz") {
			downloads++
		}
		counted.ServeHTTP(w, r)
	})
	th.CheckUpdates(context.Background())
	th.CheckUpdates(context.Background())
	st, _ := th.Status("demo")
	if st.Version != "1.0.0" || !strings.Contains(st.Origin.Problem, "The automatic update failed: Release 1.1.0 refused") || downloads != 1 {
		t.Fatalf("after two checks: %s, %q, %d downloads", st.Version, st.Origin.Problem, downloads)
	}
}

// A fresh install over a plain directory, cut short between saving its
// origin and placing its link, is finished at the next start.
func TestARestartFinishesAFreshInstallInFlight(t *testing.T) {
	stub := newForgeStub(t)
	stub.publish("github.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
	th := newURLHost(t, stub)
	writePlugin(t, th.Dir(), "demo", "serve", nil)
	th.start()
	waitState(t, th, "demo", StateRunning)
	offer, err := th.Look(context.Background(), "https://github.com/acme/hello", ForgeGitHub)
	if err != nil || offer.Replaces != "the plugins folder" {
		t.Fatalf("Look() = %+v, %v", offer, err)
	}
	staged := th.takeOffer(offer.Token).staged
	th.Close()
	in, err := th.adopt("demo", staged)
	if err == nil {
		err = th.setOrigin("demo", &origin{Repository: offer.Repository.URL, Current: in, Trial: true})
	}
	if err != nil {
		t.Fatal(err)
	}
	again := newURLHost(t, stub)
	again.cfg.StateDir = th.stateDir
	again.start()
	waitVersion(t, again, "demo", "1.0.0", StateRunning)
	if at, _ := again.pointsAt("demo"); at != in.Dir {
		t.Fatalf("the entry points at %q, want %q", at, in.Dir)
	}
	if rec := again.origins()["demo"]; rec == nil || rec.Trial {
		t.Fatalf("the origin after the start is %+v", rec)
	}
}

// A lost, empty or broken state file deletes no release at the next start:
// each plugin runs the release its link names, without origin, and keeps the
// release before. A state file without the plugin keeps only the running
// release.
func TestALostStateFileDeletesNoRelease(t *testing.T) {
	for name, lose := range map[string]func(t *testing.T, path string){
		"corrupt": func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte(`{"origins": {"demo": `), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"empty": func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"deleted": func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		},
		"without demo": func(t *testing.T, path string) {
			var st hostState
			raw, err := os.ReadFile(path)
			if err == nil {
				err = json.Unmarshal(raw, &st)
			}
			if err != nil {
				t.Fatal(err)
			}
			delete(st.Origins, "demo")
			raw, _ = json.Marshal(st)
			if err := os.WriteFile(path, raw, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			stub := newForgeStub(t)
			stub.publish("github.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
			stub.publish("gitlab.com/acme/other", "1.0.0", map[string][]byte{anyArchive("other", "1.0.0"): pluginArchive(t, "other", "1.0.0", "serve", "", nil)}, nil)
			th := newURLHost(t, stub)
			th.start()
			installFrom(t, th, "https://github.com/acme/hello", ForgeGitHub, false)
			installFrom(t, th, "https://gitlab.com/acme/other", ForgeGitLab, false)
			stub.publish("github.com/acme/hello", "1.1.0", map[string][]byte{anyArchive("demo", "1.1.0"): pluginArchive(t, "demo", "1.1.0", "serve", "", nil)}, nil)
			if err := th.Update(context.Background(), "demo", ""); err != nil {
				t.Fatalf("Update() = %v", err)
			}
			waitVersion(t, th, "demo", "1.1.0", StateRunning)
			waitSettled(t, th, "demo")
			rec := th.origins()["demo"]
			th.Close()
			path := enabledPath(th.stateDir)
			lose(t, path)

			again := newURLHost(t, stub)
			again.cfg.StateDir = th.stateDir
			again.start()
			waitVersion(t, again, "demo", "1.1.0", StateRunning)
			waitVersion(t, again, "other", "1.0.0", StateRunning)
			if at, _ := again.pointsAt("demo"); at != rec.Current.Dir {
				t.Fatalf("demo points at %q, want %q", at, rec.Current.Dir)
			}
			if _, err := os.Stat(filepath.Join(again.versionsOf("demo"), rec.Current.Dir, ManifestFile)); err != nil {
				t.Fatalf("the release that runs is gone: %v", err)
			}
			_, err := os.Stat(filepath.Join(again.versionsOf("demo"), rec.Previous.Dir))
			if kept := name != "without demo"; kept != (err == nil) {
				t.Fatalf("the release before stands %v, want %v", err == nil, kept)
			}
			if st, _ := again.Status("demo"); st.Origin.Repository != "" {
				t.Fatalf("demo came from %q, its origin was not read", st.Origin.Repository)
			}
			if _, err := os.Stat(path + ".broken"); (err == nil) != (name == "corrupt") {
				t.Fatalf("the quarantined copy stands %v", err == nil)
			}
			if err := again.Remove("demo"); err != nil {
				t.Fatalf("Remove() = %v", err)
			}
			if _, err := os.Stat(again.versionsOf("demo")); !errors.Is(err, fs.ErrNotExist) {
				t.Fatalf("Remove left the versions: %v", err)
			}
		})
	}
}

// Look offers no update to a release that is not newer, and an offer the
// plugin caught up with since is refused and used up.
func TestALookOffersNoUpdateThatIsNotNewer(t *testing.T) {
	stub := newForgeStub(t)
	stub.publish("github.com/acme/hello", "1.0.0", map[string][]byte{anyArchive("demo", "1.0.0"): pluginArchive(t, "demo", "1.0.0", "serve", "", nil)}, nil)
	th := newURLHost(t, stub)
	th.start()
	installFrom(t, th, "https://github.com/acme/hello", ForgeGitHub, false)
	waitVersion(t, th, "demo", "1.0.0", StateRunning)
	if _, err := th.Look(context.Background(), "https://github.com/acme/hello", ForgeGitHub); !IsInstallError(err) || err.Error() != "Nothing to update: Demo 1.0.0 is installed, the newest release of github.com/acme/hello is 1.0.0." {
		t.Fatalf("Look() of the release installed = %v", err)
	}

	stub.publish("github.com/acme/hello", "1.1.0", map[string][]byte{anyArchive("demo", "1.1.0"): pluginArchive(t, "demo", "1.1.0", "serve", "", nil)}, nil)
	offer, err := th.Look(context.Background(), "https://github.com/acme/hello", ForgeGitHub)
	if err != nil || offer.Updates != "1.0.0" || offer.Version != "1.1.0" {
		t.Fatalf("Look() of a newer release = %+v, %v", offer, err)
	}
	if err := th.Update(context.Background(), "demo", "1.1.0"); err != nil {
		t.Fatalf("Update() = %v", err)
	}
	waitVersion(t, th, "demo", "1.1.0", StateRunning)
	var ask *ReplaceError
	if _, err := th.Adopt(offer.Token, false, ""); !IsInstallError(err) || errors.As(err, &ask) || !strings.Contains(err.Error(), "Nothing to update: Demo 1.1.0 is installed, the newest release of github.com/acme/hello is 1.1.0.") {
		t.Fatalf("Adopt() of an offer caught up with = %v", err)
	}
	if _, err := th.Adopt(offer.Token, false, ""); !errors.Is(err, ErrOfferGone) {
		t.Fatalf("a second Adopt() of the used offer = %v", err)
	}

	stub.mu.Lock()
	stub.releases["github.com/acme/hello"] = stub.releases["github.com/acme/hello"][:1]
	stub.mu.Unlock()
	if _, err := th.Look(context.Background(), "https://github.com/acme/hello", ForgeGitHub); !IsInstallError(err) || err.Error() != "Nothing to update: Demo 1.1.0 is installed, the newest release of github.com/acme/hello is 1.0.0." {
		t.Fatalf("Look() of an older release = %v", err)
	}
	if left, _ := filepath.Glob(filepath.Join(th.Dir(), ".fetch-*")); len(left) > 0 {
		t.Fatalf("a refused look left %v", left)
	}
	if st, _ := th.Status("demo"); st.Version != "1.1.0" || st.State != StateRunning {
		t.Fatalf("the plugin stands %s %s", st.Version, st.State)
	}
}
