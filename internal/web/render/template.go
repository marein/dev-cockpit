// Package render contains HTML template models and parsing.
package render

import (
	"embed"
	"html/template"
	"path/filepath"
	"strings"

	"github.com/local/dev-cockpit/internal/filesystem"
)

//go:embed templates/*.gohtml
var templatesFS embed.FS

// HTMLTemplate returns the parsed template set used by Gin's HTML renderer.
func HTMLTemplate(assetPath func(string) string) *template.Template {
	funcMap := template.FuncMap{
		"asset":   assetPath,
		"hasURL":  func(s string) bool { return s != "" },
		"fmtSize": filesystem.HumanSize,
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
	CSRFToken string
	User      string
}
