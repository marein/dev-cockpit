package copilot

import (
	"encoding/json"
	"os"
	"slices"
	"strings"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
)

// copilotModelsNote is the line under every select over copilot's list.
const copilotModelsNote = "auto, your recent Copilot models, and what you add here."

// ModelRepository implements coder.ModelKeeper, the one list the New coder
// dialog and the assistant's selects both read.
func (p *Coder) ModelRepository() coder.ModelRepository { return p.models }

// cliModels are the CLI's own names: auto, which lets copilot pick, then the
// models the user ran recently, which is what copilot's own config remembers.
// There is no list command, so every other name is typed.
func (p *Coder) cliModels() []string {
	names := []string{"auto"}
	if data, err := os.ReadFile(p.config); err == nil {
		names = append(names, recentModels(data)...)
	}
	return names
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
