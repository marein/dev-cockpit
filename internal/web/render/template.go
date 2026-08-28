// Package render contains HTML template models and parsing.
package render

import (
	"embed"
	"html/template"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/hostinfo"
	"github.com/marein/dev-cockpit/internal/pluginhost"
)

//go:embed templates/*.gohtml
var templatesFS embed.FS

// HTMLTemplate returns the parsed template set used by Gin's HTML renderer.
// plugins feeds the two plugin funcs: pluginElements binds every final
// element name to its plugin's starter module in the import map, which is
// how the lazy element loader finds plugin code; pluginSlot is the markup
// the plugins added for a named slot.
func HTMLTemplate(assetPath func(string) string, version, assetBuild string, plugins []*pluginhost.Serve) *template.Template {
	funcMap := template.FuncMap{
		"asset":              assetPath,
		"assetBuild":         func() string { return assetBuild },
		"appVersion":         func() string { return version },
		"pluginElements":     func() []pluginhost.Element { return pluginhost.Elements(plugins, assetPath) },
		"pluginSlot":         func(slot string) template.HTML { return pluginhost.SlotHTML(plugins, slot) },
		"coderLabel":         CoderLabel,
		"deleteWorktreeNote": DeleteWorktreeNote,
		"originIcon":         OriginIcon,
		"worktreeChoice":     WorktreeChoice,
		"projectName": func(path string) string {
			p := strings.TrimSpace(path)
			if p == "" {
				return ""
			}
			return filepath.Base(filepath.Clean(p))
		},
		"hostBarClass":   HostBarClass,
		"hostRingClass":  HostRingClass,
		"hostLevelClass": HostLevelClass,
		"hostBarStyle":   HostBarStyle,
		"hostBarHeight":  HostBarHeight,
		"hostBar":        hostinfo.Bar,
		"dict": func(pairs ...any) map[string]any {
			m := make(map[string]any, len(pairs)/2)
			for i := 0; i+1 < len(pairs); i += 2 {
				key, _ := pairs[i].(string)
				m[key] = pairs[i+1]
			}
			return m
		},
	}
	return template.Must(template.New("").Funcs(funcMap).ParseFS(templatesFS, "templates/*.gohtml"))
}

// CoderLabel names a coder id for display. One implementation for the
// templates and the handlers, so a label never differs by surface. A brand
// casing the plain capitalization cannot produce is special cased.
func CoderLabel(id string) string {
	if id == "opencode" {
		return "OpenCode"
	}
	if id == "" {
		return ""
	}
	return strings.ToUpper(id[:1]) + id[1:]
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
	QuickNav   QuickNav
	// Jingle is the cross-device notification jingle selection, rendered into
	// a meta tag so the client picks the right tune.
	Jingle string
	// HasTabStrip marks the attach pages, which render the terminal tab strip
	// inline. Every other page gets a hidden switcher-only terminal-tabs
	// instance from the layout, so the double Ctrl/Meta switcher works app wide.
	HasTabStrip bool
	// AssistantID is the conversation the assistant entry points open, and
	// AssistantNews whether it has unread news. The three entry points carry
	// the id as a notification target, so dc-notifications marks them live the
	// way it marks a terminal row.
	AssistantID   string
	AssistantNews bool
	// BackupReviewCount is the number of open backup overwrite reviews,
	// rendered as a badge on the Settings nav so the pending resolution is
	// visible app wide. Fresh on every navigation (whole body boost).
	BackupReviewCount int
	// Steered marks the coders an open job holds, keyed by terminal id: the
	// assistant owns those and may write into them, and every surface shows
	// it as the purple steered mark. SteerPrefill carries the stored
	// criterion of a closed job, which is what the steer dialog offers when
	// such a terminal is steered again. Surfaces read both by id
	// (`{{index $.Steered .ID}}`) instead of carrying the values through
	// their own view structs; the fragments refresh on the terminals event,
	// so the marks stay current without client logic.
	Steered      map[string]bool
	SteerPrefill map[string]string
	// Host is the machine's load, memory and disk at render time, so the status
	// in the header is right before the first event arrives. It refreshes over
	// the event stream from there.
	Host hostinfo.Stats
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
	GroupCol  int    // column inside the group from @dc_tab_gcol, 0 for a column of its own
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

// MemberCols returns the space separated column indices matching MemberIDs (0
// for a member that renders as a column of its own). The strip is the split
// page's live mirror, so a column change made anywhere travels to an open
// split through this attribute, the way the member order does.
func (t StripTab) MemberCols() string {
	if len(t.Members) == 0 {
		return ""
	}
	cols := make([]string, len(t.Members))
	for i := range t.Members {
		cols[i] = strconv.Itoa(t.Members[i].GroupCol)
	}
	return strings.Join(cols, " ")
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
