// Package render contains HTML template models and parsing.
package render

import (
	"embed"
	"html/template"
	"path/filepath"
	"strings"
)

//go:embed templates/*.gohtml
var templatesFS embed.FS

// HTMLTemplate returns the parsed template set used by Gin's HTML renderer.
func HTMLTemplate(assetPath func(string) string, version string) *template.Template {
	funcMap := template.FuncMap{
		"asset":      assetPath,
		"appVersion": func() string { return version },
		"hasURL":     func(s string) bool { return s != "" },
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
	Switcher     Switcher
}

// Switcher feeds the quick-switch floating button: the live sessions and shells
// you can jump to, plus the identifier of the one you are currently attached to.
type Switcher struct {
	Sessions  []SwitchTarget
	Shells    []SwitchTarget
	CurrentID string
	// CurrentProject is the project of the page you're on (terminal/editor), used
	// to preselect it in the new-session / new-shell forms. Empty when there is
	// no project context.
	CurrentProject string
	// CurrentPath is the path of the page being rendered, passed to the create
	// forms as their Cancel return target.
	CurrentPath string
}

// SwitchTarget is one jump destination in the quick-switch menu.
type SwitchTarget struct {
	ID      string
	Name    string
	URL     string
	Project string // owning project name, shown under the target
}
