package assistant

import "strings"

// How full the coder's context window stands is the one number a conversation
// cannot work out for itself. The assistant runs headless, so nobody sees the
// statusline a coder terminal shows, and the numbers only exist inside the
// provider's own output. Both CLIs report what a turn consumed, neither one
// reports how much fits, so the window is resolved in two steps: what the turn
// itself said, and otherwise the table below. A model that answers neither shows
// no fill at all, because a wrong percentage is worse than none.

// ContextUsage is how full a coder's context window stood at the end of one
// turn. It is a per turn reading, not a running total: after a compact the
// tokens drop, and the value simply follows.
type ContextUsage struct {
	// Model is what answered the turn, as the provider named it. It is kept so
	// a reading can be understood later, and so the window can be resolved
	// again when the table grows.
	Model string `json:"model,omitempty"`
	// Tier is the context tier the coder had the session on, empty for a coder
	// without tiers. It is kept for the same reason as the model: it is half of
	// what the window is looked up by.
	Tier string `json:"tier,omitempty"`
	// Tokens is what the context held at the end of the turn.
	Tokens int `json:"tokens"`
	// Window is how much that model's context holds, zero when unknown.
	Window int `json:"window"`
}

// Known reports whether this reading can be shown as a percentage.
func (u ContextUsage) Known() bool { return u.Tokens > 0 && u.Window > 0 }

// Percent is how full the window stands, 0 when the reading says nothing.
// A window that is over full reads as 100: the provider is the one that knows
// where its own limit sits, and a number above it would only look broken.
func (u ContextUsage) Percent() int {
	if !u.Known() {
		return 0
	}
	pct := u.Tokens * 100 / u.Window
	if pct > 100 {
		return 100
	}
	if pct < 1 {
		// A turn that consumed anything at all is not an empty window. Rounding
		// the first percent down to nothing would show a fresh conversation and
		// a started one alike.
		return 1
	}
	return pct
}

// PercentIn is Percent for a reading whose window was never resolved. What the
// turn reported is the model and the tokens, and those never change; how large
// that model's window is, is a lookup, so a reading taken while the table still
// said nothing about the model fills in by itself once it does, without waiting
// for another turn.
func (u ContextUsage) PercentIn(coderID string) int {
	if u.Window == 0 {
		u.Window = ContextWindow(coderID, u.Model, u.Tier)
	}
	return u.Percent()
}

// ContextTierLong is the wide context tier a coder may put a session on. It is
// copilot's name for it, and the only tier name that changes a window.
const ContextTierLong = "long_context"

// contextTiers is one model's prompt bound. A model can be reachable under more
// than one context tier, and the tier is what decides the bound, not the model
// alone.
type contextTiers struct {
	// Standard is the bound a session runs under unless it is on a wider tier.
	Standard int
	// Long is the wide tier's bound, zero when the model has no wide tier.
	Long int
}

// contextWindows is how much a model's context holds, per coder. It is not one
// table per model, because the same model does not have the same window
// everywhere: copilot offers `claude-haiku-4.5` with a 128000 token prompt bound
// while claude's own CLI reports 200000 for that family, so a shared row would
// be wrong for one of them.
//
// Where the numbers come from, so an entry can be checked instead of trusted:
//
//   - claude: read off a real turn. A `claude -p` run reports `contextWindow`
//     per model on its result record, and the parser prefers that over this
//     table on every turn, so these rows only serve a run that reported none.
//   - copilot: read off the models response its own CLI fetches
//     (`fetched models from CAPI /models` in its log at `--log-level all`),
//     `billing.token_prices.<tier>.max_prompt_tokens`, and
//     `capabilities.limits.max_prompt_tokens` for a model with only one tier and
//     no billing block. Verified on 2026-07-29 against CLI 1.0.76, which reports
//     no window of its own anywhere: the `maxPromptTokens` its older builds
//     carried on a `model.call_*` record is gone.
//
// A model that is not in here shows no fill. Add a row when a reading proves it,
// not before.
var contextWindows = map[string]map[string]contextTiers{
	"claude": {
		"claude-opus-5":       {Standard: 200_000},
		"claude-opus-5[1m]":   {Standard: 1_000_000},
		"claude-sonnet-5":     {Standard: 200_000},
		"claude-sonnet-5[1m]": {Standard: 1_000_000},
		"claude-haiku-4-5":    {Standard: 200_000},
	},
	"copilot": {
		"claude-sonnet-5":         {Standard: 200_000, Long: 936_000},
		"claude-sonnet-4.6":       {Standard: 200_000, Long: 936_000},
		"claude-sonnet-4.5":       {Standard: 128_000},
		"claude-haiku-4.5":        {Standard: 128_000},
		"gpt-5.6-terra":           {Standard: 272_000, Long: 922_000},
		"gpt-5.6-luna":            {Standard: 200_000, Long: 922_000},
		"gpt-5.4":                 {Standard: 272_000, Long: 922_000},
		"gpt-5.4-mini":            {Standard: 272_000},
		"gpt-5.3-codex":           {Standard: 272_000},
		"gpt-5-mini":              {Standard: 128_000},
		"gemini-3.1-pro-preview":  {Standard: 200_000, Long: 936_000},
		"gemini-3.6-flash":        {Standard: 200_000, Long: 936_000},
		"gemini-3.5-flash":        {Standard: 200_000, Long: 936_000},
		"grok-4.5":                {Standard: 200_000, Long: 500_000},
		"kimi-k2.7-code":          {Standard: 224_000},
		"mai-code-1-flash-picker": {Standard: 128_000},
	},
}

// ContextWindow resolves how much this coder's model holds under this tier, 0
// when the table says nothing about it. An empty tier means the standard one,
// which is what a session runs under unless the coder put it on a wider tier.
// The lookup is case insensitive and ignores a date suffix, so a dated release
// like `claude-haiku-4-5-20251001` finds the entry its family has.
func ContextWindow(coderID, model, tier string) int {
	rows := contextWindows[strings.ToLower(strings.TrimSpace(coderID))]
	if rows == nil {
		return 0
	}
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" {
		return 0
	}
	entry, ok := rows[name]
	if !ok {
		// A dated release of a model has the window of that model. What follows
		// the separator has to be the date and nothing else, so a different
		// model whose name merely starts like a known one is not read as that
		// model.
		for known, row := range rows {
			if rest, cut := strings.CutPrefix(name, known+"-"); cut && isDigits(rest) {
				entry, ok = row, true
				break
			}
		}
	}
	if !ok {
		return 0
	}
	if strings.EqualFold(strings.TrimSpace(tier), ContextTierLong) && entry.Long > 0 {
		return entry.Long
	}
	return entry.Standard
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
