package browserextension

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

var testShared = map[string][]byte{"pick.js": []byte("function dcPicker() {}\n"), "console.js": []byte("globalThis.dcConsole = function () {};\n"), "draw.js": []byte("function dcDrawing() {}\n"), "browser-context.js": []byte("export const MAX_ITEMS = 8;\n"), "filter.js": []byte("export function rankRows() {}\n")}

func unzip(t *testing.T, browser string) map[string][]byte {
	t.Helper()
	var out bytes.Buffer
	if err := Zip(&out, browser, testShared); err != nil {
		t.Fatal(err)
	}
	r, err := zip.NewReader(bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range r.File {
		rc, err := f.Open()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		files[f.Name] = data
	}
	return files
}

func manifestOf(t *testing.T, files map[string][]byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(files["manifest.json"], &m); err != nil {
		t.Fatalf("the manifest is no JSON: %v", err)
	}
	return m
}

func texts(v any) []string {
	list, _ := v.([]any)
	out := []string{}
	for _, one := range list {
		if s, ok := one.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// No page reaches the extension: no content script, no file open to pages and
// no page may message it.
func TestTheZipWritesEachBrowserItsManifest(t *testing.T) {
	for _, browser := range Browsers {
		files := unzip(t, browser)
		m := manifestOf(t, files)
		if m["manifest_version"] != float64(3) || m["version"] != Version || m["version_name"] != nil {
			t.Fatalf("%s: manifest_version %v, version %v", browser, m["manifest_version"], m["version"])
		}
		want := []string{"activeTab", "scripting", "storage", "webRequest"}
		if browser == "chrome" {
			want = append(want, "sidePanel")
		}
		if got := texts(m["permissions"]); !slices.Equal(got, want) {
			t.Fatalf("%s: permissions %v", browser, got)
		}
		if _, ok := m["host_permissions"]; ok {
			t.Fatalf("%s: the manifest asks for host permissions at install", browser)
		}
		if got := texts(m["optional_host_permissions"]); !slices.Equal(got, []string{"<all_urls>"}) {
			t.Fatalf("%s: optional host permissions %v", browser, got)
		}
		for _, key := range []string{"content_scripts", "web_accessible_resources", "externally_connectable", "content_security_policy"} {
			if _, ok := m[key]; ok {
				t.Fatalf("%s: the manifest carries %s", browser, key)
			}
		}
		background, _ := m["background"].(map[string]any)
		action, _ := m["action"].(map[string]any)
		if browser != "safari" && action["default_popup"] != nil {
			t.Fatalf("%s: the button opens a popup", browser)
		}
		if title := map[bool]string{true: "Open the panel", false: "Pick elements"}[browser == "safari"]; action["default_title"] != title {
			t.Fatalf("%s: the button's title is %v", browser, action["default_title"])
		}
		switch browser {
		case "chrome":
			if background["service_worker"] != "background.js" || background["scripts"] != nil || m["side_panel"] == nil || m["sidebar_action"] != nil || m["browser_specific_settings"] != nil {
				t.Fatalf("chrome: %s", files["manifest.json"])
			}
		case "firefox":
			gecko, _ := m["browser_specific_settings"].(map[string]any)["gecko"].(map[string]any)
			if !slices.Equal(texts(background["scripts"]), []string{"background.js"}) || background["service_worker"] != nil || m["sidebar_action"] == nil || m["side_panel"] != nil || gecko["id"] != geckoID {
				t.Fatalf("firefox: %s", files["manifest.json"])
			}
		case "safari":
			safari, _ := m["browser_specific_settings"].(map[string]any)["safari"].(map[string]any)
			if background["service_worker"] != "background.js" || background["type"] != "module" || action["default_popup"] != "panel.html" || m["side_panel"] != nil || m["sidebar_action"] != nil || safari["strict_min_version"] != "26.0" {
				t.Fatalf("safari: %s", files["manifest.json"])
			}
		}
		if string(files["version.js"]) != "export const API = "+strconv.Itoa(APIVersions[len(APIVersions)-1])+";\nexport const BROWSER = \""+browser+"\";\n" {
			t.Fatalf("%s: version.js is %q", browser, files["version.js"])
		}
		for name, data := range testShared {
			got := files[name]
			if name == "console.js" {
				got, _, _ = bytes.Cut(got, []byte("//# sourceURL="))
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("%s: %s is not the shared file", browser, name)
			}
		}
	}
}

var importFrom = regexp.MustCompile(`(?m)^\s*import\s[^;]*?from\s+"\./([^"]+)"`)
var pageFile = regexp.MustCompile(`(?:src|href)="([^":]+)"`)
var scriptFile = regexp.MustCompile(`"([a-z][a-z-]*\.js)"`)

// What loads in the browser is what the cockpit hands out.
func TestEveryNamedFileIsInTheZip(t *testing.T) {
	files := unzip(t, "chrome")
	named := []string{"background.js", "panel.html", "options.html"}
	for _, icon := range []string{"16", "32", "48", "128"} {
		named = append(named, "icons/"+icon+".png")
	}
	for name, data := range files {
		switch {
		case strings.HasSuffix(name, ".js"):
			for _, m := range importFrom.FindAllSubmatch(data, -1) {
				named = append(named, string(m[1]))
			}
			for _, m := range scriptFile.FindAllSubmatch(data, -1) {
				named = append(named, string(m[1]))
			}
		case strings.HasSuffix(name, ".html"):
			for _, m := range pageFile.FindAllSubmatch(data, -1) {
				named = append(named, string(m[1]))
			}
		}
	}
	for _, name := range named {
		if _, ok := files[name]; !ok {
			t.Errorf("%s is named but not in the zip", name)
		}
	}
}

// Every cockpit with this version of the extension hands out the same bytes.
func TestTheZipIsTheSameEveryTime(t *testing.T) {
	var a, b bytes.Buffer
	if err := Zip(&a, "firefox", testShared); err != nil {
		t.Fatal(err)
	}
	if err := Zip(&b, "firefox", testShared); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("two zips of the same version differ")
	}
}

func TestTheZipRefusesOtherBrowsersAndATakenName(t *testing.T) {
	if err := Zip(io.Discard, "edge", testShared); !errors.Is(err, ErrBrowser) {
		t.Fatalf("edge: %v", err)
	}
	if err := Zip(io.Discard, "chrome", map[string][]byte{"panel.js": nil}); err == nil {
		t.Fatal("a shared file took the place of the extension's own")
	}
}

var dottedVersion = regexp.MustCompile(`^(0|[1-9]\d{0,4})(\.(0|[1-9]\d{0,4})){0,3}$`)

// Chrome, Firefox and Safari take up to four numbers of at most 65535.
func TestTheVersionIsDottedNumbers(t *testing.T) {
	if !dottedVersion.MatchString(Version) {
		t.Fatalf("the version %q is no dotted numbers", Version)
	}
	for part := range strings.SplitSeq(Version, ".") {
		if n, _ := strconv.Atoi(part); n > 65535 {
			t.Fatalf("the version %q has a number above 65535", Version)
		}
	}
	if len(APIVersions) == 0 {
		t.Fatal("the cockpit speaks no API version")
	}
	for i, v := range APIVersions {
		if v < 1 || i > 0 && v <= APIVersions[i-1] {
			t.Fatalf("the API versions %v do not rise from 1 on", APIVersions)
		}
	}
}

func TestConsoleJSTellsDevToolsToSkipIt(t *testing.T) {
	files := unzip(t, "chrome")
	script, comment, found := bytes.Cut(files["console.js"], []byte("\n//# sourceURL=dev-cockpit-browser-extension:///console.js\n//# sourceMappingURL=data:application/json;base64,"))
	if !found || !bytes.Equal(script, bytes.TrimRight(testShared["console.js"], "\n")) || !bytes.HasSuffix(comment, []byte("\n")) {
		t.Fatalf("console.js in the zip is %q", files["console.js"])
	}
	if own, _ := addon.ReadFile("addon/catch.js"); !bytes.Equal(files["catch.js"], append(bytes.TrimRight(own, "\n"), "\n//# sourceURL=dev-cockpit-browser-extension:///catch.js\n"...)) {
		t.Fatalf("catch.js in the zip ends %q", files["catch.js"][max(0, len(files["catch.js"])-80):])
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(comment)))
	if err != nil {
		t.Fatal(err)
	}
	var sourceMap struct {
		Version    int      `json:"version"`
		Sources    []string `json:"sources"`
		Mappings   string   `json:"mappings"`
		IgnoreList []int    `json:"ignoreList"`
		Google     []int    `json:"x_google_ignoreList"`
	}
	if err := json.Unmarshal(raw, &sourceMap); err != nil {
		t.Fatal(err)
	}
	if sourceMap.Version != 3 || len(sourceMap.Sources) != 1 || sourceMap.Sources[0] != "console.js" || sourceMap.Mappings != "AAAA;AACA" || len(sourceMap.IgnoreList) != 1 || sourceMap.IgnoreList[0] != 0 || len(sourceMap.Google) != 1 || sourceMap.Google[0] != 0 {
		t.Fatalf("the source map is %s", raw)
	}
	if !bytes.Equal(testShared["console.js"], []byte("globalThis.dcConsole = function () {};\n")) {
		t.Fatal("the zip changed the shared console.js")
	}
}
