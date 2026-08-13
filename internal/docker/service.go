package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"time"
)

// HostSettingKey is the settings store key naming the docker host. An empty
// value means not set, the resolution then falls through to the environment,
// the docker context and the known socket paths.
const HostSettingKey = "docker-host"

const (
	// retryInterval paces the attempts while no daemon answers. Resolving
	// and pinging cost almost nothing, so a daemon that comes up is noticed
	// within moments without anything polling hard.
	retryInterval = 15 * time.Second
	pingTimeout   = 3 * time.Second
	listTimeout   = 15 * time.Second
	// debounceDelay coalesces an event burst, a compose up starts many
	// containers back to back, into one list call.
	debounceDelay = 300 * time.Millisecond
)

// Service keeps the one docker connection of the whole cockpit: it lists the
// containers once, then follows the daemon's event stream and refreshes the
// cached list when something moved. Every surface reads the cache, nothing
// asks the daemon per request or per project.
type Service struct {
	hostSetting func() string

	mu       sync.Mutex
	state    State
	onChange func()

	kick chan struct{}

	compose composeState
	// runs is the register of compose runs, the part of this service that
	// outlives the process.
	runs    *runStore
	cliOnce sync.Once
	cli     bool
}

// NewService prepares the watcher. hostSetting reads the docker-host setting
// on every round, so a changed setting reaches a running watcher; stateDir is
// where the compose runs are registered, so they can be picked up again after
// a restart.
func NewService(stateDir string, hostSetting func() string) *Service {
	return &Service{hostSetting: hostSetting, kick: make(chan struct{}, 1), runs: newRunStore(stateDir)}
}

// OnChange registers the one callback fired after the cached state moved,
// the web layer publishes its docker event from it. Set it before Run.
func (s *Service) OnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// State answers the current reading of the cache. Nil-receiver-safe like
// the editor intelligence service: web tests build a Server without the
// daemon wiring, and for them the daemon simply is not there.
func (s *Service) State() State {
	if s == nil {
		return State{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.state
	out.Containers = append([]Container(nil), s.state.Containers...)
	return out
}

// Kick makes the watcher drop its connection and start over, which re-reads
// the setting. The settings handler calls it after a save.
func (s *Service) Kick() {
	select {
	case s.kick <- struct{}{}:
	default:
	}
}

// Client answers a client for the host the watcher is connected to. While no
// daemon is reachable it answers the error the action handlers surface.
func (s *Service) Client() (*Client, error) {
	s.mu.Lock()
	host := ""
	if s.state.Available {
		host = s.state.Host
	}
	s.mu.Unlock()
	if host == "" {
		return nil, errors.New("no reachable Docker host")
	}
	return NewClient(host)
}

// Run is the watcher loop. It never returns before ctx ends; a machine
// without a daemon just cycles quietly through the retry pause.
func (s *Service) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		s.runOnce(ctx)
	}
}

// runOnce is one connection's life: resolve, ping, subscribe to the event
// stream, list, then relist behind a short debounce whenever the stream
// reports a change. It returns when the stream ends, the host setting was
// kicked, or nothing was reachable, and the caller starts the next round.
func (s *Service) runOnce(ctx context.Context) {
	host := Resolve(s.hostSetting())
	if host == "" {
		s.setState(State{})
		s.pause(ctx, retryInterval)
		return
	}
	client, err := NewClient(host)
	if err != nil {
		s.setState(State{})
		s.pause(ctx, retryInterval)
		return
	}
	pingCtx, cancelPing := context.WithTimeout(ctx, pingTimeout)
	err = client.Ping(pingCtx)
	cancelPing()
	if err != nil {
		s.setState(State{})
		s.pause(ctx, retryInterval)
		return
	}
	// The stream opens before the first list, so a change landing between
	// the two is caught by the stream instead of falling into a gap.
	streamCtx, cancelStream := context.WithCancel(ctx)
	defer cancelStream()
	events, err := client.Events(streamCtx)
	if err != nil {
		s.setState(State{})
		s.pause(ctx, retryInterval)
		return
	}
	defer events.Close()
	if !s.relist(ctx, client, host) {
		s.setState(State{})
		s.pause(ctx, retryInterval)
		return
	}
	changes := make(chan struct{}, 1)
	streamEnded := make(chan struct{})
	go func() {
		defer close(streamEnded)
		scanner := bufio.NewScanner(events)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			if relevantEvent(scanner.Bytes()) {
				select {
				case changes <- struct{}{}:
				default:
				}
			}
		}
	}()
	debounce := time.NewTimer(debounceDelay)
	debounce.Stop()
	defer debounce.Stop()
	pending := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.kick:
			return
		case <-changes:
			if !pending {
				pending = true
				debounce.Reset(debounceDelay)
			}
		case <-debounce.C:
			pending = false
			if !s.relist(ctx, client, host) {
				return
			}
		case <-streamEnded:
			// A stream that ends right after connecting must not spin, the
			// next round waits a moment before it reconnects.
			s.pause(ctx, time.Second)
			return
		}
	}
}

func (s *Service) relist(ctx context.Context, client *Client, host string) bool {
	listCtx, cancel := context.WithTimeout(ctx, listTimeout)
	defer cancel()
	containers, err := client.Containers(listCtx)
	if err != nil {
		return false
	}
	s.setState(State{Available: true, Host: host, Containers: containers})
	return true
}

func (s *Service) setState(next State) {
	s.mu.Lock()
	changed := !reflect.DeepEqual(s.state, next)
	s.state = next
	fire := s.onChange
	s.mu.Unlock()
	if changed && fire != nil {
		fire()
	}
}

func (s *Service) pause(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-s.kick:
	case <-time.After(d):
	}
}

// relevantEvent reports whether a stream line names a change the container
// list would show. Exec events are the noise to keep out, a healthcheck
// runs one every few seconds on every container that has one.
func relevantEvent(line []byte) bool {
	var event struct {
		Type   string `json:"Type"`
		Action string `json:"Action"`
	}
	if json.Unmarshal(line, &event) != nil {
		return false
	}
	if event.Type != "container" {
		return false
	}
	action := event.Action
	if i := strings.IndexByte(action, ':'); i >= 0 {
		action = action[:i]
	}
	switch action {
	case "create", "start", "stop", "die", "destroy", "kill", "pause",
		"unpause", "rename", "restart", "update", "oom", "health_status":
		return true
	}
	return false
}
