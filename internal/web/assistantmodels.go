package web

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// The models an assistant's turns run on are picked at the ring button in the
// composer row: one for the chat, one for the checks of its steered jobs and
// one for the reactions of its triggers, each out of the list the coder's
// repository offers or typed past it. What a turn really gets is
// assistant.ModelFor's answer: for the chat the pick, else the coder's start
// default, else empty, the CLI's own default, which is what every turn ran
// on before a model could be picked;
// for a check the pick, else what the chat resolves to when the check starts,
// the ring's chat pick included, and nothing else; for a reaction the
// trigger's own model, else the ring's Triggers pick, else the chat the same
// way. The New coder
// dialog picks a session's model out of the same list, see handleCoderNew,
// and the two settings pages pick the defaults, see assistantmodelsettings.go.

// The empty entry of a select is worded per level, always with the resolved
// name. The coder's own Start pick reads Default (CLI), the one level that
// knows the CLI. On the Models tab the Chat pick reads Coder default (<start
// default>), or Coder default (CLI) without one, and the Checks and Triggers
// picks read Same as chat, what a new assistant is made with where those
// creation defaults are left empty. On the ring the Chat pick reads that
// same Coder default (<start default>) or Coder default (CLI), because the
// chain below a chat pick is the coder's start default and the CLI and
// nothing else, and the Checks and the Triggers pick read Same as chat,
// exactly the rule: an empty pick there runs on the chat as it stands when
// the turn starts, stored as nothing. The trigger form's empty entry
// reads Assistant default (<name>), the owner's Triggers pick where one
// stands, else its resolved chat, and Assistant default (CLI) where that
// ends at nothing: a trigger without a model of its own runs on what its
// owner resolves to for triggers when it fires, stored as nothing. One
// wording wherever a pick follows the chat model, so the ring and the tab
// cannot name the same thing two ways. The New coder dialog reads Default
// (<start default>), else Default (CLI). No help line stands under a pick,
// the one line a list gets is the repository's note, and the Models tab
// carries one line of its own saying that it applies to new assistants.
const modelSameChatLabel = "Same as chat"

// assistantDefaultLabel is the trigger form's empty entry over what the
// owner resolves to for its triggers, the name where one resolves and the
// CLI where none does.
func assistantDefaultLabel(name string) string {
	if name == "" {
		return "Assistant default (CLI)"
	}
	return "Assistant default (" + name + ")"
}

// modelDefaultLabel is the empty entry over a default, the name where one
// resolves and the CLI where none does.
func modelDefaultLabel(name string) string {
	if name == "" {
		return "Default (CLI)"
	}
	return "Default (" + name + ")"
}

// coderDefaultLabel is the Chat entry on the Models tab and on the ring over
// the coder's start default, the level below a chat pick.
func coderDefaultLabel(start string) string {
	if start == "" {
		return "Coder default (CLI)"
	}
	return "Coder default (" + start + ")"
}

// assistantModels stores the three models the ring's menu posts, form=model
// with `model`, `check_model` and `trigger_model`. A field the request does
// not carry leaves what stands, the way a trigger's spec reads its fields;
// the page posts all three. A name typed under Other… is remembered by the
// coder's repository, so it stands in every later list. The answer is JSON
// for the page, which toasts the sentence, and a flash for a form posted
// without JS. Every other open page of the assistant learns of the change
// over its stream, SetModels publishes the models frame, so a pick set here,
// on another tab or by the CLI shows everywhere without a reload.
func (s *Server) assistantModels(c *gin.Context, id string) {
	var choice assistant.ModelChoice
	if raw, ok := c.GetPostForm("model"); ok {
		choice.Chat, choice.ChatSet = raw, true
	}
	if raw, ok := c.GetPostForm("check_model"); ok {
		choice.Check, choice.CheckSet = raw, true
	}
	if raw, ok := c.GetPostForm("trigger_model"); ok {
		choice.Trigger, choice.TriggerSet = raw, true
	}
	entry, err := s.assistants.SetModels(id, choice)
	if err != nil {
		s.assistantActionError(c, id, err)
		return
	}
	s.rememberModel(entry.CoderID, entry.Model)
	s.rememberModel(entry.CoderID, entry.CheckModel)
	s.rememberModel(entry.CoderID, entry.TriggerModel)
	message := assistantModelsMessage(entry.Summary, s.modelDefaults(entry.CoderID))
	if wantsJSON(c.Request) {
		// models is the same reading the stream's models frame carries, picks
		// and stamp, so the page applies its own save and a frame from
		// elsewhere through one shape and can tell which of the two is older.
		c.JSON(http.StatusOK, gin.H{"saved": true, "model": entry.Model, "checkModel": entry.CheckModel, "triggerModel": entry.TriggerModel, "models": entry.ModelPicks(), "message": message})
		return
	}
	s.redirectWithFlash(c, "/assistants/"+id, message, "")
}

