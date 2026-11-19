package pluginhost

import (
	"fmt"
	"log"
	"slices"

	"github.com/marein/dev-cockpit/internal/statefile"
	"github.com/marein/dev-cockpit/plugin"
)

// optional is one compiled plugin wired optional. Every switch on configures
// it with a fresh Serve, every switch off ends that Serve's context and takes
// what it added away. problem is why the start failed to load it although it
// is switched on. title is the page title of its last load, which its page
// keeps while it is off.
type optional struct {
	order   int
	id      string
	plugin  plugin.ServePlugin
	serve   *Serve
	problem string
	title   string
}

func alwaysRunning() bool { return true }

// optionalOf answers the optional compiled plugin of an id, nil for none.
func (h *Host) optionalOf(id string) *optional {
	for _, o := range h.optional {
		if o.id == id {
			return o
		}
	}
	return nil
}

// switchedOn reads which optional compiled plugins are switched on.
func (h *Host) switchedOn() map[string]bool {
	var st hostState
	statefile.Load(enabledPath(h.cfg.StateDir), &st)
	on := map[string]bool{}
	for _, id := range st.On {
		on[id] = true
	}
	return on
}

func (h *Host) writeOn(id string, on bool) error {
	return h.changeState(func(st *hostState) {
		st.On = slices.DeleteFunc(st.On, func(listed string) bool { return listed == id })
		if on {
			st.On = append(st.On, id)
			slices.Sort(st.On)
		}
	})
}

// configureOptional runs ConfigureServe with a fresh Serve. An error or a
// panic there answers an error and ends the Serve's context.
func (h *Host) configureOptional(o *optional) (s *Serve, err error) {
	s, err = newCompiledServe(o.id, o.order, h.cfg.ProjectsDir, h.cfg.StateDir, h.projects, h.cfg.Assistants)
	if err != nil {
		return nil, err
	}
	defer func() {
		if r := recover(); r != nil {
			s.end()
			s, err = nil, fmt.Errorf("It panicked: %v", r)
		}
	}()
	if err := o.plugin.ConfigureServe(s); err != nil {
		s.end()
		return nil, err
	}
	s.sealed = true
	s.live = &liveServe{generation: h.gen.Add(1), running: alwaysRunning}
	return s, nil
}

// startOptional configures every switched on optional plugin, under ops and
// before the settings store is read, so its settings and memory apply with
// those of the plugins always on. One that fails stays off for this run and
// says why.
func (h *Host) startOptional() {
	on := h.switchedOn()
	for _, o := range h.optional {
		if !on[o.id] {
			continue
		}
		s, err := h.configureOptional(o)
		h.mu.Lock()
		if err != nil {
			o.problem = cut(err.Error())
			log.Printf("plugin %s failed to configure: %v", o.id, err)
		} else {
			o.serve, o.title = s, pageTitle(s)
		}
		h.mu.Unlock()
	}
}

// prepareOptional runs Prepare for every optional plugin startOptional
// configured. It waits for Start, Scan has no hooks yet.
func (h *Host) prepareOptional(hooks Hooks) {
	for _, o := range h.optional {
		h.mu.Lock()
		s := o.serve
		h.mu.Unlock()
		if s == nil || hooks.Prepare == nil {
			continue
		}
		if err := hooks.Prepare(s); err != nil {
			s.end()
			h.mu.Lock()
			o.serve, o.problem = nil, err.Error()
			h.mu.Unlock()
		}
	}
}

// switchOptional switches one optional plugin, under ops. Off ends its
// Serve and drops its routes, assets, mounts and events, what its settings
// and memory wrote stays. On configures it fresh, prepares its assets and
// applies its settings and memory before it joins the set. A failure writes
// the switch off and refuses with why. One already on stays as it is.
func (h *Host) switchOptional(o *optional, on bool) error {
	if !on {
		if err := h.writeOn(o.id, false); err != nil {
			return err
		}
		h.endOptional(o)
		h.changed()
		return nil
	}
	h.mu.Lock()
	hooks, started, already := h.hooks, h.started, o.serve != nil
	h.mu.Unlock()
	if already {
		return nil
	}
	h.endOptional(o)
	s, err := h.configureOptional(o)
	if err == nil && started && hooks.Prepare != nil {
		if err = hooks.Prepare(s); err != nil {
			s.end()
		}
	}
	if err != nil {
		if werr := h.writeOn(o.id, false); werr != nil {
			log.Printf("plugin %s: its switch could not be set off after it failed: %v", o.id, werr)
		}
		h.changed()
		return refuse("%s could not be switched on. %s", o.id, cut(err.Error()))
	}
	if err := h.writeOn(o.id, true); err != nil {
		s.end()
		if hooks.Release != nil {
			hooks.Release(o.id)
		}
		return err
	}
	if started && hooks.Loaded != nil {
		hooks.Loaded(s)
	}
	h.mu.Lock()
	o.serve, o.problem, o.title = s, "", pageTitle(s)
	h.mu.Unlock()
	h.changed()
	return nil
}

// endOptional ends what one optional plugin's last switch on started.
func (h *Host) endOptional(o *optional) {
	h.mu.Lock()
	s := o.serve
	o.serve, o.problem = nil, ""
	hooks := h.hooks
	h.mu.Unlock()
	if s == nil {
		return
	}
	s.end()
	if hooks.Release != nil {
		hooks.Release(o.id)
	}
}

// Optional answers the Serves of the switched on optional plugins, in wiring
// order.
func (h *Host) Optional() []*Serve {
	h.mu.Lock()
	defer h.mu.Unlock()
	var serves []*Serve
	for _, o := range h.optional {
		if o.serve != nil {
			serves = append(serves, o.serve)
		}
	}
	return serves
}

// compiledStatuses answers the compiled plugins for the Plugins page, in
// wiring order, under mu.
func (h *Host) compiledStatuses() []Status {
	var statuses []Status
	for _, s := range h.cfg.Compiled {
		statuses = append(statuses, Status{ID: s.ID(), Name: s.ID(), PageTitle: pageTitle(s), State: StateRunning, BuiltIn: true, order: s.order})
	}
	for _, o := range h.optional {
		statuses = append(statuses, h.optionalStatus(o))
	}
	slices.SortStableFunc(statuses, func(a, b Status) int { return a.order - b.order })
	return statuses
}

func (h *Host) optionalStatus(o *optional) Status {
	st := Status{ID: o.id, Name: o.id, PageTitle: o.title, BuiltIn: true, Optional: true, order: o.order}
	switch {
	case o.serve != nil:
		st.State = StateRunning
	case o.problem != "":
		st.State, st.Reason = StateBroken, o.problem
	default:
		st.State = StateDisabled
	}
	return st
}

// compiledStatus answers one compiled plugin's status, under mu, false for
// an id no compiled plugin carries.
func (h *Host) compiledStatus(id string) (Status, bool) {
	if o := h.optionalOf(id); o != nil {
		return h.optionalStatus(o), true
	}
	for _, s := range h.cfg.Compiled {
		if s.ID() == id {
			return Status{ID: id, Name: id, PageTitle: pageTitle(s), State: StateRunning, BuiltIn: true, order: s.order}, true
		}
	}
	return Status{}, false
}

func pageTitle(s *Serve) string {
	if p := s.Page(); p != nil {
		return p.Title
	}
	return ""
}
