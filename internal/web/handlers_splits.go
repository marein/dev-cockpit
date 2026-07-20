package web

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/terminal"
	"github.com/local/dev-cockpit/internal/tmux"
	"github.com/local/dev-cockpit/internal/web/render"
)

// maxGroupNameLength bounds a split view's display name.
const maxGroupNameLength = 120

// sessionRef carries what the split view endpoints need to know about one
// live coder or shell.
type sessionRef struct {
	TmuxSession string
	Kind        string // "coder" or "shell"
	Group       string
	GroupPos    int
	GroupName   string
}

// terminalSessions indexes every live coder and shell by identifier.
func (s *Server) terminalSessions() map[string]sessionRef {
	refs := map[string]sessionRef{}
	for i := range s.coders {
		for _, r := range s.coders[i].Snapshot().Running {
			refs[r.Identifier] = sessionRef{
				TmuxSession: r.TmuxSession,
				Kind:        "coder",
				Group:       r.TabGroup,
				GroupPos:    r.TabGroupPos,
				GroupName:   r.TabGroupName,
			}
		}
	}
	for _, sh := range s.shells.List() {
		refs[sh.Identifier] = sessionRef{
			TmuxSession: sh.TmuxSession,
			Kind:        "shell",
			Group:       sh.TabGroup,
			GroupPos:    sh.TabGroupPos,
			GroupName:   sh.TabGroupName,
		}
	}
	return refs
}

// invalidateTerminals drops the cached session lists so the next scan sees
// fresh tmux options.
func (s *Server) invalidateTerminals() {
	for i := range s.coders {
		s.coders[i].Invalidate()
	}
	s.shells.Invalidate()
}

// splitPageURL returns the split page a grouped member's own link leads to,
// with that member focused. ok is false for ungrouped sessions and for groups
// that do not fold (fewer than two live members), those keep their solo page.
func (s *Server) splitPageURL(gid, id string) (string, bool) {
	if gid == "" {
		return "", false
	}
	if len(s.groupMembers(gid)) < 2 {
		return "", false
	}
	return "/splits/" + gid + "?focus=" + id, true
}

// groupMembers returns the live sessions of one split view group in group
// order, as strip tabs (they carry name, project, coder and news already).
func (s *Server) groupMembers(gid string) []render.TerminalTab {
	var members []render.TerminalTab
	for _, t := range s.terminalTabs() {
		if t.Group == gid {
			members = append(members, t)
		}
	}
	sortGroupMembers(members)
	return members
}

// handleSplitAttach renders the split view page: one terminal island per
// member, side by side. A group that shrank to one member redirects to that
// member's own page, a gone group redirects to the projects list.
func (s *Server) handleSplitAttach(c *gin.Context) {
	gid, err := terminal.ValidateIdentifier(c.Param("id"))
	if err != nil {
		s.redirectWithFlash(c, "/projects", "", "No split view with this id was found.")
		return
	}
	members := s.groupMembers(gid)
	if len(members) == 0 {
		s.redirectWithFlash(c, "/projects", "", "No split view with this id was found.")
		return
	}
	if len(members) == 1 {
		c.Redirect(http.StatusSeeOther, members[0].URL)
		return
	}
	focus := c.Query("focus")
	focusValid := false
	for _, m := range members {
		if m.ID == focus {
			focusValid = true
			break
		}
	}
	if !focusValid {
		focus = members[0].ID
	}
	projectName := commonProject(members)
	if projectName != "" {
		s.projects.Touch(projectName)
	}
	// Only the focused pane counts as seen; the other members keep their news
	// until their pane is activated (the client posts the read then).
	s.notifier.MarkTargetRead(focus)
	groupName := groupLabel(members)
	page := s.page(c, pageTitle(groupName, projectName), "projects")
	page.HasTabStrip = true
	rendered := make([]render.SplitMember, 0, len(members))
	for _, m := range members {
		base := m.URL
		sm := render.SplitMember{
			ID:            m.ID,
			Name:          m.Name,
			Kind:          m.Kind,
			Coder:         m.Coder,
			Project:       m.Project,
			URL:           base,
			StreamURL:     base + "/stream",
			ResizeURL:     base + "/resize",
			InputURL:      base + "/input",
			ScrollHistory: m.Kind == "shell",
		}
		if m.Kind == "coder" {
			if co, running, err := s.resolveRunning(m.ID); err == nil {
				if files, err := co.Coder().SessionRepository().ListFiles(running.Identifier); err == nil {
					sm.FilesData = &render.CoderFilesData{
						Page:            page,
						Identifier:      m.ID,
						Files:           files,
						MaxUploadSizeMB: maxRequestBodyMegabytes(s.cfg.MaxRequestBodySize),
					}
				}
			}
		}
		rendered = append(rendered, sm)
	}
	c.HTML(http.StatusOK, "split_attach.gohtml", render.SplitAttachData{
		Page:          page,
		GroupID:       gid,
		GroupName:     groupName,
		ProjectName:   projectName,
		Focus:         focus,
		FocusExplicit: focusValid,
		Members:       rendered,
	})
}

