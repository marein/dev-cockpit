package assistant

import (
	"errors"
	"fmt"
)

// Pick is a model and the effort beside it, each empty where nothing
// narrower is picked: what a trigger names of its own, and what a turn ran
// on.
type Pick struct {
	Model, Effort string
}

// ModelChoice is what a save of the ring's menu names: the chat, the check
// and the trigger model and the effort beside each, every one with whether
// the request carried the field at all, so a field nobody named leaves what
// stands and an empty one clears it, the way a trigger's spec reads its
// fields.
type ModelChoice struct {
	Chat, Check, Trigger          string
	ChatSet, CheckSet, TriggerSet bool

	ChatEffort, CheckEffort, TriggerEffort          string
	ChatEffortSet, CheckEffortSet, TriggerEffortSet bool
}

// SetModels stores which models an assistant's own turns run on and at which
// effort. Every name goes through CleanModel and every level through
// CleanEffort, and every purpose whose model and effort the save moves is
// checked as the pair it now runs on, by the coder's own rule, wherever its
// effort is a pick at the ring, the place that fixes it: a refusal names the
// purpose and changes nothing. A pair whose effort comes from the coder's
// start default is the settings page's, where it was checked. It answers the
// instance as it stands afterwards, which is what the surfaces word their
// confirmation from and what the save's answer carries as the picks with
// their stamp. The fresh picks go out on the assistant's own stream as one
// models frame, so every open page of it moves its ring's selects without a
// reload, whether the pick was set on that page, on another tab or from the
// CLI. A save that moves nothing writes, stamps and publishes nothing. One
// that moves a pick stamps UpdatedAt, because that stamp is what orders the
// models frames.
func (s *Service) SetModels(id string, choice ModelChoice) (Instance, error) {
	current, err := s.Get(id)
	if err != nil {
		return Instance{}, err
	}
	next := current.Summary
	for _, pick := range []struct {
		set    bool
		raw    string
		effort bool
		field  *string
	}{
		{choice.ChatSet, choice.Chat, false, &next.Model},
		{choice.CheckSet, choice.Check, false, &next.CheckModel},
		{choice.TriggerSet, choice.Trigger, false, &next.TriggerModel},
		{choice.ChatEffortSet, choice.ChatEffort, true, &next.Effort},
		{choice.CheckEffortSet, choice.CheckEffort, true, &next.CheckEffort},
		{choice.TriggerEffortSet, choice.TriggerEffort, true, &next.TriggerEffort},
	} {
		if !pick.set {
			continue
		}
		clean := CleanModel
		if pick.effort {
			clean = CleanEffort
		}
		value, err := clean(pick.raw)
		if err != nil {
			return Instance{}, err
		}
		*pick.field = value
	}
	if err := s.checkPairs(current.Summary, next); err != nil {
		return Instance{}, err
	}

	s.mu.Lock()
	c, ok := s.store.Load(id)
	if !ok {
		s.mu.Unlock()
		return Instance{}, errors.New("Assistant not found.")
	}
	was := c.Summary
	c.Model, c.CheckModel, c.TriggerModel = next.Model, next.CheckModel, next.TriggerModel
	c.Effort, c.CheckEffort, c.TriggerEffort = next.Effort, next.CheckEffort, next.TriggerEffort
	if c.Summary == was {
		s.mu.Unlock()
		return c, nil
	}
	c.UpdatedAt = s.now().UTC()
	s.store.Save(c)
	s.mu.Unlock()

	frame := c.ModelPicks()
	s.hub.publish(c.ID, StreamEvent{Kind: FrameModels, Models: &frame})
	s.changed()
	return c, nil
}

// checkPairs is CheckPicks over this assistant's coder.
func (s *Service) checkPairs(was, next Summary) error {
	co, ok := s.coder(next.CoderID)
	if !ok {
		return nil
	}
	return CheckPicks(was, next, co.ModelDefaults(), co.checkEffort)
}

// CheckPicks checks the purposes moving from was to next, the chat, the
// checks and the reactions of a trigger without a pick of its own, each as
// the model and the effort it would then run on, where that effort is a pick
// of next and not the coder's start default, whose page checks it. The
// refusal names the purpose. The ring's save and the creation defaults of the
// Models tab, which are such picks once copied, are checked by it alike.
func CheckPicks(was, next Summary, defaults ModelDefaults, check func(model, level string) error) error {
	for _, purpose := range []struct {
		kind  RunKind
		label string
	}{{RunChat, "Chat"}, {RunCheck, "Checks"}, {RunReaction, "Triggers"}} {
		model, effort := pickLevels(purpose.kind, next, defaults, Pick{}, true).resolve(purpose.kind)
		before, beforeEffort := pickLevels(purpose.kind, was, defaults, Pick{}, true).resolve(purpose.kind)
		if model == before && effort == beforeEffort || effort.from != ModelFromPick || effort.value == "" {
			continue
		}
		if err := check(model.value, effort.value); err != nil {
			return fmt.Errorf("%s: %s", purpose.label, err.Error())
		}
	}
	return nil
}

// checkOwnEffort is the owner check of a trigger together with the check of
// its own effort, as the pair it runs on: the trigger's model, else the one
// its owner resolves to for triggers. A trigger without an effort of its own
// runs at what its model's level gives it, which is checked where that was
// picked. An owner that is gone refuses the trigger like it always did.
func (s *Service) checkOwnEffort(trigger Trigger) error {
	c, err := s.Get(trigger.Owner)
	if err != nil || trigger.Effort == "" {
		return err
	}
	co, ok := s.coder(c.CoderID)
	if !ok {
		return nil
	}
	return co.checkEffort(ModelFor(RunReaction, c.Summary, trigger.Model, co.ModelDefaults()), trigger.Effort)
}
