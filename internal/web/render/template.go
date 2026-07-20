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
	// HasTabStrip marks the attach pages, which render the terminal tab strip
	// inline. Every other page gets a hidden switcher-only terminal-tabs
	// instance from the layout, so the double Ctrl/Meta switcher works app wide.
	HasTabStrip bool
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
	// Active is the flat list of live coders and shells, ordered exactly like the
	// attach page tab strip (same @dc_tab_pos sort), so the quick nav and the tab
	// strip agree and a drag in either persists through POST /terminal-tabs/order.
	Active []TerminalTab
	// Strip is Active folded like the tab strip: split view groups become one
	// entry with their members, so the quick nav renders groups as blocks.
	Strip []StripTab
	// UnreadCount is the number of targets with unread news, rendered into
	// the toggle badge server-side so the badge survives a boosted body swap
	// (the app-wide event stream sends its snapshot on connect, not per
	// navigation); the client keeps it live from there.
	UnreadCount int
	CurrentID   string
	// Focus is the split member whose pane is active on the current page, so
	// the group block can mark that member row and the project context can
	// follow it even when the group's members span several projects.
	Focus string
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

// HasInactiveCoders reports whether any project carries a resumable session.
// It switches on the resume section in the tab strip's plus menu.
func (q QuickNav) HasInactiveCoders() bool {
	for _, p := range q.AllProjects {
		if len(p.InactiveCoders) > 0 {
			return true
		}
	}
	return false
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
	TabPos    int    // strip position from @dc_tab_pos, 0 when unset
	Group     string // split view group id from @dc_tab_group, empty when ungrouped
	GroupPos  int    // position inside the group from @dc_tab_gpos, 0 when unset
	GroupName string // group display name from @dc_tab_gname, may be empty
}

// StripTab is one rendered entry of the tab strip: a single session, or a
// split view group folding several sessions into one tab. Group entries fill
// the embedded TerminalTab with the group's values (ID is the group id, URL
// the split page, Kind "split", HasNews the aggregate) and carry the member
// sessions in group order.
type StripTab struct {
	TerminalTab
	Members []TerminalTab
}

// MemberIDs returns the space separated session ids behind this strip entry,
// the members for a group, the session itself otherwise. The strip client
// posts these expanded ids when it persists the tab order.
func (t StripTab) MemberIDs() string {
	if len(t.Members) == 0 {
		return t.ID
	}
	ids := make([]string, len(t.Members))
	for i := range t.Members {
		ids[i] = t.Members[i].ID
	}
	return strings.Join(ids, " ")
}

// IsActive reports whether this strip entry represents the current page: the
// entry itself, or for a group one of its members, so a member's own page
// keeps its group tab highlighted.
func (t StripTab) IsActive(currentID string) bool {
	if currentID == "" {
		return false
	}
	if t.ID == currentID {
		return true
	}
	for i := range t.Members {
		if t.Members[i].ID == currentID {
			return true
		}
	}
	return false
}

// MemberKinds returns the space separated kinds matching MemberIDs, so the
// strip client knows each member's stop/delete endpoint.
func (t StripTab) MemberKinds() string {
	if len(t.Members) == 0 {
		return t.Kind
	}
	kinds := make([]string, len(t.Members))
	for i := range t.Members {
		kinds[i] = t.Members[i].Kind
	}
	return strings.Join(kinds, " ")
}
