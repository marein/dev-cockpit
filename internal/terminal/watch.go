package terminal

import (
	"time"

	"github.com/local/dev-cockpit/internal/tmux"
)

// BellCooldown coalesces bell bursts: one event rings one bell, but nothing
// guarantees a redraw does not repeat it right after.
const BellCooldown = 2 * time.Second

// RunWatch reconciles a set of live tmux sessions with one watcher goroutine
// each: alive lists the current tmux session names mapped to their public
// identifiers, start spawns a watcher and returns its stop channel. Blocks;
// run it in a goroutine.
func RunWatch(interval time.Duration, alive func() map[string]string, start func(tmuxName, id string) (chan struct{}, error)) {
	watched := map[string]chan struct{}{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		live := alive()
		for name, stop := range watched {
			if _, ok := live[name]; !ok {
				close(stop)
				delete(watched, name)
			}
		}
		for name, id := range live {
			if _, ok := watched[name]; ok {
				continue
			}
			stop, err := start(name, id)
			if err != nil {
				continue
			}
			watched[name] = stop
		}
	}
}

// WatchOutput attaches a read-only control-mode client to a tmux session and
// feeds every output chunk to scan: the OSC-filtered bytes plus the payloads
// of completed OSC sequences. It never resizes, so pane geometry stays
// untouched. The returned channel stops the watcher when closed.
func WatchOutput(tmuxName string, scan func(out []byte, marks []string)) (chan struct{}, error) {
	ctl, err := tmux.StartControl(tmuxName)
	if err != nil {
		return nil, err
	}
	stop := make(chan struct{})
	go func() {
		defer ctl.Close()
		var filter tmux.OSCFilter
		var offset int64
		for {
			updated := ctl.Updated()
			if ctl.Exited() {
				return
			}
			data, next, reset := ctl.Delta(offset)
			if reset {
				if _, fresh, err := ctl.Snapshot(); err == nil {
					offset = fresh
					filter.Reset()
				}
				continue
			}
			if len(data) > 0 {
				offset = next
				out, marks := filter.FilterMarks(data)
				scan(out, marks)
			}
			select {
			case <-stop:
				return
			case <-updated:
			}
		}
	}()
	return stop, nil
}
