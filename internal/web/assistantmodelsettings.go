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
// names per coder, edited on two pages: Settings, Assistants, Models, the
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

// modelDefaultField is the form field one default posts under.
func modelDefaultField(coderID string, purpose assistant.ModelPurpose) string {
	return string(purpose) + "-" + coderID
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
		defaults := s.modelDefaults(id)
		data.Coders = append(data.Coders, render.CoderModelDefaults{
			ID:      id,
			Label:   render.CoderLabel(id),
			Note:    repo.Note(),
			Chat:    modelPick(modelDefaultField(id, assistant.ModelPurposeChat), defaults.Chat, coderDefaultLabel(defaults.Start), repo),
			Check:   modelPick(modelDefaultField(id, assistant.ModelPurposeCheck), defaults.Check, modelSameChatLabel, repo),
			Trigger: modelPick(modelDefaultField(id, assistant.ModelPurposeTrigger), defaults.Trigger, modelSameChatLabel, repo),
		})
	}
	c.HTML(http.StatusOK, "settings_assistant_models.gohtml", data)
}

// handleSettingsAssistantModelsSave stores the posted defaults. Every field
// is checked before any is stored, so a refused name leaves the page as it
// was, and a field the form did not post is left alone, the way a trigger's
// spec reads its fields.
func (s *Server) handleSettingsAssistantModelsSave(c *gin.Context) {
	type pick struct {
		coderID string
		purpose assistant.ModelPurpose
		name    string
	}
	var picks []pick
	for i := range s.coders {
		id := s.coders[i].ID()
		for _, purpose := range assistantPurposes {
			raw, ok := c.GetPostForm(modelDefaultField(id, purpose))
			if !ok {
				continue
			}
			name, err := assistant.CleanModel(raw)
			if err != nil {
				s.redirectWithFlash(c, assistantModelsSettingsPath, "", err.Error())
				return
			}
			picks = append(picks, pick{coderID: id, purpose: purpose, name: name})
		}
	}
	for _, p := range picks {
		if _, err := assistant.StoreModelDefault(s.settings, p.coderID, p.purpose, p.name); err != nil {
			s.redirectWithFlash(c, assistantModelsSettingsPath, "", err.Error())
			return
		}
		s.rememberModel(p.coderID, p.name)
	}
	s.redirectWithFlash(c, assistantModelsSettingsPath, "Settings saved.", "")
}

// handleCoderModels is a coder's Models section: the start default over its
// list and the names its repository remembered.
func (s *Server) handleCoderModels(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		repo := s.coderModelRepository(co.ID())
		var added []string
		for _, m := range repo.List() {
			if m.Added {
				added = append(added, m.Name)
			}
		}
		c.HTML(http.StatusOK, "coder_models.gohtml", render.CoderModelsData{
			Page:        s.page(c, s.coderTitle(co, "Models"), "settings"),
			SettingsNav: s.coderSettingsNav("coder", co, "models"),
			Base:        s.coderBase(co),
			Start:       modelPick("start", s.modelDefaults(co.ID()).Start, modelDefaultLabel(""), repo),
			Note:        repo.Note(),
			Added:       added,
			MaxRunes:    assistant.MaxModelRunes,
		})
	}
}

func (s *Server) handleCoderModelsSave(co *coder.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		target := s.coderBase(co) + "/models"
		name, err := assistant.StoreModelDefault(s.settings, co.ID(), assistant.ModelPurposeStart, c.PostForm("start"))
		if err != nil {
			s.redirectWithFlash(c, target, "", err.Error())
			return
		}
		s.rememberModel(co.ID(), name)
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
