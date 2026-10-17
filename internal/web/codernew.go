package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/web/render"
)

type coderCreateForm struct {
	// Name is optional: a coder without one starts under the CLI's own title,
	// see coder.Manager.Start.
	Name    AlphaNumDashString `form:"name"`
	Project string             `form:"project" binding:"required"`
	Coder   string             `form:"coder"`
	Agent   string             `form:"agent"`
	// Model is the model the session starts on, empty for the CLI's own
	// default. It is checked by the shared rule before the session exists,
	// coder.Manager.Start refuses a name no CLI takes with that rule's own
	// sentence, which the dialog shows like every other refusal. Effort is the
	// level beside it, checked against the coder's levels the same way.
	Model             string       `form:"model"`
	Effort            string       `form:"effort"`
	AutomaticApproval CheckboxBool `form:"automatic_approval"`
	// Task is the first prompt the session starts with. It reaches the CLI in
	// its argv, so it arrives whether or not the CLI already reads stdin.
	Task string `form:"prompt"`
	// DoneWhen asks for the new coder to be steered, in the same request that
	// creates it. The criterion is validated before the session exists: a
	// criterion the watcher would refuse must not leave a running coder
	// without its job, and a repeated call would start a second session on
	// the same task.
	DoneWhen string `form:"done_when"`
	// Then is the sequel: what happens once that job closes done. It becomes a
	// one shot trigger on this very terminal, made in this request, so there
	// is no window between the coder starting and the arrangement standing. A
	// job can close before a second call could make the trigger, and the
	// sequel would then never fire.
	Then string `form:"then"`
}

func (s *Server) handleCoderNew(c *gin.Context) {
	// Only the project the form was opened from preselects an option. Without
	// one the server marks none, so the browser takes the first one, which the
	// select ends up with in the order the projects page is in.
	defaultPath := ""
	if name := strings.TrimSpace(c.Query("project")); name != "" {
		if p, err := s.projects.FindByName(name); err == nil {
			defaultPath = p.Path
		}
	}
	selected, err := s.coderFromRequest(c)
	if err != nil {
		selected = s.coders[0]
	}
	coders := make([]render.CoderChoice, 0, len(s.coders))
	for i := range s.coders {
		co := s.coders[i]
		defaultAgent := ""
		if agentID, err := co.Coder().AgentRepository().ValidateSelected(c.Query("agent")); err == nil {
			defaultAgent = agentID
		}
		// The model select offers what this coder's repository offers, the
		// same list the assistant's selects read, with nothing picked: a new
		// session starts on the coder's start default, which the empty entry
		// names, else the CLI's own, unless somebody picks otherwise.
		repo := s.coderModelRepository(co.ID())
		defaults := s.modelDefaults(co.ID())
		coders = append(coders, render.CoderChoice{
			ID:           co.ID(),
			Agents:       co.Coder().AgentRepository().Options(),
			DefaultAgent: defaultAgent,
			Model:        modelPick("model", "", modelDefaultLabel(defaults.Start), repo),
			Effort:       effortPick("effort", "", modelDefaultLabel(defaults.StartEffort), "", coder.EffortsFor(co.Coder())),
			ModelNote:    repo.Note(),
			ModelWarning: s.modelWarning(co.Coder()),
		})
	}
	// A coder needs its whole form, so a split scoped create opens it
	// prefilled and carries the group target through as hidden fields.
	target := splitTargetFromRequest(c)
	page := s.page(c, "New Coder", "projects")
	data := render.CoderNewData{
		Page:              page,
		Projects:          render.ProjectOptions(page.QuickNav.AllProjects),
		DefaultPath:       defaultPath,
		Coders:            coders,
		SelectedCoder:     selected.ID(),
		AutomaticApproval: true,
		Return:            s.formReturn(c),
		Panel:             c.Query("panel") == "1",
		Modal:             inFormModal(c),
		SplitGroup:        target.Group,
		SplitColumn:       target.Column,
	}
	// The dialog asked for this page and gets the form alone, the same one the
	// page renders around its card.
	if data.Modal {
		c.HTML(http.StatusOK, "coders_new_form.gohtml", data)
		return
	}
	c.HTML(http.StatusOK, "coders_new.gohtml", data)
}
