package web

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// The defaults per purpose are settings, the four keys assistant.ModelDefaultKey
// names per coder and the effort beside each under assistant.EffortDefaultKey,
// edited on two pages: Settings, Assistants, Models, the
// first tab of the assistant settings and where its bare path lands, sets
// per coder the three creation defaults a new assistant is made with where
// they are set, its chat model over the coder's own start default, its check
// model and its trigger model, which the tab says in one line, and nothing
// on it is read at run time; and a
// coder's own settings page sets that start default, the one level that
// knows the CLI, and holds the names its repository remembered, one control
// with a row per name and the add row under them, so an empty list is the
// add row and no hole. Every pick is over the coder's repository, and a name
// typed under Other… goes through Add like on every other select.
const assistantModelsSettingsPath = "/settings/assistant/models"

// assistantPurposes are the three purposes the assistant settings tab picks;
// the start default is the coder page's.
var assistantPurposes = []assistant.ModelPurpose{assistant.ModelPurposeChat, assistant.ModelPurposeCheck, assistant.ModelPurposeTrigger}

// modelDefaultField is the form field one default posts under, and
// effortDefaultField the field of the effort beside it.
func modelDefaultField(coderID string, purpose assistant.ModelPurpose) string {
	return string(purpose) + "-" + coderID
}

func effortDefaultField(coderID string, purpose assistant.ModelPurpose) string {
	return "effort-" + string(purpose) + "-" + coderID
}

func (s *Server) handleSettingsAssistantModels(c *gin.Context) {
	data := render.SettingsAssistantModelsData{
		Page:        s.page(c, "Settings", "settings"),
		SettingsNav: s.settingsNav("assistant"),
		Section:     "models",
	}
	for i := range s.coders {
		id := s.coders[i].ID()
		repo := s.coderModelRepository(id)
		levels := coder.EffortsFor(s.coders[i].Coder())
		defaults := s.modelDefaults(id)
		data.Coders = append(data.Coders, render.CoderModelDefaults{
			ID:            id,
			Label:         render.CoderLabel(id),
			Note:          repo.Note(),
			Warning:       s.modelWarning(s.coders[i].Coder()),
			Chat:          modelPick(modelDefaultField(id, assistant.ModelPurposeChat), defaults.Chat, coderDefaultLabel(defaults.Start), repo),
			Check:         modelPick(modelDefaultField(id, assistant.ModelPurposeCheck), defaults.Check, modelSameChatLabel, repo),
			Trigger:       modelPick(modelDefaultField(id, assistant.ModelPurposeTrigger), defaults.Trigger, modelSameChatLabel, repo),
			ChatEffort:    effortPick(effortDefaultField(id, assistant.ModelPurposeChat), defaults.ChatEffort, coderDefaultLabel(defaults.StartEffort), defaults.Chat, levels),
			CheckEffort:   effortPick(effortDefaultField(id, assistant.ModelPurposeCheck), defaults.CheckEffort, modelSameChatLabel, defaults.Check, levels),
			TriggerEffort: effortPick(effortDefaultField(id, assistant.ModelPurposeTrigger), defaults.TriggerEffort, modelSameChatLabel, defaults.Trigger, levels),
		})
	}
	c.HTML(http.StatusOK, "settings_assistant_models.gohtml", data)
}

// handleSettingsAssistantModelsSave stores the posted defaults. Every field
// is checked before any is stored, so a refused name or level leaves the
// page as it was, and a field the form did not post is left alone, the way a
// trigger's spec reads its fields. The defaults are a new assistant's picks
// once copied, so every purpose they move is checked as the pair a new
// assistant would run on, by the coder's own rule, and a refusal names the
// coder and the purpose.
func (s *Server) handleSettingsAssistantModelsSave(c *gin.Context) {
	type update struct {
		coderID  string
		was, now assistant.ModelDefaults
	}
	var updates []update
	for i := range s.coders {
		co := s.coders[i]
		id := co.ID()
		was := s.modelDefaults(id)
		now := was
		for _, purpose := range assistantPurposes {
			model, effort := defaultFields(&now, purpose)
			if raw, ok := c.GetPostForm(modelDefaultField(id, purpose)); ok {
				name, err := assistant.CleanModel(raw)
				if err != nil {
					s.redirectWithFlash(c, assistantModelsSettingsPath, "", err.Error())
					return
				}
				*model = name
			}
			if raw, ok := c.GetPostForm(effortDefaultField(id, purpose)); ok {
				level, err := assistant.CleanEffort(raw)
				if err != nil {
					s.redirectWithFlash(c, assistantModelsSettingsPath, "", err.Error())
					return
				}
				*effort = level
			}
		}
		check := func(model, level string) error { return coder.CheckEffortFor(co.Coder(), model, level) }
		if err := assistant.CheckPicks(creationPicks(was), creationPicks(now), was, check); err != nil {
			s.redirectWithFlash(c, assistantModelsSettingsPath, "", render.CoderLabel(id)+", "+err.Error())
			return
		}
		updates = append(updates, update{coderID: id, was: was, now: now})
	}
	for _, u := range updates {
		for _, purpose := range assistantPurposes {
			wasModel, wasEffort := defaultFields(&u.was, purpose)
			model, effort := defaultFields(&u.now, purpose)
			if *model != *wasModel {
				if _, err := assistant.StoreModelDefault(s.settings, u.coderID, purpose, *model); err != nil {
					s.redirectWithFlash(c, assistantModelsSettingsPath, "", err.Error())
					return
				}
				s.rememberModel(u.coderID, *model)
			}
			if *effort != *wasEffort {
				if _, err := assistant.StoreEffortDefault(s.settings, u.coderID, purpose, *effort); err != nil {
					s.redirectWithFlash(c, assistantModelsSettingsPath, "", err.Error())
					return
				}
			}
		}
	}
	s.redirectWithFlash(c, assistantModelsSettingsPath, "Settings saved.", "")
}

