package assistant

import (
	"errors"
	"fmt"
	"strings"
)

// Release calls a job off. It stays visible in that state, so the user can see
// what happened to it instead of finding it gone. A check that is running on
// the job right now is killed: release takes the actor away, and a dead check
// writes nothing and costs nothing more.
//
// Only a job that still steers is called off. One that is over keeps the state
// it ended in and is refused like a terminal nobody steers, because stopping a
// coder releases its job and a done job would otherwise read stopped. The
// check runs again inside the update, so a job that closes in between keeps
// its state too.
//
// by is the assistant asking, empty for the user. The user may call any job
// off, they own the machine; an assistant only its own, because releasing
// somebody else's job is taking their coder away by the back door.
func (w *Watcher) Release(terminal, by string) error {
	job, ok := w.jobs.Find(strings.TrimSpace(terminal))
	if !ok {
		return errors.New("No job steers that terminal.")
	}
	if !job.State.Open() {
		return closedJobError(job)
	}
	if by != "" && job.Owner != by {
		return fmt.Errorf("%s steers that coder, not you. Leave it alone or let the user decide.", w.ownerName(job.Owner))
	}
	fresh, ok := w.jobs.Of(job.Owner).Update(job.Terminal, func(job *Job) bool {
		if !job.State.Open() {
			return false
		}
		job.State = JobStopped
		job.UpdatedAt = w.now().UTC()
		return true
	})
	if !ok && fresh.Terminal != "" {
		return closedJobError(fresh)
	}
	if !ok {
		return errors.New("No job steers that terminal.")
	}
	w.service.killChecks(fresh.Terminal)
	w.service.changed()
	w.announceChange(fresh.Project)
	return nil
}

func closedJobError(job Job) error {
	return fmt.Errorf("Nothing steers that coder any more, its job is %s.", job.State)
}