type groupRequest struct {
	IDs []string `json:"ids"`
}

// handleTerminalTabsGroup creates or extends a split view group: the posted
// session ids become its members in that order. When one of them already
// belongs to a group, that group is reused (its id, name and unlisted members
// survive), so dropping a tab onto a group joins it and dropping groups onto
// each other merges them.
func (s *Server) handleTerminalTabsGroup(c *gin.Context) {
	var req groupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "Invalid split view request.")
		return
	}
	if len(req.IDs) > maxTabOrderIDs {
		c.String(http.StatusRequestEntityTooLarge, "Too many entries.")
		return
	}
	refs := s.terminalSessions()
	seen := map[string]bool{}
	var members []string
	gid := ""
	for _, id := range req.IDs {
		ref, ok := refs[id]
		if !ok || seen[id] {
			continue
		}
		seen[id] = true
		members = append(members, id)
		if gid == "" && ref.Group != "" {
			gid = ref.Group
		}
	}
	gname := ""
	if gid != "" {
		var leftover []string
		for id, ref := range refs {
			if ref.Group != gid {
				continue
			}
			if gname == "" {
				gname = ref.GroupName
			}
			if !seen[id] {
				leftover = append(leftover, id)
			}
		}
		sort.Slice(leftover, func(i, j int) bool {
			pi, pj := refs[leftover[i]].GroupPos, refs[leftover[j]].GroupPos
			if pi != pj {
				return pi < pj
			}
			return leftover[i] < leftover[j]
		})
		members = append(members, leftover...)
	}
	if len(members) < 2 {
		c.String(http.StatusBadRequest, "A split view needs at least two terminals.")
		return
	}
	if gid == "" {
		key, err := terminal.NewKey()
		if err != nil {
			c.String(http.StatusInternalServerError, "Could not create the split view.")
			return
		}
		gid = key
	}
	names := make([]string, len(members))
	for i, id := range members {
		names[i] = refs[id].TmuxSession
	}
	if err := tmux.New().SetTabGroup(names, gid, gname); err != nil {
		c.String(http.StatusInternalServerError, "Could not create the split view.")
		return
	}
	s.invalidateTerminals()
	s.publishTerminals("")
	c.JSON(http.StatusOK, map[string]string{"id": gid, "url": "/splits/" + gid})
}

