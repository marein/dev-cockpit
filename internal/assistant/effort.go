package assistant

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/marein/dev-cockpit/internal/settings"
)

// An effort is picked beside every model and follows it everywhere: the chat,
// the checks and the triggers of an assistant, a trigger of its own and the
// start of a coder session each carry one, it is read on the levels a model
// is read on and together with it (EffortOrigin), and empty is again the
// CLI's own default at the end of it. Which levels a model takes is the
// coder's answer, CoderInfo.CheckEffort, so this package only knows the shape
// of a level and where a pair is checked.

// MaxEffortRunes bounds an effort level. The longest level the three CLIs
// take today is minimal, seven runes, and opencode's levels are the variant
// names a provider gives its models, so there is room for a longer word.
const MaxEffortRunes = 24

// CleanEffort reads an effort level the way every surface takes one: trimmed,
// at most MaxEffortRunes, a lowercase letter first and then lowercase
// letters, digits, - and _. A level travels into an argv behind a flag and
// into opencode's state, and every level the three CLIs name has that shape.
// Empty is a value, it clears the choice.
func CleanEffort(raw string) (string, error) {
	level := strings.TrimSpace(raw)
	if level == "" {
		return "", nil
	}
	if len([]rune(level)) > MaxEffortRunes {
		return "", fmt.Errorf("An effort level is at most %d characters long.", MaxEffortRunes)
	}
	for i, r := range level {
		switch {
		case r >= 'a' && r <= 'z':
		case i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '_'):
		default:
			return "", errors.New("An effort level is one lowercase word, like high.")
		}
	}
	return level, nil
}

// CleanEffortIn is CleanEffort, then one of the levels a coder takes. The
// refusal names them, so whoever typed a level reads what would have worked.
func CleanEffortIn(raw string, levels []string) (string, error) {
	level, err := CleanEffort(raw)
	if err != nil || level == "" {
		return level, err
	}
	if slices.Contains(levels, level) {
		return level, nil
	}
	if len(levels) == 0 {
		return "", errors.New("This coder takes no effort level.")
	}
	return "", fmt.Errorf("The effort is one of %s.", strings.Join(levels, ", "))
}

// EffortDefaultKey is the settings key of the effort beside one coder's
// default model for one purpose.
func EffortDefaultKey(coderID string, purpose ModelPurpose) string {
	return "effort-" + string(purpose) + "-default-" + coderID
}

// StoreEffortDefault writes one default effort, read under CleanEffort, and
// answers the level it stored; whether its model takes it is the caller's
// check, it knows the pair. Empty takes the key out of the store, the CLI's
// own default, like StoreModelDefault.
func StoreEffortDefault(store *settings.Store, coderID string, purpose ModelPurpose, raw string) (string, error) {
	level, err := CleanEffort(raw)
	if err != nil {
		return "", err
	}
	if store == nil {
		return level, nil
	}
	key := EffortDefaultKey(coderID, purpose)
	if level == "" {
		store.Delete(key)
	} else {
		store.Set(key, level)
	}
	return level, nil
}

// EffortReading is EffortOrigin's whole answer, the effort beside
// ModelReading's model, read the same way.
type EffortReading struct {
	Effort     string
	From       ModelSource
	SameAsChat bool
}

func (r chainReading) effort() EffortReading {
	return EffortReading{Effort: r.value, From: r.from, SameAsChat: r.sameAsChat}
}

// EffortOrigin is the one reading of which effort a turn runs at, read on the
// levels its model is read on and paired with it: own is the trigger's model
// and effort a reaction hands in. An effort left empty follows the model, so
// a purpose that runs on the chat's model runs at the chat's effort, and one
// on a model of its own, picked on its own level, runs at that level's
// effort or at the CLI's own, never at a level picked for another model.
func EffortOrigin(kind RunKind, owner Summary, own Pick, defaults ModelDefaults) EffortReading {
	_, effort := pickLevels(kind, owner, defaults, own, true).resolve(kind)
	return effort.effort()
}

// DefaultEffortOrigin is what an effort select's empty entry stands for while
// its model select is empty too, the chain below the purpose's own level.
// With a model of its own picked there, the empty entry stands for the CLI's
// own level instead, see EffortOrigin.
func DefaultEffortOrigin(kind RunKind, owner Summary, defaults ModelDefaults) EffortReading {
	_, effort := pickLevels(kind, owner, defaults, Pick{}, false).resolve(kind)
	return effort.effort()
}

// EffortFor is EffortOrigin's effort alone, what a turn is started with.
func EffortFor(kind RunKind, owner Summary, own Pick, defaults ModelDefaults) string {
	return EffortOrigin(kind, owner, own, defaults).Effort
}

// DefaultEffortFor is DefaultEffortOrigin's effort alone.
func DefaultEffortFor(kind RunKind, owner Summary, defaults ModelDefaults) string {
	return DefaultEffortOrigin(kind, owner, defaults).Effort
}
