package assistant

import (
	"slices"
	"strings"
)

// TerminalMoved is what a terminal that took another id while it stays does
// to the arrangements made about it: a codex coder started without a prompt
// runs under the key the cockpit minted until its first message, and under
// the id codex gave it from then on, which its signals carry and its resume
// comes back under. A job steered under the key and every trigger waiting
// for it move over, so the next signal finds them the way it finds those of
// a coder that had its id from the start. One already standing under the new
// id keeps it.
func (w *Watcher) TerminalMoved(from, to string) {
	from, to = strings.TrimSpace(from), strings.TrimSpace(to)
	if from == "" || to == "" || from == to {
		return
	}
	moved := false
	project := ""
	for _, owner := range w.jobs.Owners() {
		if job, ok := w.jobs.Of(owner).move(from, to); ok {
			moved, project = true, job.Project
		}
	}
	if w.service.events != nil && w.service.events.terminalMoved(from, to) {
		moved = true
	}
	if !moved {
		return
	}
	w.service.changed()
	if project != "" {
		w.announceChange(project)
	}
}

func (s *JobStore) move(from, to string) (Job, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.load()
	if slices.ContainsFunc(list, func(j Job) bool { return j.Terminal == to }) {
		return Job{}, false
	}
	at := slices.IndexFunc(list, func(j Job) bool { return j.Terminal == from })
	if at < 0 {
		return Job{}, false
	}
	list[at].Terminal = to
	s.write(list)
	return list[at], true
}

// terminalMoved runs inside a coder's snapshot, see coder.Manager.OnMoved,
// so it waits on no lock of the reactor's, only on the store's own around
// each write, the one every write of a trigger takes.
func (r *Reactor) terminalMoved(from, to string) bool {
	moved := false
	for _, trigger := range r.triggers.All() {
		if !trigger.holds(from) || trigger.holds(to) {
			continue
		}
		if _, ok := r.triggers.Of(trigger.Owner).Update(trigger.ID, func(s *Trigger) bool {
			return s.mark(from, func(t *TriggerTarget) { t.Terminal = to })
		}); ok {
			moved = true
		}
	}
	return moved
}
