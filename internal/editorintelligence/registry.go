// Package editorintelligence provides code intelligence for the project
// editor: language server completion over stdio JSON-RPC and optional AI
// inline completion from a local provider. All providers run server side,
// the browser only exchanges bounded document snapshots and results.
package editorintelligence

import (
	"os/exec"
	"path"
	"sort"
	"strings"
)

// Profile is one fixed language server profile compiled into the binary.
// Settings can enable or disable a profile, but never change its command,
// so a setting can not become a command execution surface.
type Profile struct {
	// ID is the stable profile identifier used in settings and URLs.
	ID string
	// Label names the profile on the settings page.
	Label string
	// Command is the argv started for this profile, resolved via PATH.
	Command []string
	// languageIDs maps the owned file extensions (lowercase, without dot)
	// to the LSP language identifier sent in didOpen.
	languageIDs map[string]string
}

// profiles is the fixed registry, ordered for stable rendering.
var profiles = []*Profile{
	{
		ID:          "go",
		Label:       "Go (gopls)",
		Command:     []string{"gopls"},
		languageIDs: map[string]string{"go": "go"},
	},
	{
		ID:          "php",
		Label:       "PHP (Intelephense)",
		Command:     []string{"intelephense", "--stdio"},
		languageIDs: map[string]string{"php": "php"},
	},
	{
		ID:          "python",
		Label:       "Python (Pyright)",
		Command:     []string{"pyright-langserver", "--stdio"},
		languageIDs: map[string]string{"py": "python"},
	},
	{
		ID:          "html",
		Label:       "HTML (VS Code HTML server)",
		Command:     []string{"vscode-html-language-server", "--stdio"},
		languageIDs: map[string]string{"html": "html", "htm": "html"},
	},
	{
		ID:          "css",
		Label:       "CSS and SCSS (VS Code CSS server)",
		Command:     []string{"vscode-css-language-server", "--stdio"},
		languageIDs: map[string]string{"css": "css", "scss": "scss"},
	},
}

// Profiles returns the fixed profile registry in rendering order.
func Profiles() []*Profile {
	return profiles
}

// OwnedExtensions returns every file extension a profile owns, sorted. The
// editor page hands the list to the client, so the browser never mirrors
// the registry.
func OwnedExtensions() []string {
	var exts []string
	for _, p := range profiles {
		for ext := range p.languageIDs {
			exts = append(exts, ext)
		}
	}
	sort.Strings(exts)
	return exts
}

// ProfileForPath returns the profile owning the file's extension and the
// LSP language id for it.
func ProfileForPath(rel string) (*Profile, string, bool) {
	ext := strings.ToLower(strings.TrimPrefix(path.Ext(rel), "."))
	if ext == "" {
		return nil, "", false
	}
	for _, p := range profiles {
		if id, ok := p.languageIDs[ext]; ok {
			return p, id, true
		}
	}
	return nil, "", false
}

// Detection is the result of looking up a profile's executable.
type Detection struct {
	Found bool
	// Path is the resolved executable path when found.
	Path string
}

// Detect looks the profile's executable up in PATH. It never starts a
// process.
func (p *Profile) Detect() Detection {
	resolved, err := exec.LookPath(p.Command[0])
	if err != nil {
		return Detection{}
	}
	return Detection{Found: true, Path: resolved}
}
