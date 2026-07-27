package assistant

import "testing"

func TestContextWindowResolvesADatedRelease(t *testing.T) {
	if got := ContextWindow("claude", "claude-haiku-4-5-20251001", ""); got != 200_000 {
		t.Fatalf("want a dated release to find its model's window, got %d", got)
	}
	if got := ContextWindow("claude", "Claude-Sonnet-5", ""); got != 200_000 {
		t.Fatalf("want the lookup to ignore case, got %d", got)
	}
	if got := ContextWindow("claude", "claude-opus-5[1m]", ""); got != 1_000_000 {
		t.Fatalf("want the long context variant measured against its own size, got %d", got)
	}
}

// The same model is not the same window everywhere: copilot serves claude's
// small models with a prompt bound of its own, so one shared row would be wrong
// for one of the two coders.
func TestContextWindowIsPerCoder(t *testing.T) {
	if got := ContextWindow("copilot", "claude-haiku-4.5", ""); got != 128_000 {
		t.Fatalf("want copilot's own bound for the model it serves, got %d", got)
	}
	if got := ContextWindow("claude", "claude-haiku-4-5", ""); got != 200_000 {
		t.Fatalf("want claude's own bound for its own model, got %d", got)
	}
	if got := ContextWindow("", "gpt-5.6-terra", ""); got != 0 {
		t.Fatalf("want nothing without a coder, got %d", got)
	}
}

// A model reachable under a wide tier has a far larger bound there. The tier
// travels with the reading, so a session on the wide one is not measured against
// the standard bound and shown as three times as full as it is.
func TestContextWindowFollowsTheTier(t *testing.T) {
	if got := ContextWindow("copilot", "gpt-5.6-terra", ""); got != 272_000 {
		t.Fatalf("want the standard tier without a tier, got %d", got)
	}
	if got := ContextWindow("copilot", "gpt-5.6-terra", ContextTierLong); got != 922_000 {
		t.Fatalf("want the wide tier's bound, got %d", got)
	}
	// A model with only one tier keeps its bound whatever the tier says.
	if got := ContextWindow("copilot", "gpt-5.3-codex", ContextTierLong); got != 272_000 {
		t.Fatalf("want the only bound a single tier model has, got %d", got)
	}
}

// A model the table says nothing about shows no fill: a percentage against a
// guessed window would look exact and be wrong.
func TestContextWindowRefusesToGuess(t *testing.T) {
	for _, model := range []string{"", "gpt-9-imaginary", "claude-opus-5-plus-something", "auto"} {
		if got := ContextWindow("copilot", model, ""); got != 0 {
			t.Fatalf("want no window for %q, got %d", model, got)
		}
	}
	if got := ContextWindow("nosuchcoder", "gpt-5.6-terra", ""); got != 0 {
		t.Fatalf("want no window for an unknown coder, got %d", got)
	}
}

func TestContextUsagePercent(t *testing.T) {
	cases := []struct {
		name  string
		usage ContextUsage
		want  int
	}{
		{"half", ContextUsage{Tokens: 100_000, Window: 200_000}, 50},
		{"unknown window", ContextUsage{Tokens: 100_000}, 0},
		{"nothing consumed", ContextUsage{Window: 200_000}, 0},
		// The provider is the one that knows where its own limit sits, and a
		// number above it would only look broken.
		{"over full", ContextUsage{Tokens: 250_000, Window: 200_000}, 100},
		// A started conversation is not an empty one, so the first percent
		// rounds up instead of away.
		{"barely started", ContextUsage{Tokens: 500, Window: 1_000_000}, 1},
	}
	for _, c := range cases {
		if got := c.usage.Percent(); got != c.want {
			t.Fatalf("%s: want %d percent, got %d", c.name, c.want, got)
		}
	}
}

// A reading taken while the table said nothing about the model keeps its tokens
// and its model, so it resolves by itself once the table learns that model. No
// second turn, and no rewriting of what a turn reported.
func TestPercentResolvesAWindowTheReadingNeverGot(t *testing.T) {
	stale := ContextUsage{Model: "gpt-5.6-terra", Tokens: 22918}
	if stale.Percent() != 0 {
		t.Fatalf("want nothing without a window on the reading itself, got %d", stale.Percent())
	}
	if got := stale.PercentIn("copilot"); got != 8 {
		t.Fatalf("want the window looked up for the coder, got %d", got)
	}
	// The tier the session was on travels with the reading, so the later lookup
	// is the same one the turn would have made.
	wide := ContextUsage{Model: "gpt-5.6-terra", Tier: ContextTierLong, Tokens: 22918}
	if got := wide.PercentIn("copilot"); got != 2 {
		t.Fatalf("want the wide tier honoured on a later lookup, got %d", got)
	}
	// A window the reading already carries is never second guessed.
	fixed := ContextUsage{Model: "gpt-5.6-terra", Tokens: 22918, Window: 100_000}
	if got := fixed.PercentIn("copilot"); got != 22 {
		t.Fatalf("want the reading's own window kept, got %d", got)
	}
}