// assistantModelsMessage is the one sentence a save is answered with, on the
// page and in the flash alike: what the chat, the checks and the triggers
// run on, ModelFor's own reading, so the coder's start default is named and
// a pick that left it to the default reads as what really runs. A purpose
// without a pick of its own folds into the chat's clause, because it then
// runs on what the chat resolves to, the chat pick included, which is what
// Same as chat means; one pinned to a model of its own is named on its own,
// even where that is the chat's very model.
func assistantModelsMessage(entry assistant.Summary, defaults assistant.ModelDefaults) string {
	chat := orCLIDefault(assistant.ModelFor(assistant.RunChat, entry, "", defaults))
	same := []string{"Chat"}
	var own []string
	if entry.CheckModel == "" {
		same = append(same, "checks")
	} else {
		own = append(own, "checks on "+orCLIDefault(assistant.ModelFor(assistant.RunCheck, entry, "", defaults)))
	}
	if entry.TriggerModel == "" {
		same = append(same, "triggers")
	} else {
		own = append(own, "triggers on "+orCLIDefault(assistant.ModelFor(assistant.RunReaction, entry, "", defaults)))
	}
	subject := same[0]
	if len(same) > 1 {
		subject = strings.Join(same[:len(same)-1], ", ") + " and " + same[len(same)-1]
	}
	return strings.Join(append([]string{subject + " on " + chat}, own...), ", ") + "."
}

// handleAssistantModelsResolved answers the calling assistant's own three
// resolutions for its `assistant-models-get` command: the model its chat turns,
// its checks and its triggers run on where nothing narrower is picked, each
// with where the answer came from and whether it is the chat's own reading,
// so a check without a pick reads as "same as chat, ring" and never as if it
// had been picked at the ring. It needs the caller, it reads that one's own
// and nobody else's, and it changes nothing.
func (s *Server) handleAssistantModelsResolved(c *gin.Context) {
	id, err := s.assistantCaller(c)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	inst, err := s.assistants.Get(id)
	if err != nil {
		s.assistantJobError(c, err)
		return
	}
	defaults := s.modelDefaults(inst.CoderID)
	choice := func(kind assistant.RunKind) gin.H {
		reading := assistant.ModelOrigin(kind, inst.Summary, "", defaults)
		return gin.H{"model": reading.Model, "source": string(reading.From), "sameAsChat": reading.SameAsChat}
	}
	c.JSON(http.StatusOK, gin.H{
		"id": inst.ID, "coderId": inst.CoderID,
		"chat": choice(assistant.RunChat), "check": choice(assistant.RunCheck), "trigger": choice(assistant.RunReaction),
	})
}

// orCLIDefault names a resolution that ended at empty.
func orCLIDefault(name string) string {
	if name == "" {
		return "the CLI's default"
	}
	return name
}

