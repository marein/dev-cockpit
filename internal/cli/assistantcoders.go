package cli

import (
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/coder"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// assistantCoders adapts the installed coders to what the assistant needs,
// keeping internal/assistant free of any dependency on internal/coder.
// assistantCoders hands the assistant the coders that can answer a turn, each
// with its effort levels and the reading of its stored model defaults, the
// purpose fallback behind a pick, read fresh on every turn from the settings.
type assistantCoders struct {
	coders []coder.Coder
	store  *settings.Store
}

func (c assistantCoders) Available() []assistant.CoderInfo {
	out := make([]assistant.CoderInfo, 0, len(c.coders))
	for _, co := range c.coders {
		runner := coder.AssistantRunnerFor(co)
		if runner == nil {
			continue
		}
		id := co.ID()
		out = append(out, assistant.CoderInfo{ID: id, Label: render.CoderLabel(id), Runner: runner, Defaults: func() assistant.ModelDefaults {
			return assistant.ModelDefaultsFor(c.store, id)
		}, CheckEffort: func(model, level string) error { return coder.CheckEffortFor(co, model, level) }})
	}
	return out
}
