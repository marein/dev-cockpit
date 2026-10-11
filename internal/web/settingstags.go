package web

import (
	"cmp"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/web/render"
)

const settingsTagsPath = "/settings/projects/tags"

// handleSettingsProjectsTags lists every tag for its name and its color on
// every project at once.
func (s *Server) handleSettingsProjectsTags(c *gin.Context) {
	s.renderSettingsTags(c, nil, nil)
}

// handleSettingsProjectsTagsSave applies what the page staged in one write.
// A refusal renders the page again rather than redirecting, which would lose
// what was sent.
func (s *Server) handleSettingsProjectsTagsSave(c *gin.Context) {
	if err := readForm(c); err != nil {
		s.renderSettingsTags(c, nil, err)
		return
	}
	changes := sentTagChanges(c)
	saved, err := s.projects.SaveTags(changes)
	if err != nil {
		s.renderSettingsTags(c, changes, err)
		return
	}
	s.publishProjects()
	s.redirectWithFlash(c, settingsTagsPath, tagsSavedMessage(saved), "")
}

func (s *Server) renderSettingsTags(c *gin.Context, sent []project.TagChange, refused error) {
	data, problem := s.settingsTagsData(sent, refused)
	data.Page = s.page(c, "Settings", "settings")
	data.SettingsNav = s.settingsNav("projects")
	if problem != "" {
		data.Page.Flash = render.Flash{Message: problem, Level: "error"}
	}
	c.HTML(http.StatusOK, "settings_projects_tags.gohtml", data)
}

// settingsTagsData answers the page as it stands with the tags as they are
// now, and what to say on top of it. After a refused save a tag shows what
// was sent for it only where the page changed it and the tag is still the
// one the page loaded, so an edit typed here stays while everything else
// shows as it is now, and no later save carries an older value back. An edit
// of a tag that moved elsewhere is dropped and named as the page loaded it. A
// file that cannot be
// read shows no list, an empty one would read as no tags.
func (s *Server) settingsTagsData(sent []project.TagChange, refused error) (render.SettingsTagsData, string) {
	var problem string
	if refused != nil {
		problem = refused.Error()
	}
	states, err := s.projects.TagStates()
	if err != nil {
		problem = err.Error()
	}
	var dropped []string
	var added []project.Tag
	for _, change := range sent {
		if change.ID == "" {
			tag := project.Tag{Name: change.Name}
			if slices.Contains(project.TagColors, change.Color) {
				tag.Color = change.Color
			}
			added = append(added, tag)
		}
	}
	rows := make([]render.TagRow, 0, len(states))
	uses := make([]project.TagUse, 0, len(states))
	for _, state := range states {
		row := render.TagRow{TagState: state, Field: state.Name, Pick: state.Color}
		if i := slices.IndexFunc(sent, func(change project.TagChange) bool { return change.ID == state.ID }); i >= 0 && sent[i].Changed() {
			if sent[i].Version != state.Version {
				dropped = append(dropped, cmp.Or(sent[i].LoadedName, state.Name))
			} else {
				row.Field, row.Remove = sent[i].Name, sent[i].Remove
				if slices.Contains(project.TagColors, sent[i].Color) {
					row.Pick = sent[i].Color
				}
			}
		}
		rows = append(rows, row)
		uses = append(uses, project.TagUse{Tag: state.Tag})
	}
	for _, change := range sent {
		if change.ID != "" && change.Changed() && !slices.ContainsFunc(states, func(state project.TagState) bool { return state.ID == change.ID }) {
			dropped = append(dropped, cmp.Or(change.LoadedName, change.ID))
		}
	}
	var conflict *project.TagsConflict
	if len(dropped) > 0 && err == nil && !errors.As(refused, &conflict) {
		problem = strings.TrimSpace(problem + " " + (&project.TagsConflict{Names: dropped}).Error())
	}
	return render.SettingsTagsData{Rows: rows, New: added, Colors: project.TagColors, NextColors: project.NextTagColors(uses), Unread: err != nil}, problem
}

// sentTagChanges reads the page's rows: every tag it shows by its id (tag)
// with version:<id>, was:<id> and was-color:<id> as it loaded the tag, and
// name:<id>, color:<id> and remove:<id> as it shows it now, then every new
// tag by new-name and new-color, paired in the order they stand. A row
// without an id names no tag, a change without one would read as a new tag.
func sentTagChanges(c *gin.Context) []project.TagChange {
	var changes []project.TagChange
	for _, id := range c.PostFormArray("tag") {
		if id == "" {
			continue
		}
		changes = append(changes, project.TagChange{
			ID:          id,
			Version:     c.PostForm("version:" + id),
			LoadedName:  c.PostForm("was:" + id),
			LoadedColor: c.PostForm("was-color:" + id),
			Name:        c.PostForm("name:" + id),
			Color:       c.PostForm("color:" + id),
			Remove:      c.PostForm("remove:"+id) == "1",
		})
	}
	colors := c.PostFormArray("new-color")
	for i, name := range c.PostFormArray("new-name") {
		change := project.TagChange{Name: name}
		if i < len(colors) {
			change.Color = colors[i]
		}
		changes = append(changes, change)
	}
	return changes
}

func tagsSavedMessage(saved project.TagsSaved) string {
	var parts []string
	for _, part := range []struct {
		count int
		word  string
	}{{saved.Created, "created"}, {saved.Renamed, "renamed"}, {saved.Recolored, "recolored"}, {saved.Merged, "merged"}, {saved.Removed, "removed"}} {
		if part.count > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", part.count, part.word))
		}
	}
	if len(parts) == 0 {
		return "Nothing changed."
	}
	return "Tags saved: " + strings.Join(parts, ", ") + "."
}
