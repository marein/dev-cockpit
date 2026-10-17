package assistant

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/marein/dev-cockpit/internal/settings"
)

// A model is picked per purpose and never by the cockpit itself: the chat
// turns of an assistant, the checks of its steered jobs, the reactions of a
// trigger and the start of a coder session may each run on a model of their
// own, so a trigger that mostly answers NOTHING runs on a cheap one while the
// chat keeps a strong one. Behind a chat pick stands the coder's start
// default, a setting per coder, and behind everything the CLI's own default,
// which is what every turn ran on before a model could be picked: every
// fallback ends there. Behind a check pick stands the chat this assistant
// resolves to, read when the check starts; behind a trigger's own model
// stands the assistant's Triggers pick at the ring, else that same chat,
// read when the reaction starts. Same as chat is therefore stored as nothing
// at all, and a chat moved at the ring moves every later check and every
// later reaction that follows it. The three defaults of the Models tab,
// chat, check and trigger, are creation defaults, copied onto a new
// assistant's picks once and never read at run time.

// MaxModelRunes bounds a model name. The longest names the three CLIs take
// today are provider/model pairs of forty runes, so twice that is room for a
// longer one and still a name, not a paragraph.
const MaxModelRunes = 80

// modelPunctuation is what a model name may carry beyond letters and digits:
// the dot and the dash of a version, the slash of opencode's provider/model,
// the colon of a tag, the underscore, and the brackets claude puts around a
// context tier.
const modelPunctuation = "._/:-[]"

// CleanModel reads a model name the way every surface takes one: trimmed, at
// most MaxModelRunes, letters, digits and the punctuation above, no whitespace
// inside, and never a first rune of "-": the name travels into an argv behind
// a flag, and a value that starts with a dash is read as an option there, by
// every one of the three CLIs, which is the one shape the alphabet alone
// would let through. Empty is a value, it clears the choice. Anything else is
// refused with a sentence that says what a name may hold, because a refusal
// here is cheaper than a turn that fails on it.
func CleanModel(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", nil
	}
	if len([]rune(name)) > MaxModelRunes {
		return "", fmt.Errorf("A model name is at most %d characters long.", MaxModelRunes)
	}
	if strings.HasPrefix(name, "-") {
		return "", errors.New("A model name cannot start with a dash, a CLI would read it as an option.")
	}
	for _, r := range name {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune(modelPunctuation, r) {
			continue
		}
		return "", errors.New("A model name holds letters, digits and . _ / : - [ ] only, no spaces.")
	}
	return name, nil
}

// ModelPurpose names what a stored default is for.
type ModelPurpose string

const (
	ModelPurposeChat    ModelPurpose = "chat"
	ModelPurposeCheck   ModelPurpose = "check"
	ModelPurposeTrigger ModelPurpose = "trigger"
	ModelPurposeStart   ModelPurpose = "start"
)

// ModelPurposes are the four, in the order the settings pages show them.
var ModelPurposes = []ModelPurpose{ModelPurposeChat, ModelPurposeCheck, ModelPurposeTrigger, ModelPurposeStart}

// ModelDefaults are one coder's defaults per purpose, empty for the CLI's
// own. They are read where they are needed and never cached, so a change on
// the settings page reaches the next reading without a restart. Start is the
// one live link, behind a session's pick and behind an assistant's chat
// pick; the other three are creation defaults, Chat copied onto a new
// assistant's chat model, Check onto its check model and Trigger onto its
// trigger model, each only where it is set, and a run time reading never
// touches them again: an assistant made before a default was set keeps what
// it had, and a trigger carries a model only where somebody set one on it.
// The effort of every purpose is a default of the same kind beside its
// model, read and copied the same way, see EffortOrigin.
type ModelDefaults struct {
	Chat    string
	Check   string
	Trigger string
	Start   string

	ChatEffort    string
	CheckEffort   string
	TriggerEffort string
	StartEffort   string
}

// Of answers the default of one purpose.
func (d ModelDefaults) Of(purpose ModelPurpose) string {
	switch purpose {
	case ModelPurposeChat:
		return d.Chat
	case ModelPurposeCheck:
		return d.Check
	case ModelPurposeTrigger:
		return d.Trigger
	case ModelPurposeStart:
		return d.Start
	}
	return ""
}