// assistantModelsData is what the ring's menu renders for one assistant: the
// three selects over the coder's list, the stored values selected. The Chat
// select's empty entry names the coder's start default, what the chain
// below a chat pick resolves to, and the Checks and the Triggers select's
// read Same as chat: per ModelFor an empty pick there runs on what the chat
// resolves to when the turn starts, the chat pick on the select above it
// included, and on nothing else.
func (s *Server) assistantModelsData(current assistant.Instance) *render.AssistantModels {
	repo := s.coderModelRepository(current.CoderID)
	return &render.AssistantModels{
		Chat:    modelPick("model", current.Model, coderDefaultLabel(assistant.DefaultModelFor(assistant.RunChat, current.Summary, s.modelDefaults(current.CoderID))), repo),
		Check:   modelPick("check_model", current.CheckModel, modelSameChatLabel, repo),
		Trigger: modelPick("trigger_model", current.TriggerModel, modelSameChatLabel, repo),
		Note:    repo.Note(),
	}
}

// coderModelRepository is the one reading of a coder's list on this server:
// the ring, the trigger form, the New coder dialog, the settings pages and
// the `model-list` read all take it, and it asks the coder itself
// (coder.ModelKeeper), so a coder without the conversation capability still
// lists its models for a session. A coder that is not serving answers the
// empty repository.
func (s *Server) coderModelRepository(coderID string) coder.ModelRepository {
	if m := s.coderByID(coderID); m != nil {
		return coder.ModelRepositoryFor(m.Coder())
	}
	return coder.ModelRepositoryFor(nil)
}

// modelDefaults reads a coder's stored defaults, fresh, so a save on the
// settings page shows on the next render.
func (s *Server) modelDefaults(coderID string) assistant.ModelDefaults {
	return assistant.ModelDefaultsFor(s.settings, coderID)
}

// rememberModel puts a name somebody picked into the coder's repository, so a
// name typed under Other… once stands in every later list. A name from the
// list changes nothing, and an empty pick names nothing to remember.
func (s *Server) rememberModel(coderID, name string) {
	if strings.TrimSpace(name) == "" {
		return
	}
	if err := s.coderModelRepository(coderID).Add(name); err != nil {
		log.Printf("coder %s: the model %q could not be remembered: %v", coderID, name, err)
	}
}

// modelPick builds one select: the empty entry first under the word the
// caller gives it, the repository's names with the stored value selected, and
// the stored value as an entry of its own where the repository does not hold
// it, so a name stored before the repository knew it is never silently
// dropped by the next save.
func modelPick(field, current, emptyLabel string, repo coder.ModelRepository) render.AssistantModelPick {
	pick := render.AssistantModelPick{Field: field, Current: current, MaxRunes: assistant.MaxModelRunes}
	pick.Options = append(pick.Options, render.AssistantModelOption{Label: emptyLabel, Selected: current == ""})
	for _, m := range repo.List() {
		pick.Options = append(pick.Options, render.AssistantModelOption{Value: m.Name, Label: m.Name, Selected: m.Name == current})
	}
	if current != "" && !repo.Exists(current) {
		pick.Options = append(pick.Options, render.AssistantModelOption{Value: current, Label: current, Selected: true})
	}
	return pick
}

// handleAssistantModels serves the lists as JSON for the assistant's
// `model-list` command: per installed coder, or the one named by `coder`, the
// names with their source, the defaults set for it and the note. Reads only.
func (s *Server) handleAssistantModels(c *gin.Context) {
	want := strings.TrimSpace(c.Query("coder"))
	out := make([]gin.H, 0, len(s.coders))
	for i := range s.coders {
		id := s.coders[i].ID()
		if want != "" && id != want {
			continue
		}
		repo := coder.ModelRepositoryFor(s.coders[i].Coder())
		models := make([]gin.H, 0)
		for _, m := range repo.List() {
			models = append(models, gin.H{"name": m.Name, "source": m.Source()})
		}
		defaults := s.modelDefaults(id)
		out = append(out, gin.H{
			"id":       id,
			"label":    render.CoderLabel(id),
			"note":     repo.Note(),
			"models":   models,
			"defaults": gin.H{"chat": defaults.Chat, "check": defaults.Check, "trigger": defaults.Trigger, "start": defaults.Start},
		})
	}
	if want != "" && len(out) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("No coder %q is serving here.", want)})
		return
	}
	c.JSON(http.StatusOK, gin.H{"coders": out})
}
