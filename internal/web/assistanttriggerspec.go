package web

import (
	"fmt"
	"slices"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
)

// assistantTriggerSpec reads a trigger's fields out of the form both surfaces
// post. A field the request does not carry is one nobody named, which on a
// create takes the default and on an edit leaves what stands, so the two ways
// in share one reading and one meaning. source is what the trigger reacts to
// when the request does not say (an edit never moves it), and stood are the
// terminals it already names: those keep the name they were stored with
// instead of being looked up again, a coder that stopped is still a terminal
// this trigger waits for.
func (s *Server) assistantTriggerSpec(c *gin.Context, source string, stood []string) (assistant.TriggerSpec, error) {
	spec := assistant.TriggerSpec{
		Event:    c.PostForm("event"),
		Spec:     c.PostForm("spec"),
		Timezone: c.PostForm("timezone"),
		Task:     c.PostForm("task"),
	}
	// The model and the effort are optional and an empty one is a value, the
	// assistant default: the page's selects post on every save and `default`
	// posts the empty field, while a request without one leaves what stands.
	spec.Model, spec.ModelSet = c.GetPostForm("model")
	spec.Effort, spec.EffortSet = c.GetPostForm("effort")
	// The name is optional and an empty one is a value: the page posts the
	// field on every save, so clearing it is saying so, while a request
	// without the field named nothing and leaves what stands.
	if raw, ok := c.GetPostForm("name"); ok {
		spec.Name, spec.NameSet = raw, true
	}
	if kind, err := assistant.ParseEventOption(spec.Event); err == nil {
		source = kind.Source
	}
	// Several terminals fire it, and the mode says whether any of them does or
	// every one of them has to, which is a barrier. The page's select posts the
	// mode, `--all` posts it too.
	if raw, ok := c.GetPostForm("mode"); ok {
		spec.All, spec.AllSet = strings.EqualFold(strings.TrimSpace(raw), "all"), true
	}
	if raw, ok := c.GetPostFormArray("once"); ok {
		spec.OnceSet = true
		spec.Once = slices.ContainsFunc(raw, func(v string) bool { return strings.TrimSpace(v) != "" })
	}
	if raw, ok := c.GetPostFormArray("terminal"); ok {
		spec.TargetsSet = true
		for _, one := range raw {
			terminal := strings.TrimSpace(one)
			if terminal == "" {
				continue
			}
			target := assistant.TriggerTarget{Terminal: terminal}
			// A coder target is named here, where the running coders are; a job
			// target takes its name from the job, which the reactor reads.
			if source == assistant.EventCoder && !slices.Contains(stood, terminal) {
				running, err := s.assistantSteerTarget(terminal)
				if err != nil {
					return spec, err
				}
				target.Name = running.Name
			}
			spec.Targets = append(spec.Targets, target)
		}
	}
	// The expiry is a span either way in, and the two ways write it
	// differently: a command writes one word, `8h` or `never`, while the form
	// asks for a number with the unit beside it and says none in that same
	// select. So the unit is what the reading starts from, the number carries
	// it where there is one, and both ways end in one span and one parser.
	until := strings.TrimSpace(c.PostForm("until"))
	if unit := strings.TrimSpace(c.PostForm("untilUnit")); strings.EqualFold(unit, triggerNoExpiry) {
		// The number is disabled while that entry stands, so whatever it
		// carried says nothing: the select is the answer.
		until = triggerNoExpiry
	} else if until != "" {
		until += unit
	}
	if until != "" {
		if strings.EqualFold(until, triggerNoExpiry) {
			spec.Never = true
		} else {
			span, err := parseSpan(until)
			if err != nil {
				return spec, fmt.Errorf("The expiry %q cannot be read: a span like 8h, 90m or 2d, or never.", until)
			}
			spec.Until = span
		}
	}
	if raw := strings.TrimSpace(c.PostForm("batch")); raw != "" {
		batch, err := parseSpan(raw)
		if err != nil {
			return spec, fmt.Errorf("The batch window %q cannot be read: a span like 30s or 2m, or 0.", raw)
		}
		spec.Batch, spec.BatchSet = batch, true
	}
	return spec, nil
}
