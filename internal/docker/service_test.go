package docker

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeDaemon is enough daemon for the watcher: a ping, a mutable container
// list, and an event stream fed through a channel.
type fakeDaemon struct {
	mu     sync.Mutex
	list   []map[string]any
	events chan string
}

func (d *fakeDaemon) setList(list []map[string]any) {
	d.mu.Lock()
	d.list = list
	d.mu.Unlock()
}

func (d *fakeDaemon) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/"+apiVersion+"/_ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("OK"))
	})
	mux.HandleFunc("/"+apiVersion+"/containers/json", func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		defer d.mu.Unlock()
		_ = json.NewEncoder(w).Encode(d.list)
	})
	mux.HandleFunc("/"+apiVersion+"/events", func(w http.ResponseWriter, r *http.Request) {
		flusher := w.(http.Flusher)
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		for {
			select {
			case <-r.Context().Done():
				return
			case line := <-d.events:
				_, _ = w.Write([]byte(line + "\n"))
				flusher.Flush()
			}
		}
	})
	return mux
}

func waitFor(t *testing.T, what string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestServiceListsAndFollowsEvents(t *testing.T) {
	daemon := &fakeDaemon{events: make(chan string)}
	daemon.setList([]map[string]any{{"Id": "aaa", "Names": []string{"/one"}, "State": "running"}})
	server := httptest.NewServer(daemon.handler())
	defer server.Close()

	service := NewService(t.TempDir(), func() string { return server.URL })
	var mu sync.Mutex
	changes := 0
	service.OnChange(func() {
		mu.Lock()
		changes++
		mu.Unlock()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)

	waitFor(t, "the first list", func() bool {
		state := service.State()
		return state.Available && len(state.Containers) == 1
	})
	if service.State().Host != server.URL {
		t.Fatalf("host answered %q", service.State().Host)
	}

	daemon.setList([]map[string]any{
		{"Id": "aaa", "Names": []string{"/one"}, "State": "running"},
		{"Id": "bbb", "Names": []string{"/two"}, "State": "running"},
	})
	daemon.events <- `{"Type":"container","Action":"start"}`
	waitFor(t, "the event driven relist", func() bool {
		return len(service.State().Containers) == 2
	})

	mu.Lock()
	seen := changes
	mu.Unlock()
	if seen < 2 {
		t.Fatalf("OnChange fired %d times, want one per moved state", seen)
	}
}

func TestServiceGoesUnavailableWhenTheDaemonDies(t *testing.T) {
	daemon := &fakeDaemon{events: make(chan string)}
	server := httptest.NewServer(daemon.handler())
	service := NewService(t.TempDir(), func() string { return server.URL })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go service.Run(ctx)
	waitFor(t, "the connection", func() bool { return service.State().Available })

	server.CloseClientConnections()
	server.Close()
	waitFor(t, "the unavailable state", func() bool { return !service.State().Available })
}

func TestServiceUnavailableWithoutAnyHost(t *testing.T) {
	service := NewService(t.TempDir(), func() string { return "" })
	if service.State().Available {
		t.Fatal("fresh service claims availability")
	}
	if _, err := service.Client(); err == nil {
		t.Fatal("Client answered without a host")
	}
}