// ModelDefaultKey is the settings key of one coder's default for one purpose,
// ModelAddedKey the key its remembered names stand under and EffortDefaultKey
// the key of the effort beside a default model. The nine keys a coder has
// are named here and nowhere else.
func ModelDefaultKey(coderID string, purpose ModelPurpose) string {
	return "model-" + string(purpose) + "-default-" + coderID
}

// ModelAddedKey is the settings key of the names a coder's repository was
// told to remember, one JSON array.
func ModelAddedKey(coderID string) string {
	return "model-added-" + coderID
}

// ModelDefaultsFor reads a coder's defaults out of the store. A nil store
// answers empty everywhere, the way the handler tests build a server, and a
// stored value CleanModel or CleanEffort refuses reads as empty rather than
// reaching an argv.
func ModelDefaultsFor(store *settings.Store, coderID string) ModelDefaults {
	if store == nil {
		return ModelDefaults{}
	}
	read := func(purpose ModelPurpose) string {
		name, err := CleanModel(store.Get(ModelDefaultKey(coderID, purpose)))
		if err != nil {
			return ""
		}
		return name
	}
	effort := func(purpose ModelPurpose) string {
		level, err := CleanEffort(store.Get(EffortDefaultKey(coderID, purpose)))
		if err != nil {
			return ""
		}
		return level
	}
	return ModelDefaults{
		Chat:          read(ModelPurposeChat),
		Check:         read(ModelPurposeCheck),
		Trigger:       read(ModelPurposeTrigger),
		Start:         read(ModelPurposeStart),
		ChatEffort:    effort(ModelPurposeChat),
		CheckEffort:   effort(ModelPurposeCheck),
		TriggerEffort: effort(ModelPurposeTrigger),
		StartEffort:   effort(ModelPurposeStart),
	}
}

// StoreModelDefault writes one default, cleaned by the one rule, and answers
// the name it stored. Empty is a value, the CLI's own default, and it takes
// the key out of the store instead of storing an empty string, so the default
// reads as never answered.
func StoreModelDefault(store *settings.Store, coderID string, purpose ModelPurpose, raw string) (string, error) {
	name, err := CleanModel(raw)
	if err != nil {
		return "", err
	}
	if store == nil {
		return name, nil
	}
	key := ModelDefaultKey(coderID, purpose)
	if name == "" {
		store.Delete(key)
	} else {
		store.Set(key, name)
	}
	return name, nil
}

// ModelSource says where a resolution ended: at a pick somebody made on the
// ring or a trigger, at the coder's own start default on its settings page,
// or at nothing, the CLI's own default. It names the level the model really
// stands on, whatever purpose asked for it: a check that landed on the ring's
// chat pick reports the ring. The Models tab is no level here, what it holds
// is copied onto a new assistant's picks and reads as a pick from then on.
type ModelSource string

const (
	ModelFromPick  ModelSource = "pick"
	ModelFromCoder ModelSource = "coder"
	ModelFromCLI   ModelSource = "cli"
)

// ModelReading is ModelOrigin's whole answer: the model a turn runs on, the
// level it really stands on, and whether a check or a reaction took it as
// the chat's own reading. SameAsChat is what a check without a check pick
// carries, and a reaction of a trigger without a model of its own whose
// owner picked no Triggers model either, whichever level the chat itself
// resolved to, so a surface can say "same as chat, ring" instead of naming
// the ring alone as if the check had been picked there.
type ModelReading struct {
	Model      string
	From       ModelSource
	SameAsChat bool
}

// ModelOrigin is the one reading of which model a turn runs on, with where
// the answer came from beside it. The assistant inherits from the coder or
// overrides it, and a check and a reaction inherit from the chat: a turn
// takes its own pick, the assistant's chat or check model or the trigger's
// model handed in as trigger, else DefaultModelOrigin's chain below that
// pick. A trigger's model and an assistant's check and trigger models are
// read when the turn starts, so a chat moved at the ring later moves every
// check and every reaction that follows the chat with it.
func ModelOrigin(kind RunKind, owner Summary, trigger string, defaults ModelDefaults) ModelReading {
	model, _ := pickLevels(kind, owner, defaults, ModelPick{Model: trigger}, true).resolve(kind)
	return model.model()
}

