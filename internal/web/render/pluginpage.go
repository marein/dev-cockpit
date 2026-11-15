package render

import "html/template"

// PluginPageData is a plugin's own page: the cockpit's layout around the
// markup the plugin added with AddPage.
type PluginPageData struct {
	Page
	Body template.HTML
}
