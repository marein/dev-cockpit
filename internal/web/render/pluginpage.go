package render

import (
	"html"
	"html/template"
	"strings"

	"github.com/marein/dev-cockpit/internal/pluginhost"
	"github.com/marein/dev-cockpit/internal/update"
)

// PluginPageData is a plugin's page in the cockpit's layout, Body for a
// compiled plugin always on, Region for any other.
type PluginPageData struct {
	Page
	Body   template.HTML
	Region *PluginRegionData
}

// PluginRegionData is one live region: Tab the active tab of the rail or the
// Cockpit sheet, Slot the slot it holds, Plugin the plugin whose page body it
// is, Status nil for a plugin the host does not know.
type PluginRegionData struct {
	Tab    string
	Slot   string
	Plugin string
	Status *pluginhost.Status
}

// SettingsPluginsData is Settings › Plugins. Path, PathError and Replace
// belong to the install field, Refused shows at the row of RefusedID or,
// without one, over the page.
type SettingsPluginsData struct {
	Page
	SettingsNav SettingsNav
	Folder      string
	Plugins     []PluginRow
	Path        string
	PathError   string
	Replace     string
	Refused     string
	RefusedID   string
}

// BuiltIn answers the rows of the compiled plugins, in wiring order.
func (d SettingsPluginsData) BuiltIn() []PluginRow {
	var rows []PluginRow
	for _, row := range d.Plugins {
		if row.BuiltIn {
			rows = append(rows, row)
		}
	}
	return rows
}

// Installed answers the rows of the external plugins, by id.
func (d SettingsPluginsData) Installed() []PluginRow {
	var rows []PluginRow
	for _, row := range d.Plugins {
		if !row.BuiltIn {
			rows = append(rows, row)
		}
	}
	return rows
}

// Updatable answers whether a plugin came from a repository, the only kind
// an update check reads.
func (d SettingsPluginsData) Updatable() bool {
	for _, row := range d.Plugins {
		if row.Origin.Repository != "" {
			return true
		}
	}
	return false
}

// PluginRow is one plugin's row, Notes the notes of every release its update
// steps over, newest first.
type PluginRow struct {
	pluginhost.Status
	Label   string
	Tone    string
	Enabled bool
	Notes   template.HTML
}

// PluginRows builds one row per plugin with its badge label and tone.
func PluginRows(statuses []pluginhost.Status) []PluginRow {
	rows := make([]PluginRow, 0, len(statuses))
	for _, st := range statuses {
		row := PluginRow{Status: st, Enabled: st.State != pluginhost.StateDisabled}
		if step := st.Origin.Step; step != nil {
			row.Notes = releaseNotes(step.Releases)
		}
		switch {
		case st.BuiltIn && !st.Optional:
			row.Label, row.Tone = "Always on", "green"
		case st.State == pluginhost.StateRunning:
			row.Label, row.Tone = "Running", "green"
		case st.State == pluginhost.StateStarting:
			row.Label, row.Tone = "Starting", "azure"
		case st.State == pluginhost.StateCrashed:
			row.Label, row.Tone = "Crashed", "red"
		case st.State == pluginhost.StateBroken:
			row.Label, row.Tone = "Broken", "red"
		default:
			row.Label, row.Tone = "Disabled", "secondary"
		}
		rows = append(rows, row)
	}
	return rows
}

// releaseNotes joins releases like the cockpit's update dialog: version,
// name where it differs, date and the notes the feed rendered, which carry no
// raw HTML of the forge.
func releaseNotes(releases []update.Release) template.HTML {
	var b strings.Builder
	for i, rel := range releases {
		if i > 0 {
			b.WriteString("<hr>")
		}
		b.WriteString("<p><strong>" + html.EscapeString(rel.Version) + "</strong>")
		if name := strings.TrimPrefix(rel.Name, "v"); name != "" && name != rel.Version {
			b.WriteString(" · " + html.EscapeString(rel.Name))
		}
		if len(rel.Date) >= 10 {
			b.WriteString(` <span class="text-secondary">· ` + html.EscapeString(rel.Date[:10]) + "</span>")
		}
		b.WriteString("</p>")
		if rel.NotesHTML != "" {
			b.WriteString(rel.NotesHTML)
		} else {
			b.WriteString("<p>No release notes.</p>")
		}
	}
	return template.HTML(b.String())
}

// PluginRepositoryData is the install from a repository, as dialog (Modal)
// or page. Ask repeats the question when what the offer replaces changed.
// Refused is why an install failed, shown over the address in the box the
// dialog's next send clears.
type PluginRepositoryData struct {
	Page
	SettingsNav SettingsNav
	Modal       bool
	URL         string
	Forge       string
	URLError    string
	Refused     string
	Offer       *pluginhost.Offer
	Ask         string
}

// PluginLogData is the last lines of one plugin's process.
type PluginLogData struct {
	Page
	SettingsNav SettingsNav
	Name        string
	ID          string
	Lines       []string
}
