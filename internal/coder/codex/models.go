package codex

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
)

// codexModelsNote is the line under every select over codex's list.
const codexModelsNote = "List from codex debug models, cached one minute."

// modelListTTL is how long what `codex debug models` printed is trusted
// before the next read starts a refresh. The command renders codex's own
// catalog, which codex keeps current against the account itself, in about
// 30 ms (measured on 0.172.0), so a short span lets the list follow a plan's
// change soon.
const modelListTTL = time.Minute

// modelListMaxOutput caps what one run may print. The catalog carries every
// model's whole instruction template and was 837991 bytes for 12 models on
// 0.172.0, so this holds several times that, and an output that reaches it
// is not a catalog.
const modelListMaxOutput = 16 << 20

// ModelRepository implements coder.ModelKeeper, the one list the New coder
// dialog and the assistant's selects both read.
func (p *Coder) ModelRepository() coder.ModelRepository { return p.models }

// cliModels are the CLI's own names, out of the cache that refreshes itself.
func (p *Coder) cliModels() []coder.Model {
	names := p.modelCache.Entries(time.Now())
	models := make([]coder.Model, len(names))
	for i, name := range names {
		models[i] = coder.Model{Name: name}
	}
	return models
}

// listModels runs `codex debug models`, codex's own catalog for the account
// it is logged in with. models_cache.json behind it is codex's cache and its
// shape is codex's business, the command is the surface codex offers.
func listModels(ctx context.Context) ([]string, error) {
	out, err := coder.RunListCommand(ctx, modelListMaxOutput, "codex", "debug", "models")
	if err != nil {
		return nil, err
	}
	return parseCatalog([]byte(out))
}

// parseCatalog reads the slugs a session can be started on out of the
// catalog, in codex's own order. A model the catalog hides is left out the
// way codex's own model picker leaves it out, it lists visibility list alone
// (verified on 0.172.0: gpt-reserve and codex-auto-review stand there hidden).
// A slug no session could carry is dropped, and one listed twice is listed
// once.
func parseCatalog(data []byte) ([]string, error) {
	var catalog struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range catalog.Models {
		if m.Visibility != "list" {
			continue
		}
		name, err := assistant.CleanModel(m.Slug)
		if err != nil || name == "" || slices.Contains(names, name) {
			continue
		}
		names = append(names, name)
	}
	return names, nil
}