// handleTerminalTabsUngroup removes sessions from their split view groups. A
// group left with fewer than two members is dissolved entirely. The strip
// posts JSON ids; the split page's header and pane buttons post plain forms
// and get a redirect (back to the split while it lives, else to a member).
func (s *Server) handleTerminalTabsUngroup(c *gin.Context) {
	isForm := !strings.Contains(c.ContentType(), "json")
	var ids []string
	if isForm {
		ids = c.PostFormArray("id")
	} else {
		var req groupRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			c.String(http.StatusBadRequest, "Invalid split view request.")
			return
		}
		ids = req.IDs
	}
	if len(ids) > maxTabOrderIDs {
		c.String(http.StatusRequestEntityTooLarge, "Too many entries.")
		return
	}
	refs := s.terminalSessions()
	removed := map[string]bool{}
	affected := map[string]bool{}
	var names []string
	for _, id := range ids {
		ref, ok := refs[id]
		if !ok || removed[id] || ref.Group == "" {
			continue
		}
		removed[id] = true
		affected[ref.Group] = true
		names = append(names, ref.TmuxSession)
	}
	// Dissolve groups that the removal leaves with a single member.
	remaining := map[string][]string{}
	for id, ref := range refs {
		if ref.Group != "" && affected[ref.Group] && !removed[id] {
			remaining[ref.Group] = append(remaining[ref.Group], id)
		}
	}
	for _, rest := range remaining {
		if len(rest) == 1 {
			names = append(names, refs[rest[0]].TmuxSession)
		}
	}
	if len(names) > 0 {
		if err := tmux.New().ClearTabGroup(names); err != nil {
			c.String(http.StatusInternalServerError, "Could not change the split view.")
			return
		}
		s.invalidateTerminals()
		s.publishTerminals("")
	}
	landing, alive := ungroupLanding(ids, refs, remaining)
	if !isForm {
		c.JSON(http.StatusOK, map[string]string{"url": landing})
		return
	}
	if alive {
		c.Redirect(http.StatusSeeOther, landing)
		return
	}
	s.redirectWithFlash(c, landing, "Split view dissolved.", "")
}

// ungroupLanding picks where an ungroup leads: back to the split page while
// its group still has members (alive), else to the first surviving or removed
// member's own page.
func ungroupLanding(ids []string, refs map[string]sessionRef, remaining map[string][]string) (string, bool) {
	gid := ""
	for _, id := range ids {
		if ref, ok := refs[id]; ok && ref.Group != "" {
			gid = ref.Group
			break
		}
	}
	rest := remaining[gid]
	if gid != "" && len(rest) >= 2 {
		return "/splits/" + gid, true
	}
	if len(rest) == 1 {
		return sessionURL(rest[0], refs), false
	}
	for _, id := range ids {
		if _, ok := refs[id]; ok {
			return sessionURL(id, refs), false
		}
	}
	return "/projects", false
}

func sessionURL(id string, refs map[string]sessionRef) string {
	if refs[id].Kind == "shell" {
		return "/shells/" + id
	}
	return "/coders/" + id
}

type groupNameRequest struct {
	Group string `json:"group"`
	Name  string `json:"name"`
}

// handleTerminalTabsGroupName renames a split view group by writing the name
// onto every member (last write wins). An empty name removes the stored name,
// the strip then falls back to the joined member names.
func (s *Server) handleTerminalTabsGroupName(c *gin.Context) {
	var req groupNameRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.String(http.StatusBadRequest, "Invalid split view request.")
		return
	}
	gid, err := terminal.ValidateIdentifier(req.Group)
	if err != nil {
		c.String(http.StatusBadRequest, "Invalid split view request.")
		return
	}
	name := sanitizeGroupName(req.Name)
	refs := s.terminalSessions()
	var names []string
	for _, ref := range refs {
		if ref.Group == gid {
			names = append(names, ref.TmuxSession)
		}
	}
	if len(names) < 2 {
		c.String(http.StatusGone, "No split view with this id was found.")
		return
	}
	if err := tmux.New().SetTabGroupName(names, name); err != nil {
		c.String(http.StatusInternalServerError, "Could not rename the split view.")
		return
	}
	s.invalidateTerminals()
	s.publishTerminals("")
	c.JSON(http.StatusOK, map[string]string{"name": name})
}

// sanitizeGroupName trims a user supplied group name and drops control
// characters, mirroring the shell name rules.
func sanitizeGroupName(raw string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r == '\t' || r == '\n' || r == '\r' {
			return ' '
		}
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, raw)
	cleaned = strings.TrimSpace(cleaned)
	if len(cleaned) > maxGroupNameLength {
		cleaned = strings.TrimSpace(cleaned[:maxGroupNameLength])
	}
	return cleaned
}
