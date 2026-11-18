package web

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/browserextension"
)

// Every extension ever handed out reads this answer, so the literal path and
// field names stand here and never follow a rename in the code.
func TestTheBrowserExtensionHandshakeKeepsItsShape(t *testing.T) {
	f := newBrowserExtensionFixture(t)
	token := f.makeToken(t, "Laptop")
	if rec := api(f, http.MethodGet, "/api/browser-extension/version", "", nil, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the handshake answered %d without the token", rec.Code)
	}
	rec := api(f, http.MethodGet, "/api/browser-extension/version", token, nil, "")
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("the handshake answered %d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatalf("the handshake is no JSON object: %s", rec.Body.String())
	}
	var offered string
	if err := json.Unmarshal(fields["extension"], &offered); err != nil || offered != browserextension.Version || !regexp.MustCompile(`^\d+(\.\d+){0,3}$`).MatchString(offered) {
		t.Fatalf("extension is %s", fields["extension"])
	}
	var speaks []int
	if err := json.Unmarshal(fields["api"], &speaks); err != nil || !slices.Equal(speaks, browserextension.APIVersions) {
		t.Fatalf("api is %s", fields["api"])
	}
	files := browserExtensionZipFiles(t, "chrome", nil)
	cockpit := string(files["cockpit.js"])
	for _, read := range []string{`"/api/browser-extension/version"`, "answer?.api", "answer?.extension", "api.runtime.getManifest().version"} {
		if !strings.Contains(cockpit, read) {
			t.Fatalf("the extension no longer reads %s", read)
		}
	}
	if want := "export const API = " + strconv.Itoa(speaks[len(speaks)-1]) + ";"; !strings.Contains(string(files["version.js"]), want) {
		t.Fatalf("the zip does not speak the cockpit's newest API: %s", files["version.js"])
	}
}

// One line per version of the extension, oldest first, with the fingerprint
// of every file its three zips ship. A browser keeps an extension by its
// version, so a change to those files raises "version" in
// internal/browserextension/addon/manifest.json and adds its line here.
var browserExtensionFingerprints = []browserExtensionFingerprint{
	{"1.0.0", "4398d9cc2f97e3458121f16aff5d167923f0a025af65dc6a27ab7882ef0f179c"},
}

type browserExtensionFingerprint struct {
	version, files string
}

func TestTheBrowserExtensionVersionRisesWithItsFiles(t *testing.T) {
	shipped := browserExtensionFilesFingerprint(t, nil)
	if err := checkBrowserExtensionFingerprints(browserExtensionFingerprints, browserextension.Version, shipped); err != nil {
		t.Fatal(err)
	}
	changed := browserExtensionFilesFingerprint(t, func(shared map[string][]byte) { shared["pick.js"] = append(slices.Clone(shared["pick.js"]), '\n') })
	if changed == shipped || checkBrowserExtensionFingerprints(browserExtensionFingerprints, browserextension.Version, changed) == nil {
		t.Fatal("a changed shipped file passes without a raise of the version")
	}
}

func TestTheBrowserExtensionFingerprintCheck(t *testing.T) {
	record := []browserExtensionFingerprint{{"1.0.0", "a"}, {"1.0.1", "b"}}
	for _, c := range []struct {
		name, version, files string
		record               []browserExtensionFingerprint
		fails                bool
	}{
		{"the last version with its files", "1.0.1", "b", record, false},
		{"a file changed without a raise", "1.0.1", "c", record, true},
		{"a raise without its line", "1.1.0", "c", record, true},
		{"a version handed out before", "1.0.0", "a", record, true},
		{"a version below the last", "0.9.0", "c", record, true},
		{"a record that does not rise", "1.0.1", "b", []browserExtensionFingerprint{{"1.0.1", "a"}, {"1.0.1", "b"}}, true},
	} {
		if err := checkBrowserExtensionFingerprints(c.record, c.version, c.files); (err != nil) != c.fails {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func checkBrowserExtensionFingerprints(record []browserExtensionFingerprint, version, files string) error {
	if len(record) == 0 {
		return fmt.Errorf("record version %s with %q in browserExtensionFingerprints", version, files)
	}
	for i := 1; i < len(record); i++ {
		if !olderBrowserExtensionVersion(record[i-1].version, record[i].version) {
			return fmt.Errorf("the versions in browserExtensionFingerprints do not rise at %s", record[i].version)
		}
	}
	last := record[len(record)-1]
	switch {
	case version == last.version && files == last.files:
		return nil
	case version == last.version:
		return fmt.Errorf("the files of the browser extension changed under version %s. Raise \"version\" in internal/browserextension/addon/manifest.json, the test then names the line to add", version)
	case olderBrowserExtensionVersion(last.version, version):
		return fmt.Errorf("add {%q, %q} to browserExtensionFingerprints", version, files)
	default:
		return fmt.Errorf("the browser extension's version %s is not above %s, the last one handed out", version, last.version)
	}
}

func olderBrowserExtensionVersion(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := range max(len(as), len(bs)) {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

func browserExtensionZipFiles(t *testing.T, browserName string, change func(map[string][]byte)) map[string][]byte {
	t.Helper()
	shared, err := browserExtensionShared()
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(shared)
	}
	var out bytes.Buffer
	if err := browserextension.Zip(&out, browserName, shared); err != nil {
		t.Fatal(err)
	}
	return unzipped(t, out.Bytes())
}

func unzipped(t *testing.T, body []byte) map[string][]byte {
	t.Helper()
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, one := range r.File {
		rc, err := one.Open()
		if err != nil {
			t.Fatal(err)
		}
		files[one.Name], err = io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	return files
}

// Over what the zips hold, not their bytes, which a new compressor may write
// otherwise.
func browserExtensionFilesFingerprint(t *testing.T, change func(map[string][]byte)) string {
	t.Helper()
	sum := sha256.New()
	for _, browserName := range browserextension.Browsers {
		files := browserExtensionZipFiles(t, browserName, change)
		names := make([]string, 0, len(files))
		for name := range files {
			names = append(names, name)
		}
		slices.Sort(names)
		for _, name := range names {
			fmt.Fprintf(sum, "%s\x00%s\x00%d\x00", browserName, name, len(files[name]))
			sum.Write(files[name])
		}
	}
	return hex.EncodeToString(sum.Sum(nil))
}