// defaultFields are the model and the effort default of one purpose of the
// tab.
func defaultFields(d *assistant.ModelDefaults, purpose assistant.ModelPurpose) (model, effort *string) {
	switch purpose {
	case assistant.ModelPurposeCheck:
		return &d.Check, &d.CheckEffort
	case assistant.ModelPurposeTrigger:
		return &d.Trigger, &d.TriggerEffort
	}
	return &d.Chat, &d.ChatEffort
}

// creationPicks are the picks a new assistant is made with out of the tab's
// defaults, see assistant.Service.create.
func creationPicks(d assistant.ModelDefaults) assistant.Summary {
	return assistant.Summary{
		Model: d.Chat, CheckModel: d.Check, TriggerModel: d.Trigger,
		Effort: d.ChatEffort, CheckEffort: d.CheckEffort, TriggerEffort: d.TriggerEffort,
	}
}

// handleCoderModels is a coder's Models section: the start default over its
// list with the start effort beside it, and the names its repository
// remembered.
func (s *Server) handleCoderModels(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo := s.coderModelRepository(co.ID())
		defaults := s.modelDefaults(co.ID())
		var added []string
		for _, m := range repo.List() {
			if m.Added() {
				added = append(added, m.Name)
			}
		}
		c.HTML(http.StatusOK, "coder_models.gohtml", render.CoderModelsData{
			Page:        s.page(c, s.coderTitle(co, "Models"), "settings"),
			SettingsNav: s.coderSettingsNav("coder", co, "models"),
			Base:        s.coderBase(co),
			Start:       modelPick("start", defaults.Start, modelDefaultLabel(""), repo),
			StartEffort: effortPick("start_effort", defaults.StartEffort, modelDefaultLabel(""), defaults.Start, coder.EffortsFor(co.Coder())),
			Note:        repo.Note(),
			Warning:     s.modelWarning(co.Coder()),
			Added:       added,
			MaxRunes:    assistant.MaxModelRunes,
		})
	}
}

// handleCoderModelsSave stores the start default and the start effort, the
// two checked as the pair a start without a pick runs on, by the coder's own
// rule, before either is stored, so a refused pair leaves both as they were.
// A form without the effort field leaves the effort standing, and the stored
// one is checked beside the new model.
func (s *Server) handleCoderModelsSave(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := s.coderBase(co) + "/models"
		model, err := assistant.CleanModel(c.PostForm("start"))
		if err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		effort := s.modelDefaults(co.ID()).StartEffort
		if raw, ok := c.GetPostForm("start_effort"); ok {
			if effort, err = assistant.CleanEffort(raw); err != nil {
				s.redirectWithFlash(c, target, "", err.Error())
				return
			}
		}
		if err := coder.CheckEffortFor(co.Coder(), model, effort); err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		if _, err := assistant.StoreModelDefault(s.settings, co.ID(), assistant.ModelPurposeStart, model); err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		s.rememberModel(co.ID(), model)
		if _, err := assistant.StoreEffortDefault(s.settings, co.ID(), assistant.ModelPurposeStart, effort); err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		s.redirectWithFlash(c, target, "Settings saved.", "")
	}
}

// handleCoderModelAdd remembers one name typed into the add row, checked by
// the repository with the shared rule.
func (s *Server) handleCoderModelAdd(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := s.coderBase(co) + "/models"
		name := strings.TrimSpace(c.PostForm("name"))
		if err := s.coderModelRepository(co.ID()).Add(name); err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		s.redirectWithFlash(c, target, name+" added.", "")
	}
}

// handleCoderModelDelete forgets one added name. A CLI name is refused by the
// repository with its own sentence.
func (s *Server) handleCoderModelDelete(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := s.coderBase(co) + "/models"
		name := strings.TrimSpace(c.PostForm("name"))
		if err := s.coderModelRepository(co.ID()).Delete(name); err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		s.redirectWithFlash(c, target, name+" removed.", "")
	}
}
