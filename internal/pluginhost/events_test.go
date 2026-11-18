package pluginhost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/plugin"
)

// A compiled plugin's handler sees its sources' events in order, a panicking
// handler of another plugin takes nothing down.
func TestACompiledHandlerSeesItsSources(t *testing.T) {
	got := make(chan plugin.Event, 8)
	serves := configured(t,
		plugin.Named[plugin.ServePlugin]{ID: "loud", Plugin: serveFunc(func(s plugin.Serve) error {
			s.AddEventHandler([]string{plugin.EventCoder}, func(plugin.Event) { panic("boom") })
			return nil
		})},
		plugin.Named[plugin.ServePlugin]{ID: "quiet", Plugin: serveFunc(func(s plugin.Serve) error {
			s.AddEventHandler([]string{plugin.EventCoder, plugin.EventJob}, func(ev plugin.Event) { got <- ev })
			return nil
		})},
	)
	set := NewSet(serves, nil)
	for _, ev := range []plugin.Event{{Source: plugin.EventCompose, Kind: "done"}, {Source: plugin.EventCoder, Kind: "asks"}, {Source: plugin.EventJob, Kind: "done"}} {
		set.Publish(ev)
	}
	for _, want := range []string{plugin.EventCoder, plugin.EventJob} {
		select {
		case ev := <-got:
			if ev.Source != want {
				t.Fatalf("the handler saw %s, want %s", ev.Source, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("the handler never saw %s", want)
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("an unknown source was taken")
		}
	}()
	configured(t, plugin.Named[plugin.ServePlugin]{ID: "odd", Plugin: serveFunc(func(s plugin.Serve) error {
		s.AddEventHandler([]string{"weather"}, func(plugin.Event) {})
		return nil
	})})
}

// An external plugin streams the events of its manifest's sources on its
// callback socket.
func TestAnExternalPluginStreamsItsEvents(t *testing.T) {
	h := newTestHost(t)
	writePlugin(t, h.Dir(), "demo", "ok", func(m map[string]any) { m["events"] = []string{plugin.EventCoder} })
	h.start()
	waitState(t, h, "demo", StateRunning)
	set := NewSet(nil, h.Host)
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- proxied(t, set.Lookup("demo"), httptest.NewRequest(http.MethodGet, "/plugins/demo/listen", nil))
	}()
	for {
		set.Publish(plugin.Event{Source: plugin.EventCompose, Headline: "not subscribed"})
		set.Publish(plugin.Event{Source: plugin.EventCoder, Kind: "asks", Headline: "Coder asks a question: fix"})
		select {
		case rec := <-done:
			if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"headline":"Coder asks a question: fix"`) || strings.Contains(rec.Body.String(), "not subscribed") {
				t.Fatalf("the stream answered %d %s", rec.Code, rec.Body.String())
			}
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}
