// Package render contains HTML template models and parsing.
package render

import (
	"embed"
	"html/template"
	"path/filepath"
	"strings"
	"time"
)

//go:embed templates/*.gohtml
var templatesFS embed.FS

// HTMLTemplate returns the parsed template set used by Gin's HTML renderer.
func HTMLTemplate(assetPath func(string) string, version, assetBuild string) *template.Template {
	funcMap := template.FuncMap{
		"asset":      assetPath,
		"assetBuild": func() string { return assetBuild },
		"appVersion": func() string { return version },
		"coderLabel": func(id string) string {
			if id == "" {
				return ""
			}
			return strings.ToUpper(id[:1]) + id[1:]
		},
		"projectName": func(path string) string {
			p := strings.TrimSpace(path)
			if p == "" {
				return ""
			}
			return filepath.Base(filepath.Clean(p))
		},
	}
	return template.Must(template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.gohtml"))
}

// Flash carries a one-shot notice from a previous request.
type Flash struct {
	Message string
	Level   string // "success" | "error"
}

// Page carries request-scoped metadata shared by all templates.
type Page struct {
	Title     string
	ActiveTab string
	Flash     Flash
	// FlashProject, when set, anchors the flash to a project card on the projects
	// page (rendered there instead of at the top of the page).
	FlashProject string
	CSRFToken    string
	User         string
	// MultiCoder is true when more than one coder is active, switching on the
	// coder badges and selectors across the UI.
	MultiCoder bool
	// CoderHome is the canonical landing URL of the coder pages (the first
	// active coder's instructions), used by the main nav.
	CoderHome string
	QuickNav  QuickNav
	// Jingle is the cross-device notification jingle selection, rendered into
	// a meta tag so the client picks the right tune.
	Jingle string
}

// CoderNav feeds the coder pages layout (instructions, agents, skills): the
// horizontal coder switcher (rendered only when more than one coder is
// active) and the section tabs in the card header.
type CoderNav struct {
	Coders   []string // active coder ids
	Selected string   // coder the page is scoped to
	Active   string   // active section: "instructions" | "agents" | "skills"
	Multi    bool     // true when more than one coder is active
}

// QuickNav feeds the quick nav floating button: the live sessions and shells you
// can jump to, plus the identifier of the one you are currently attached to.
type QuickNav struct {
	Coders    []QuickNavTarget
	Shells    []QuickNavTarget
	CurrentID string
	// CurrentProject is the project of the page you're on (terminal/editor), used
	// to preselect it in the new-session / new-shell forms. Empty when there is
	// no project context.
	CurrentProject string
	// CurrentProjectPath is that project's working directory, for the direct
	// "new shell in current project" form (which posts a path, not a name).
	CurrentProjectPath string
	// CurrentPath is the path of the page being rendered, passed to the create
	// forms as their Cancel return target.
	CurrentPath string
	// AllProjects feeds the two-level project browser: every project (alpha
	// sorted, like the projects page) with its editor, sessions and shells.
	AllProjects []ProjectNav
}

// QuickNavTarget is one jump destination in the quick nav menu.
type QuickNavTarget struct {
	ID      string
	Name    string
	URL     string
	Project string // owning project name, shown under the target
	Coder   string // owning coder id, shown when several coders run
	HasNews bool
}

// TerminalTab is one entry in the attach page tab strip: a live coder or shell.
type TerminalTab struct {
	ID        string
	Name      string
	URL       string
	Project   string // owning project name, shown under the tab name
	Coder     string // owning coder id, empty for shells
	Kind      string // "coder" or "shell"
	HasNews   bool
	StartedAt time.Time
}
