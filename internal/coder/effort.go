package coder

import "errors"

// EffortKeeper is the optional capability of a coder whose CLI takes an
// effort level: Efforts answers the levels it offers, from the least to the
// most, what every effort select lists, and CheckEffort answers whether one
// model takes one level, with the sentence that says why not. A coder whose
// levels belong to each model answers per model, and one whose CLI decides
// per model itself answers for its whole list. A coder without the
// capability takes no level.
type EffortKeeper interface {
	Efforts() []string
	CheckEffort(model, level string) error
}

// EffortsFor answers a coder's levels, none for a coder without the
// capability.
func EffortsFor(c Coder) []string {
	if keeper, ok := c.(EffortKeeper); ok {
		return keeper.Efforts()
	}
	return nil
}

// CheckEffortFor is the coder's own check of a model and a level, nil for no
// level at all.
func CheckEffortFor(c Coder, model, level string) error {
	if level == "" {
		return nil
	}
	if keeper, ok := c.(EffortKeeper); ok {
		return keeper.CheckEffort(model, level)
	}
	return errors.New("This coder takes no effort level.")
}

// SessionPreparer is the optional capability of a runtime that has to settle
// something before a start's command is built, and that can refuse: it
// answers the start the command is built from, and a start it refuses never
// begins, so nothing is left to fail in the pane. opencode's terminal takes
// no effort flag, it is handed the level through the agent the session runs
// as, which is what such a preparation names.
type SessionPreparer interface {
	PrepareStart(start SessionStart) (SessionStart, error)
}
