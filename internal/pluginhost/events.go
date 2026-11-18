package pluginhost

import (
	"fmt"
	"log"
	"sync"

	"github.com/marein/dev-cockpit/plugin"
)

// definedSources mirrors the plugin package's Event constants.
var definedSources = map[string]bool{
	plugin.EventJob:     true,
	plugin.EventCoder:   true,
	plugin.EventCompose: true,
}

// eventQueue bounds the events waiting for the plugins, past it Publish
// drops an event rather than block the publisher.
const eventQueue = 256

type eventHandler struct {
	sources map[string]bool
	handle  func(plugin.Event)
}

func (s *Serve) AddEventHandler(sources []string, handler func(plugin.Event)) {
	s.add()
	if handler == nil || len(sources) == 0 {
		panic(fmt.Sprintf("plugin %s: AddEventHandler needs a source and a handler", s.id))
	}
	set := map[string]bool{}
	for _, source := range sources {
		if !definedSources[source] {
			panic(fmt.Sprintf("plugin %s: unknown event source %q", s.id, source))
		}
		set[source] = true
	}
	s.handlers = append(s.handlers, eventHandler{sources: set, handle: handler})
}

// events is a Set's queue, its goroutine starts with the first event.
type events struct {
	once  sync.Once
	queue chan plugin.Event
}

// Publish hands an event to every plugin that subscribed to its source, in
// publish order. It never blocks, an event that finds the queue full is
// dropped and logged.
func (s *Set) Publish(ev plugin.Event) {
	if s == nil {
		return
	}
	s.events.once.Do(func() {
		s.events.queue = make(chan plugin.Event, eventQueue)
		go func() {
			for ev := range s.events.queue {
				for _, serve := range s.Serves() {
					serve.deliver(ev)
				}
			}
		}()
	})
	select {
	case s.events.queue <- ev:
	default:
		log.Printf("plugins: an event of %s was dropped, the plugins fall behind", ev.Source)
	}
}

// deliver runs the handlers of the event's source, a panic is logged and
// takes nothing else down.
func (s *Serve) deliver(ev plugin.Event) {
	for _, h := range s.handlers {
		if !h.sources[ev.Source] {
			continue
		}
		func() {
			defer func() {
				if r := recover(); r != nil {
					log.Printf("plugin %s: its event handler panicked: %v", s.id, r)
				}
			}()
			h.handle(ev)
		}()
	}
}

// maxEventStreams bounds the event streams one external plugin holds open.
const maxEventStreams = 8

// deliverEvent is an external plugin's event handler. A stream that does not
// keep up misses the event.
func (s *supervisor) deliverEvent(ev plugin.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for stream := range s.streams {
		select {
		case stream <- ev:
		default:
		}
	}
}

func (s *supervisor) openStream() (chan plugin.Event, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.streams) >= maxEventStreams {
		return nil, false
	}
	if s.streams == nil {
		s.streams = map[chan plugin.Event]struct{}{}
	}
	stream := make(chan plugin.Event, 64)
	s.streams[stream] = struct{}{}
	return stream, true
}

func (s *supervisor) closeStream(stream chan plugin.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.streams, stream)
}
