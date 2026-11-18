package web

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/pluginhost"
)

// exampleRelease serves a GitHub shaped feed with one release that carries
// the example plugin as its any archive.
func exampleRelease(t *testing.T, version string) *httptest.Server {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	example := "../../examples/plugins/hello"
	err := filepath.WalkDir(example, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(example, path)
		body, _ := os.ReadFile(path)
		if rel == "plugin.json" {
			var m map[string]any
			_ = json.Unmarshal(body, &m)
			m["version"] = version
			body, _ = json.Marshal(m)
		}
		info, _ := d.Info()
		_ = tw.WriteHeader(&tar.Header{Name: "hello/" + filepath.ToSlash(rel), Mode: int64(info.Mode().Perm() | 0o755), Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, err = tw.Write(body)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = tw.Close()
	_ = gz.Close()
	name := "hello_" + version + "_any.tar.gz"
	sum := sha256.Sum256(archive.Bytes())
	sums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/acme/hello/releases":
			_ = json.NewEncoder(w).Encode([]map[string]any{{"tag_name": "v" + version, "body": "Notes.", "assets": []map[string]string{
				{"name": name, "browser_download_url": server.URL + "/a"},
				{"name": "hello_" + version + "_checksums.txt", "browser_download_url": server.URL + "/s"},
			}}})
		case "/a":
			_, _ = w.Write(archive.Bytes())
		case "/s":
			_, _ = w.Write([]byte(sums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func postPlugins(r http.Handler, path string, form url.Values) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.ServeHTTP(closeNotifier{rec}, req)
	return rec
}

// A refused address answers JSON, a found one the offer with what it
// replaces. An install not told of the replace asks again, one that was lands
// on the row naming the repository. A refused update shows at its row, the
// local socket installs nothing.
func TestTheDialogInstallsFromARepository(t *testing.T) {
	stub := exampleRelease(t, "2.0.0")
	r, _, host, _ := externalShell(t, func(cfg *pluginhost.HostConfig) { cfg.PluginAPI = stub.URL })
	dialog := shellDo(t, r, http.MethodGet, settingsPluginNewPath+"?modal=1", "").Body.String()
	if !strings.Contains(dialog, `data-form-title="Install from a repository"`) || strings.Contains(dialog, "dc-app") {
		t.Fatalf("the dialog's form reads %.300s", dialog)
	}
	if strings.Count(dialog, `name="forge"`) != 2 || strings.Contains(between(t, dialog, "data-plugin-forge-pick>", "</div>"), "checked") {
		t.Fatal("the dialog does not offer the forge or picks one itself")
	}
	rec := postPlugins(r, settingsPluginNewPath+"?modal=1", url.Values{"form": {"look"}, "forge": {"github"}, "url": {"http://github.com/acme/hello"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "The address must use https.") {
		t.Fatalf("a plain http address answered %d %s", rec.Code, rec.Body)
	}
	page := postPlugins(r, settingsPluginNewPath, url.Values{"form": {"look"}, "forge": {"github"}, "url": {"ftp://x"}})
	if page.Code != http.StatusUnprocessableEntity || !strings.Contains(page.Body.String(), `value="ftp://x"`) || !strings.Contains(page.Body.String(), "data-plugin-url-error") || !strings.Contains(page.Body.String(), `value="github" autocomplete="off" required checked`) {
		t.Fatalf("the page's refusal answered %d", page.Code)
	}
	rec = postPlugins(r, settingsPluginNewPath+"?modal=1", url.Values{"form": {"look"}, "forge": {"github"}, "url": {"github.com/acme/hello"}})
	offer := rec.Body.String()
	for _, want := range []string{`data-plugin-offer="hello"`, "data-plugin-offer-version>2.0.0<", "https://github.com/acme/hello", "</a> on GitHub", `name="forge" value="github"`, "hello_2.0.0_any.tar.gz", "installed from the plugins folder", `data-form-title="Replace hello?"`} {
		if !strings.Contains(offer, want) {
			t.Fatalf("the offer misses %q: %.600s", want, offer)
		}
	}
	token := regexp.MustCompile(`name="token" value="([0-9a-f]+)"`).FindStringSubmatch(offer)[1]
	rec = postPlugins(r, settingsPluginNewPath+"?modal=1", url.Values{"form": {"install"}, "token": {token}, "auto": {"1"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "data-plugin-offer-ask") {
		t.Fatalf("an install not told of the replace answered %d", rec.Code)
	}
	rec = postPlugins(r, settingsPluginNewPath+"?modal=1", url.Values{"form": {"install"}, "token": {token}, "auto": {"1"}, "replaces": {"the plugins folder"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"location":"/settings/plugins#plugin-hello"`) {
		t.Fatalf("the install answered %d %s", rec.Code, rec.Body)
	}
	waitPlugin(t, host, "hello", pluginhost.StateRunning)
	list := shellDo(t, r, http.MethodGet, settingsPluginsPath, "").Body.String()
	if !strings.Contains(list, `data-plugin-repository>github.com/acme/hello<`) || !strings.Contains(list, "data-plugin-forge>GitHub<") || !strings.Contains(list, `data-plugin-auto="on"`) || !strings.Contains(list, "data-plugin-check") {
		t.Fatal("the row does not name the repository, the automatic updates or the check")
	}
	rec = postPlugins(r, settingsPluginsPath, url.Values{"form": {"update"}, "id": {"hello"}, "version": {"9.9.9"}})
	if rec.Code != http.StatusConflict || !strings.Contains(between(t, rec.Body.String(), "data-plugin-refusal>", "</div>"), "2.0.0 is the newest release.") {
		t.Fatalf("a refused update answered %d", rec.Code)
	}
	local := httptest.NewRequest(http.MethodPost, settingsPluginNewPath, strings.NewReader("form=look&forge=github&url=github.com%2Facme%2Fhello"))
	local.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if rec := shellRecord(r, local.WithContext(context.WithValue(local.Context(), localCallKey, true))); rec.Code != http.StatusForbidden {
		t.Fatalf("the local socket's look answered %d", rec.Code)
	}
}

// A path install over a plugin of another source asks with a form carrying
// path and yes, and replaces only once that is sent.
func TestAPathInstallAsksBeforeAReplace(t *testing.T) {
	r, _, host, _ := externalShell(t)
	example, err := filepath.Abs("../../examples/plugins/hello")
	if err != nil {
		t.Fatal(err)
	}
	rec := postPlugins(r, settingsPluginsPath, url.Values{"form": {"install"}, "path": {example}})
	ask := between(t, rec.Body.String(), "data-plugin-replace-ask>", "</form>")
	if rec.Code != http.StatusConflict || !strings.Contains(ask, "installed from the plugins folder") || !strings.Contains(ask, `name="replace" value="1"`) || !strings.Contains(ask, `name="path" value="`+example+`"`) {
		t.Fatalf("the install answered %d with %.400s", rec.Code, ask)
	}
	if st, _ := host.Status("hello"); st.Origin.Path != "" {
		t.Fatal("the plugin was replaced before the yes")
	}
	rec = postPlugins(r, settingsPluginsPath, url.Values{"form": {"install"}, "path": {example}, "replace": {"1"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("the replace answered %d", rec.Code)
	}
	waitPlugin(t, host, "hello", pluginhost.StateRunning)
	if st, _ := host.Status("hello"); st.Origin.Path != example {
		t.Fatalf("the replaced plugin came from %q", st.Origin.Path)
	}
}

// A look whose newest release is already installed offers nothing, in the
// dialog and on the page. An install of a spent offer answers the address
// form again with the reason.
func TestTheDialogOffersNoUpdateThatIsNotNewer(t *testing.T) {
	stub := exampleRelease(t, "2.0.0")
	r, _, host, _ := externalShell(t, func(cfg *pluginhost.HostConfig) { cfg.PluginAPI = stub.URL })
	look := func(path string) *httptest.ResponseRecorder {
		return postPlugins(r, path, url.Values{"form": {"look"}, "forge": {"github"}, "url": {"github.com/acme/hello"}})
	}
	token := regexp.MustCompile(`name="token" value="([0-9a-f]+)"`).FindStringSubmatch(look(settingsPluginNewPath + "?modal=1").Body.String())[1]
	install := url.Values{"form": {"install"}, "forge": {"github"}, "url": {"github.com/acme/hello"}, "token": {token}, "replaces": {"the plugins folder"}}
	if rec := postPlugins(r, settingsPluginNewPath+"?modal=1", install); rec.Code != http.StatusOK {
		t.Fatalf("the install answered %d %s", rec.Code, rec.Body)
	}
	waitPlugin(t, host, "hello", pluginhost.StateRunning)

	refused := "Nothing to update: Hello 2.0.0 is installed, the newest release of github.com/acme/hello is 2.0.0."
	rec := look(settingsPluginNewPath + "?modal=1")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), refused) || strings.Contains(rec.Body.String(), "data-plugin-offer") {
		t.Fatalf("the dialog's look answered %d %s", rec.Code, rec.Body)
	}
	page := look(settingsPluginNewPath)
	if page.Code != http.StatusUnprocessableEntity || !strings.Contains(between(t, page.Body.String(), "data-plugin-url-error>", "</div>"), refused) || strings.Contains(page.Body.String(), "data-plugin-offer") {
		t.Fatalf("the page's look answered %d", page.Code)
	}

	rec = postPlugins(r, settingsPluginNewPath+"?modal=1", install)
	body := rec.Body.String()
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, `data-form-title="Install from a repository"`) || !strings.Contains(body, `value="github.com/acme/hello"`) || strings.Contains(body, "data-plugin-offer") || strings.Contains(body, "dc-app") {
		t.Fatalf("an install of a used offer answered %d %.600s", rec.Code, body)
	}
	if got := between(t, body, "data-form-modal-error>", "</div>"); got != "The download is gone, look up the repository again." || strings.Contains(body, "data-plugin-url-error") {
		t.Fatalf("the dialog says %q", got)
	}
}
