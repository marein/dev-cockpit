package browserextension

import (
	"archive/zip"
	"bytes"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strconv"
	"strings"
	"time"
)

//go:embed addon
var addon embed.FS

var Browsers = []string{"chrome", "firefox", "safari"}

var ErrBrowser = errors.New("The extension is built for Chrome, Firefox and Safari.")

// Firefox keeps the extension's storage under this id, so it never changes.
const geckoID = "{155d77a3-029a-4c19-b7fa-561fde3383bb}"

// Fixed, so the same cockpit always hands out the same bytes.
var zipTime = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

// The extension's own version, apart from the cockpit's: a release that
// leaves the extension alone hands out the same zip, which the browser
// keeps without a reinstall.
var Version = sourceVersion()

// The versions of /api/browser-extension/ the cockpit answers, oldest
// first. The zips speak the last.
var APIVersions = []int{1}

func sourceVersion() string {
	var m struct {
		Version string `json:"version"`
	}
	raw, err := addon.ReadFile("addon/manifest.json")
	if err == nil {
		err = json.Unmarshal(raw, &m)
	}
	if err != nil || m.Version == "" {
		panic("the extension's manifest names no version")
	}
	return m.Version
}

func manifest(browser string) ([]byte, error) {
	raw, err := addon.ReadFile("addon/manifest.json")
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	permissions, _ := m["permissions"].([]any)
	switch browser {
	case "chrome":
		m["minimum_chrome_version"] = "116"
		m["background"] = map[string]any{"service_worker": "background.js", "type": "module"}
		m["side_panel"] = map[string]any{"default_path": "panel.html"}
		m["permissions"] = append(permissions, "sidePanel")
	case "firefox":
		m["background"] = map[string]any{"scripts": []string{"background.js"}, "type": "module"}
		m["sidebar_action"] = map[string]any{
			"default_panel":   "panel.html",
			"default_title":   "dev-cockpit",
			"default_icon":    map[string]any{"16": "icons/16.png", "32": "icons/32.png"},
			"open_at_install": false,
		}
		m["browser_specific_settings"] = map[string]any{"gecko": map[string]any{
			"id":                          geckoID,
			"strict_min_version":          "142.0",
			"data_collection_permissions": map[string]any{"required": []string{"websiteContent"}},
		}}
	case "safari":
		m["background"] = map[string]any{"service_worker": "background.js", "type": "module"}
		action, _ := m["action"].(map[string]any)
		action["default_popup"] = "panel.html"
		action["default_title"] = "Open the panel"
		commands, _ := m["commands"].(map[string]any)
		key, _ := commands["_execute_action"].(map[string]any)
		key["description"] = "Open the panel"
		m["browser_specific_settings"] = map[string]any{"safari": map[string]any{"strict_min_version": "26.0"}}
	default:
		return nil, ErrBrowser
	}
	return json.MarshalIndent(m, "", "  ")
}

// A page's console call passes through console.js in the page's own world.
// Chrome files a console message under the extension whose address made the
// call, so the scripts there go by a name that is no extension's address, and
// the page's messages stay out of the extension's errors. The source map puts
// console.js on DevTools' ignore list, so DevTools names the page's own line.
const pageWorldName = "dev-cockpit-browser-extension:///"

func inPageWorld(name string, script []byte) []byte {
	return fmt.Appendf(slices.Clip(bytes.TrimRight(script, "\n")), "\n//# sourceURL=%s%s\n", pageWorldName, name)
}

func ignoreListed(name string, script []byte) ([]byte, error) {
	lines := bytes.Count(script, []byte("\n")) + 1
	sourceMap, err := json.Marshal(map[string]any{
		"version":             3,
		"file":                name,
		"sources":             []string{name},
		"mappings":            "AAAA" + strings.Repeat(";AACA", lines-1),
		"ignoreList":          []int{0},
		"x_google_ignoreList": []int{0},
	})
	if err != nil {
		return nil, err
	}
	return fmt.Appendf(inPageWorld(name, script), "//# sourceMappingURL=data:application/json;base64,%s\n", base64.StdEncoding.EncodeToString(sourceMap)), nil
}

func Zip(w io.Writer, browser string, shared map[string][]byte) error {
	if !slices.Contains(Browsers, browser) {
		return ErrBrowser
	}
	files := map[string][]byte{}
	err := fs.WalkDir(addon, "addon", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := addon.ReadFile(path)
		files[strings.TrimPrefix(path, "addon/")] = data
		return err
	})
	if err != nil {
		return err
	}
	if files["manifest.json"], err = manifest(browser); err != nil {
		return err
	}
	files["version.js"] = []byte("export const API = " + strconv.Itoa(APIVersions[len(APIVersions)-1]) + ";\nexport const BROWSER = \"" + browser + "\";\n")
	for name, data := range shared {
		if _, taken := files[name]; taken {
			return fmt.Errorf("the extension has its own %s", name)
		}
		files[name] = data
	}
	if files["console.js"], err = ignoreListed("console.js", files["console.js"]); err != nil {
		return err
	}
	files["catch.js"] = inPageWorld("catch.js", files["catch.js"])
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	out := zip.NewWriter(w)
	for _, name := range names {
		entry, err := out.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: zipTime})
		if err != nil {
			return err
		}
		if _, err := entry.Write(files[name]); err != nil {
			return err
		}
	}
	return out.Close()
}
