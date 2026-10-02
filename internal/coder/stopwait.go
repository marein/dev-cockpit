package coder

import (
	"strconv"
	"time"
)

// StopWait bounds how long a delete waits for a stopped coder to end. claude
// writes its exit record within about half a second of the hangup, the rest
// is room for a busy host.
const StopWait = 5 * time.Second

// stopPoll is how often the wait looks whether the process is gone.
const stopPoll = 50 * time.Millisecond

// StopAndWait stops a coder and waits, at most limit, until its pane process
// has ended. A delete of the session's records comes right after it: a CLI
// still exiting would write its exit lines into a file that is gone and
// create it again, and what it writes on the way out is read before the
// records go.
func (s *Manager) StopAndWait(rawID string, limit time.Duration) (string, error) {
	r, err := s.ResolveRunning(rawID)
	if err != nil {
		return "", err
	}
	name, err := s.Stop(rawID)
	if err != nil {
		return "", err
	}
	if pid, err := strconv.Atoi(r.PID); err == nil && pid > 0 {
		deadline := time.Now().Add(limit)
		for processAlive(pid) && time.Now().Before(deadline) {
			time.Sleep(stopPoll)
		}
	}
	return name, nil
}