// DefaultModelOrigin is the chain below a pick, what a purpose runs on when
// nothing is picked for it, and what a pick's empty entry stands for. A chat
// turn runs on the coder's start default on its settings page, else empty,
// the CLI's own default. A check
// runs on the chat's own reading, ring pick included, and on nothing else. A
// reaction of a trigger without a model of its own runs on the assistant's
// Triggers pick at the ring, a pick like the trigger's own and never the
// chat's reading, else on the chat's reading the way a check does: Same as
// chat means what this assistant's chat resolves to when the check or the
// reaction starts. The defaults of the Models tab never stand in this
// chain, the chat default included, they are copied onto a new assistant
// once, see Service.create.
func DefaultModelOrigin(kind RunKind, owner Summary, defaults ModelDefaults) ModelReading {
	model, _ := pickLevels(kind, owner, defaults, ModelPick{}, false).resolve(kind)
	return model.model()
}

// ModelFor is ModelOrigin's model alone, what a turn is started with.
func ModelFor(kind RunKind, owner Summary, trigger string, defaults ModelDefaults) string {
	return ModelOrigin(kind, owner, trigger, defaults).Model
}

// DefaultModelFor is DefaultModelOrigin's model alone, what a pick's empty
// entry stands for.
func DefaultModelFor(kind RunKind, owner Summary, defaults ModelDefaults) string {
	return DefaultModelOrigin(kind, owner, defaults).Model
}

// pickLevel is one level of the chain a turn reads its model and its effort
// on: the trigger's own, the assistant's check or Triggers pick, its chat
// pick, the coder's start default. chat marks the levels of the chat's own
// chain, which a check or a reaction reads as Same as chat.
type pickLevel struct {
	model, effort string
	from          ModelSource
	chat          bool
}

type pickChain []pickLevel

// pickLevels are the levels of one purpose, the narrowest first. own is the
// trigger's pick of a reaction; withOwn false leaves the purpose's own level
// out, which is the chain below it, what an empty entry stands for.
func pickLevels(kind RunKind, owner Summary, defaults ModelDefaults, own ModelPick, withOwn bool) pickChain {
	chain := pickChain{
		{model: owner.Model, effort: owner.Effort, from: ModelFromPick, chat: true},
		{model: defaults.Start, effort: defaults.StartEffort, from: ModelFromCoder, chat: true},
	}
	switch kind {
	case RunCheck:
		chain = append(pickChain{{model: owner.CheckModel, effort: owner.CheckEffort, from: ModelFromPick}}, chain...)
	case RunReaction:
		chain = append(pickChain{
			{model: own.Model, effort: own.Effort, from: ModelFromPick},
			{model: owner.TriggerModel, effort: owner.TriggerEffort, from: ModelFromPick},
		}, chain...)
	}
	if !withOwn {
		chain = chain[1:]
	}
	return chain
}

// chainReading is what the chain answers for one value, before it is named
// a model or an effort.
type chainReading struct {
	value      string
	from       ModelSource
	sameAsChat bool
}

func (r chainReading) model() ModelReading {
	return ModelReading{Model: r.value, From: r.from, SameAsChat: r.sameAsChat}
}

// resolve reads the model and the effort of one purpose together. The model
// is the narrowest level that names one, else the CLI's. The effort is the
// narrowest level that names one, but never one below the level the model
// came from: a level is read as a pair, so a purpose on a model of its own
// takes an effort only from its own level and otherwise the CLI's, and never
// a level somebody picked for another model, which that model may not take.
// An effort picked without a model runs on whatever model stands below it,
// the way the model it does not name would. A check and a reaction whose
// reading comes from the chat's own levels, or from nothing, read as Same as
// chat.
func (c pickChain) resolve(kind RunKind) (model, effort chainReading) {
	same := func(level int) bool {
		return kind != RunChat && (level == len(c) || c[level].chat)
	}
	stop := len(c)
	for i, level := range c {
		if level.model != "" {
			stop = i
			break
		}
	}
	model = chainReading{from: ModelFromCLI, sameAsChat: same(stop)}
	if stop < len(c) {
		model = chainReading{value: c[stop].model, from: c[stop].from, sameAsChat: same(stop)}
	}
	effort = chainReading{from: ModelFromCLI, sameAsChat: same(stop)}
	for i := 0; i < len(c) && i <= stop; i++ {
		if c[i].effort != "" {
			return model, chainReading{value: c[i].effort, from: c[i].from, sameAsChat: same(i)}
		}
	}
	return model, effort
}
