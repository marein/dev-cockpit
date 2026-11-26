package terminal

import (
	"time"

	"github.com/marein/dev-cockpit/internal/tmux"
)

// BellCooldown coalesces bell bursts: one event rings one bell, but nothing
// guarantees a redraw does not repeat it right after.
const BellCooldown = 2 * time.Second

// Watch is one running watcher. Stop ends it, and Ended says it is over, by
// a stop or because the session it read went away.
type Watch struct {
	stop  chan struct{}
	ended chan struct{}
}

func (w *Watch) Stop() { close(w.stop) }

func (w *Watch) Ended() bool {
	select {
	case <-w.ended:
		return true
	default:
		return false
	}
}

// RunWatch reconciles a set of live tmux sessions with one watcher goroutine
// each: alive lists the current tmux session names mapped to their public
// identifiers, start spawns a watcher. A watcher that ended on its own is
// started again while its name is alive: a session that was stopped and came
// back under the same name between two ticks never left the set, and would
// otherwise stay unwatched for the rest of its life. A send on changed
// reconciles at once instead of at the next tick, nil leaves it to the
// ticks. Blocks; run it in a goroutine.
func RunWatch(interval time.Duration, changed <-chan struct{}, alive func() map[string]string, start func(tmuxName, id string) (*Watch, error)) {
	watched := map[string]*Watch{}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
		case <-changed:
		}
		live := alive()
		for name, watch := range watched {
			if _, ok := live[name]; !ok {
				watch.Stop()
				delete(watched, name)
			} else if watch.Ended() {
				delete(watched, name)
			}
		}
		for name, id := range live {
			if _, ok := watched[name]; ok {
				continue
			}
			watch, err := start(name, id)
			if err != nil {
				continue
			}
			watched[name] = watch
		}
	}
}

// WatchOutput attaches a read-only control-mode client to a tmux session and
// feeds every output chunk to scan: the OSC-filtered bytes plus the payloads
// of completed OSC sequences. It never resizes, so pane geometry stays
// untouched. The returned watch stops it.
//
// Only output from after the attach is reported. The connect hands over the
// pane's accumulated history as one flood of chunks, and a watcher that
// scanned it would report a burst of movement for a session where nothing is
// happening; every reattach, a serve restart above running sessions included,
// would repeat it. The snapshot moves the offset past all of that.
func WatchOutput(tmuxName string, scan func(out []byte, marks []string)) (*Watch, error) {
	ctl, err := tmux.StartControl(tmuxName)
	if err != nil {
		return nil, err
	}
	watch := &Watch{stop: make(chan struct{}), ended: make(chan struct{})}
	go func() {
		defer close(watch.ended)
		defer ctl.Close()
		var filter tmux.OSCFilter
		var offset int64
		if _, fresh, err := ctl.Snapshot(); err == nil {
			offset = fresh
		}
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
			case <-watch.stop:
				return
			case <-updated:
			}
		}
	}()
	return watch, nil
}
