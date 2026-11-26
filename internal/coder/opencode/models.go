package opencode

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
)

// opencodeModelsNote is the line under every select over opencode's list.
const opencodeModelsNote = "List from opencode models, cached ten minutes."

// modelEntry is one model of `opencode models --verbose`: the provider/model
// name a turn is started under, and the bound the metadata printed under that
// name puts on what a turn may send.
type modelEntry struct {
	Name string
	// Window is the prompt bound out of the model's metadata, see windowOf,
	// zero where the block named none or could not be read, which reads as
	// unknown.
	Window int
}

// modelMetadata is what is read out of the block under a name: the two limits
// the window is resolved from, the costs and capabilities beside them are
// opencode's business.
type modelMetadata struct {
	Limit struct {
		Context int `json:"context"`
		Input   int `json:"input"`
	} `json:"limit"`
}

// ModelRepository implements coder.ModelKeeper, the one list the New coder
// dialog and the assistant's selects both read.
func (p *Coder) ModelRepository() coder.ModelRepository { return p.models }

// cliModels are the CLI's own names: what `opencode models` printed the last
// time it ran, one provider/model per line, out of the cache that refreshes
// itself, in opencode's own order.
func (p *Coder) cliModels() []coder.Model {
	if p.modelCache == nil {
		return nil
	}
	entries := p.modelCache.Entries(time.Now())
	models := make([]coder.Model, len(entries))
	for i, entry := range entries {
		models[i] = coder.Model{Name: entry.Name}
	}
	return models
}

// modelWindow is the bound a model's prompt is measured against, out of the
// same cached list the selects read (windowOf, over its metadata), so the two
// cannot come out of different lists. Zero for a name the list does not hold,
// for a list that never came back and for a model whose metadata named no
// bound, all of which read as unknown. The name is the provider/model form
// the list prints, compared as it stands.
func (p *Coder) modelWindow(name string) int {
	if p.modelCache == nil {
		return 0
	}
	for _, entry := range p.modelCache.Entries(time.Now()) {
		if entry.Name == name {
			return entry.Window
		}
	}
	return 0
}

// modelListTTL is how long what `opencode models` printed is trusted before
// the next read starts a refresh. The command boots opencode's JavaScript
// runtime and takes over a second.
const modelListTTL = 10 * time.Minute

// modelListMaxOutput caps what one run may print. A verbose list is about one
// and a half kilobytes per model (37928 bytes for 26 models, measured on
// 1.18.32), so this holds a few thousand models, and an output that reaches
// it is not a list.
const modelListMaxOutput = 4 << 20

// listModels runs `opencode models --verbose` and reads its lines. The
// verbose form costs the same run as the plain one, measured on 1.18.32, and
// is the only place the installed CLI says how much context a model holds.
func listModels(ctx context.Context) ([]modelEntry, error) {
	out, err := coder.RunListCommand(ctx, modelListMaxOutput, "opencode", "models", "--verbose")
	if err != nil {
		return nil, err
	}
	return parseModelList(out), nil
}

// parseModelList reads `opencode models --verbose`: one provider/model per
// line, each followed by its metadata as one pretty printed JSON object, from
// the line that opens the brace in its first column to the line that is
// nothing but the closing one, the braces nested inside standing indented. A
// block printed on one line is that line. A block is read for the name
// directly above it and for nothing else, so a block under a line that is no
// name is read past instead of landing on the model before it. Everything
// else is dropped: a blank line, a warning opencode prints on the way, a name
// no turn could carry. A block that cannot be read leaves its model without a
// bound rather than the list without the model, so a select still offers what
// the ring cannot measure. The plain list, names without blocks, reads the
// same way with every bound unknown. The order is opencode's own, which puts
// a provider's models together.
func parseModelList(text string) []modelEntry {
	var out []modelEntry
	var block []string
	inBlock := false
	// owner is whether the newest entry is still waiting for its block.
	owner := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case inBlock:
			block = append(block, line)
			if line != "}" {
				continue
			}
			inBlock = false
			if owner {
				out[len(out)-1].Window = windowOf(strings.Join(block, "\n"))
				owner = false
			}
		case strings.HasPrefix(line, "{"):
			block = append(block[:0], line)
			if strings.HasSuffix(strings.TrimSpace(line), "}") {
				if owner {
					out[len(out)-1].Window = windowOf(line)
					owner = false
				}
				continue
			}
			inBlock = true
		default:
			owner = false
			name, err := assistant.CleanModel(line)
			if err != nil || name == "" || !strings.Contains(name, "/") {
				continue
			}
			out = append(out, modelEntry{Name: name})
			owner = true
		}
	}
	return out
}

// windowOf is the bound one metadata block puts on a prompt: `limit.input`
// where the block names one, else `limit.context`, else zero, which reads as
// unknown. That is the bound opencode's own compaction check reads (1.18.32,
// its isOverflow measures a turn's tokens against `limit.input` less a
// reserve where the model names one, and against `limit.context` less the
// output allowance otherwise; the reserve and the allowance are its margins
// and stay out of the bound, the way claude's autocompact margin stays out
// of its window). `limit.input` is what a provider caps the prompt at below
// the whole window, and for the github-copilot provider it is number for
// number copilot's own `max_prompt_tokens`, the bound the copilot rows of
// the table in `internal/assistant` carry. The TUI's own percent reads
// against `limit.context`, which for a model with an input bound stands
// well past the point where the session compacts. A zero input reads as
// none, the way opencode reads it.
func windowOf(block string) int {
	var meta modelMetadata
	if err := json.Unmarshal([]byte(block), &meta); err != nil {
		return 0
	}
	if meta.Limit.Input > 0 {
		return meta.Limit.Input
	}
	if meta.Limit.Context > 0 {
		return meta.Limit.Context
	}
	return 0
}
