package copilot

import (
	"encoding/json"
	"os"
	"slices"
	"strings"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
)

// copilotEfforts are the levels `copilot --reasoning-effort` takes, from its
// own help (GitHub Copilot CLI 1.0.98). copilot checks a level against the
// model itself and refuses one the model does not take, see effortRefusal.
var copilotEfforts = []string{"none", "minimal", "low", "medium", "high", "xhigh", "max"}

// Efforts implements coder.EffortKeeper.
func (p *Coder) Efforts() []string { return slices.Clone(copilotEfforts) }

// CheckEffort implements coder.EffortKeeper over the whole list: copilot
// knows which model takes which level and nothing here does, so a level the
// model refuses is copilot's refusal, see effortRefusal.
func (p *Coder) CheckEffort(_, level string) error {
	_, err := assistant.CleanEffortIn(level, copilotEfforts)
	return err
}

// copilotModelsNote is the line under every select over copilot's list.
const copilotModelsNote = "auto, your recent Copilot models, and what you add here."

// ModelRepository implements coder.ModelKeeper, the one list the New coder
// dialog and the assistant's selects both read.
func (p *Coder) ModelRepository() coder.ModelRepository { return p.models }

// cliModels are the CLI's own names: auto, which lets copilot pick, then the
// models the user ran recently, which is what copilot's own config remembers.
// There is no list command, so every other name is typed.
func (p *Coder) cliModels() []coder.Model {
	names := []string{"auto"}
	if data, err := os.ReadFile(p.config); err == nil {
		names = append(names, recentModels(data)...)
	}
	models := make([]coder.Model, len(names))
	for i, name := range names {
		models[i] = coder.Model{Name: name}
	}
	return models
}

// recentModels reads recentModelIds out of copilot's config.json. The file
// opens with comment lines before the JSON, which no JSON reader takes, so
// those are dropped first; a file that cannot be read after that is an empty
// list, the way a missing one is. auto is the select's own first entry and is
// not repeated, and a name no session could carry is left out.
func recentModels(data []byte) []string {
	var kept []string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "//") {
			continue
		}
		kept = append(kept, line)
	}
	var config struct {
		Recent []string `json:"recentModelIds"`
	}
	if err := json.Unmarshal([]byte(strings.Join(kept, "\n")), &config); err != nil {
		return nil
	}
	var out []string
	for _, raw := range config.Recent {
		name, err := assistant.CleanModel(raw)
		if err != nil || name == "" || name == "auto" || slices.Contains(out, name) {
			continue
		}
		out = append(out, name)
	}
	return out
}
