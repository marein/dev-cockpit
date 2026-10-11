package render

import (
	"strings"

	"github.com/marein/dev-cockpit/internal/project"
)

// ProjectTagsData is the model for a main project's tags form. Tags are the
// project's, the stored ones or what a refused save sent, a new one without
// an id, InUse every tag there is, so the form offers them in their colors,
// Colors every color a tag can wear and NextColors the ones new tags take in
// turn.
type ProjectTagsData struct {
	Page
	Project    string
	Tags       []project.Tag
	InUse      []project.TagUse
	Colors     []string
	NextColors []string
	Return     string
	// Modal marks the render for the dialog, like CoderNewData.Modal.
	Modal bool
}

// Offered lists the tags the project does not carry.
func (d ProjectTagsData) Offered() []project.TagUse {
	on := map[string]bool{}
	for _, tag := range d.Tags {
		on[tag.ID] = true
	}
	var offered []project.TagUse
	for _, use := range d.InUse {
		if !on[use.ID] {
			offered = append(offered, use)
		}
	}
	return offered
}

// SettingsTagsData feeds Settings › Projects › Tags: every tag as a row, the
// new tags a refused save sent, the colors a tag can wear, NextColors the
// ones new tags take in turn, and Unread when the tags could not be read,
// which shows no list rather than an empty one.
type SettingsTagsData struct {
	Page
	SettingsNav SettingsNav
	Rows        []TagRow
	New         []project.Tag
	Colors      []string
	NextColors  []string
	Unread      bool
}

// TagRow is one tag on the settings page, as loaded, with the name, the color
// and the removal its fields show. They differ from the loaded ones only
// after a refused save, which comes back with what it sent.
type TagRow struct {
	project.TagState
	Field  string
	Pick   string
	Remove bool
}

// Carriers joins the projects carrying the tag for the page's script. A
// project's name is a directory's, it never holds a slash.
func (r TagRow) Carriers() string {
	return strings.Join(r.Projects, "/")
}
