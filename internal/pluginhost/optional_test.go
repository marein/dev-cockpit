package pluginhost

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/plugin"
)

// lampPlugin adds one of every kind and runs a goroutine until its Serve's
// context ends.
type lampPlugin struct {
	overwrite bool
	fail      error
	panics    bool
	events    chan plugin.Event

	mu       sync.Mutex
	serves   []plugin.Serve
	running  atomic.Int32
	finished atomic.Int32
}

func (p *lampPlugin) ConfigureServe(s plugin.Serve) error {
	p.mu.Lock()
	p.serves = append(p.serves, s)
	p.mu.Unlock()
	p.running.Add(1)
	go func() {
		<-s.Context().Done()
		p.running.Add(-1)
		p.finished.Add(1)
	}()
	if p.panics {
		panic("the lamp burst")
	}
	if p.fail != nil {
		return p.fail
	}
	s.AddRoutes(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("lit")) }))
	s.AddPage(plugin.Page{Title: "Lamp", Icon: "ti-bulb"})
	s.AddSlotHTML(plugin.SlotProjectsActions, "<b>lamp</b>")
	s.AddSettings(map[string]string{"lamp-color": "warm"}, p.overwrite)
	s.AddAssistantMemory(memoryFiles(map[string]string{"lamp.md": "the lamp"}), p.overwrite)
	if p.events != nil {
		s.AddEventHandler([]string{plugin.EventJob}, func(ev plugin.Event) { p.events <- ev })
	}
	return nil
}

func (p *lampPlugin) configured() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.serves)
}

func (p *lampPlugin) last() plugin.Serve {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.serves[len(p.serves)-1]
}

// optionalHost wires the optional "lamp" before the always on "fixed", with a
// Set over the host. loaded counts the Loaded hook, where the cockpit applies
// a live plugin's settings and memory.
type optionalHost struct {
	*testHost
	set      *Set
	loaded   atomic.Int32
	prepared atomic.Int32
	store    *settings.Store
	memory   string
}

func newOptionalHost(t *testing.T, stateDir string, lamp plugin.ServePlugin) *optionalHost {
	t.Helper()
	wiring := []plugin.Named[plugin.ServePlugin]{
		{ID: "lamp", Plugin: lamp, Optional: true},
		{ID: "fixed", Plugin: &testPlugin{routeBody: "fixed"}},
	}
	required, err := ConfigureServe(wiring, "", stateDir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	th := &testHost{stateDir: stateDir, projectsDir: t.TempDir()}
	th.Host = NewHost(HostConfig{StateDir: stateDir, ProjectsDir: th.projectsDir, Compiled: required, Wiring: wiring, CSRFField: "csrf_token"})
	t.Cleanup(func() {
		th.Close()
		_ = os.RemoveAll(runRoot(stateDir))
	})
	oh := &optionalHost{testHost: th, set: NewSet(required, th.Host), store: settings.New(filepath.Join(stateDir, "settings.json")), memory: t.TempDir()}
	return oh
}

func (oh *optionalHost) start() {
	oh.Scan()
	ApplySettings(oh.set.Serves(), oh.store)
	applyMemory(oh.set.Serves(), oh.memory)
	oh.Start(Hooks{
		Prepare: func(*Serve) error { oh.prepared.Add(1); return nil },
		Release: func(string) { oh.released.Add(1) },
		Loaded: func(s *Serve) {
			oh.loaded.Add(1)
			ApplySettings([]*Serve{s}, oh.store)
			applyMemory([]*Serve{s}, oh.memory)
		},
		Changed: func() { oh.changed.Add(1) },
	})
}

func ids(serves []*Serve) string {
	var names []string
	for _, s := range serves {
		names = append(names, s.ID())
	}
	return strings.Join(names, ",")
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s never happened", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// An optional plugin that is off is not configured, served, written or in
// the set. The Plugins page lists it built in and off, beside the one always
// on.
func TestAnOptionalPluginRunsNothingWhileOff(t *testing.T) {
	lamp := &lampPlugin{}
	oh := newOptionalHost(t, t.TempDir(), lamp)
	oh.start()
	if lamp.configured() != 0 {
		t.Fatal("an optional plugin that is off was configured")
	}
	if got := ids(oh.set.Serves()); got != "fixed" {
		t.Fatalf("the set holds %s, want fixed alone", got)
	}
	if _, err := os.Stat(filepath.Join(oh.stateDir, "plugins", "lamp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("an optional plugin that is off has a state directory: %v", err)
	}
	if _, ok := oh.store.Lookup("lamp-color"); ok {
		t.Fatal("an optional plugin that is off wrote a setting")
	}
	if names := dirNames(t, oh.memory); len(names) != 0 {
		t.Fatalf("an optional plugin that is off wrote memory: %v", names)
	}
	list := oh.List()
	if len(list) != 2 || list[0].ID != "lamp" || list[1].ID != "fixed" {
		t.Fatalf("the page lists %+v, want lamp then fixed in wiring order", list)
	}
	if st := list[0]; !st.BuiltIn || !st.Optional || st.State != StateDisabled {
		t.Fatalf("the optional plugin stands %+v", st)
	}
	if st := list[1]; !st.BuiltIn || st.Optional || st.State != StateRunning {
		t.Fatalf("the required plugin stands %+v", st)
	}
	if st, ok := oh.Status("lamp"); !ok || st.State != StateDisabled {
		t.Fatalf("Status(lamp) = %+v, %v", st, ok)
	}
}

// Every switch on configures a fresh live Serve at its wiring place and
// applies its settings and memory. Every switch off ends that Serve's context
// and takes it out of the set.
func TestSwitchingAnOptionalPluginConfiguresAndEndsIt(t *testing.T) {
	lamp := &lampPlugin{}
	oh := newOptionalHost(t, t.TempDir(), lamp)
	oh.start()
	before := oh.changed.Load()
	if err := oh.SetEnabled("lamp", true); err != nil {
		t.Fatal(err)
	}
	if lamp.configured() != 1 || lamp.running.Load() != 1 {
		t.Fatalf("switched on, the lamp was configured %d times and runs %d", lamp.configured(), lamp.running.Load())
	}
	if got := ids(oh.set.Serves()); got != "lamp,fixed" {
		t.Fatalf("the set holds %s, want lamp,fixed", got)
	}
	first := oh.set.Lookup("lamp")
	if !first.Live() || !first.Running() || first.Key() == "lamp" || first.Page() == nil {
		t.Fatalf("the switched on lamp is not a live plugin with its page: key %s", first.Key())
	}
	if oh.prepared.Load() != 1 || oh.loaded.Load() != 1 || oh.changed.Load() == before {
		t.Fatalf("switch on prepared %d, loaded %d and announced %v", oh.prepared.Load(), oh.loaded.Load(), oh.changed.Load() != before)
	}
	if st, _ := oh.Status("lamp"); st.State != StateRunning || st.PageTitle != "Lamp" {
		t.Fatalf("the lamp stands %+v", st)
	}
	if err := oh.SetEnabled("lamp", true); err != nil || lamp.configured() != 1 {
		t.Fatalf("switching on a lamp that is on configured it again: %v", err)
	}

	if err := oh.SetEnabled("lamp", false); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the lamp's goroutine ending", func() bool { return lamp.running.Load() == 0 })
	if lamp.last().Context().Err() == nil {
		t.Fatal("the switched off Serve's context did not end")
	}
	if got := ids(oh.set.Serves()); got != "fixed" {
		t.Fatalf("switched off, the set holds %s", got)
	}
	if oh.released.Load() != 1 {
		t.Fatalf("switch off released %d asset sets", oh.released.Load())
	}
	if st, _ := oh.Status("lamp"); st.State != StateDisabled || st.PageTitle != "Lamp" {
		t.Fatalf("the switched off lamp stands %+v, its page keeps its title", st)
	}

	if err := oh.SetEnabled("lamp", true); err != nil {
		t.Fatal(err)
	}
	second := oh.set.Lookup("lamp")
	if lamp.configured() != 2 || second == first || second.Key() == first.Key() {
		t.Fatal("switching on again did not configure a fresh Serve")
	}
	if lamp.running.Load() != 1 || lamp.finished.Load() != 1 {
		t.Fatalf("after on, off, on the lamp runs %d and finished %d", lamp.running.Load(), lamp.finished.Load())
	}
}

// An off plugin's event handler hears nothing, an on one hears what happens.
func TestAnOptionalPluginHearsEventsOnlyWhileOn(t *testing.T) {
	lamp := &lampPlugin{events: make(chan plugin.Event, 4)}
	oh := newOptionalHost(t, t.TempDir(), lamp)
	oh.start()
	oh.set.Publish(plugin.Event{Source: plugin.EventJob, Headline: "while off"})
	if err := oh.SetEnabled("lamp", true); err != nil {
		t.Fatal(err)
	}
	oh.set.Publish(plugin.Event{Source: plugin.EventJob, Headline: "while on"})
	select {
	case ev := <-lamp.events:
		if ev.Headline != "while on" {
			t.Fatalf("the lamp heard %q first", ev.Headline)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the switched on lamp heard nothing")
	}
	if err := oh.SetEnabled("lamp", false); err != nil {
		t.Fatal(err)
	}
	oh.set.Publish(plugin.Event{Source: plugin.EventJob, Headline: "off again"})
	select {
	case ev := <-lamp.events:
		t.Fatalf("the switched off lamp heard %q", ev.Headline)
	case <-time.After(200 * time.Millisecond):
	}
}

// The switch lasts across a restart: Scan configures the lamp before the
// settings store is read, Start prepares it without the Loaded hook, as for
// the plugins always on. Close ends the lamp's context.
func TestTheSwitchOfAnOptionalPluginLastsAcrossARestart(t *testing.T) {
	stateDir := t.TempDir()
	lamp := &lampPlugin{}
	first := newOptionalHost(t, stateDir, lamp)
	first.start()
	if err := first.SetEnabled("lamp", true); err != nil {
		t.Fatal(err)
	}
	first.Close()
	waitFor(t, "the lamp ending at Close", func() bool { return lamp.running.Load() == 0 })

	again := &lampPlugin{}
	second := newOptionalHost(t, stateDir, again)
	second.Scan()
	if again.configured() != 1 || ids(second.set.Serves()) != "lamp,fixed" {
		t.Fatalf("after a restart the lamp was configured %d times, the set holds %s", again.configured(), ids(second.set.Serves()))
	}
	second.Start(Hooks{Prepare: func(*Serve) error { second.prepared.Add(1); return nil }, Loaded: func(*Serve) { second.loaded.Add(1) }})
	if second.prepared.Load() != 1 || second.loaded.Load() != 0 {
		t.Fatalf("the start prepared %d and loaded %d", second.prepared.Load(), second.loaded.Load())
	}
	if err := second.SetEnabled("lamp", false); err != nil {
		t.Fatal(err)
	}
	third := newOptionalHost(t, stateDir, &lampPlugin{})
	third.Scan()
	if got := ids(third.set.Serves()); got != "fixed" {
		t.Fatalf("switched off before the restart, the set holds %s", got)
	}
}

// A switch on writes settings and memory by their overwrite rule, a switch
// off leaves them and their edits, the next switch on keeps an edit without
// overwrite and replaces it with.
func TestSwitchingOffLeavesWhatAnOptionalPluginWrote(t *testing.T) {
	for _, overwrite := range []bool{false, true} {
		lamp := &lampPlugin{overwrite: overwrite}
		oh := newOptionalHost(t, t.TempDir(), lamp)
		oh.start()
		if err := oh.SetEnabled("lamp", true); err != nil {
			t.Fatal(err)
		}
		memory := filepath.Join(oh.memory, "lamp.md")
		if value, _ := oh.store.Lookup("lamp-color"); value != "warm" || readFile(t, memory) != "the lamp" {
			t.Fatalf("switch on wrote %q and %q", value, readFile(t, memory))
		}
		oh.store.Set("lamp-color", "cold")
		writeFile(t, memory, "edited")
		if err := oh.SetEnabled("lamp", false); err != nil {
			t.Fatal(err)
		}
		if value, _ := oh.store.Lookup("lamp-color"); value != "cold" || readFile(t, memory) != "edited" {
			t.Fatalf("switch off changed what was written: %q and %q", value, readFile(t, memory))
		}
		if _, err := os.Stat(filepath.Join(oh.stateDir, "plugins", "lamp")); err != nil {
			t.Fatalf("switch off took the plugin's state directory: %v", err)
		}
		if err := oh.SetEnabled("lamp", true); err != nil {
			t.Fatal(err)
		}
		wantValue, wantMemory := "cold", "edited"
		if overwrite {
			wantValue, wantMemory = "warm", "the lamp"
		}
		if value, _ := oh.store.Lookup("lamp-color"); value != wantValue || readFile(t, memory) != wantMemory {
			t.Fatalf("overwrite %v: switching on again left %q and %q", overwrite, value, readFile(t, memory))
		}
	}
}

// A plugin that fails or panics in ConfigureServe is refused with why, its
// context ends, it stays out of the set and its switch stays off.
func TestAnOptionalPluginThatFailsIsRefusedAndStaysOff(t *testing.T) {
	for name, lamp := range map[string]*lampPlugin{
		"error": {fail: errors.New("The bulb is missing.")},
		"panic": {panics: true},
	} {
		t.Run(name, func(t *testing.T) {
			oh := newOptionalHost(t, t.TempDir(), lamp)
			oh.start()
			err := oh.SetEnabled("lamp", true)
			if !IsInstallError(err) || !strings.Contains(err.Error(), "lamp could not be switched on") {
				t.Fatalf("SetEnabled() = %v, want a refusal that says why", err)
			}
			if name == "error" && !strings.Contains(err.Error(), "The bulb is missing.") || name == "panic" && !strings.Contains(err.Error(), "the lamp burst") {
				t.Fatalf("the refusal does not name the cause: %v", err)
			}
			waitFor(t, "the failed lamp's context ending", func() bool { return lamp.running.Load() == 0 })
			if st, _ := oh.Status("lamp"); st.State != StateDisabled || ids(oh.set.Serves()) != "fixed" {
				t.Fatalf("the failed lamp stands %+v in %s", st, ids(oh.set.Serves()))
			}
			if oh.switchedOn()["lamp"] {
				t.Fatal("the failed lamp's switch stayed on")
			}
		})
	}
}

// A switched on lamp that fails at the next start is broken with why, the
// cockpit starts anyway, a switch off clears it.
func TestAnOptionalPluginThatFailsAtTheStartStandsBroken(t *testing.T) {
	stateDir := t.TempDir()
	first := newOptionalHost(t, stateDir, &lampPlugin{})
	first.start()
	if err := first.SetEnabled("lamp", true); err != nil {
		t.Fatal(err)
	}
	first.Close()
	oh := newOptionalHost(t, stateDir, &lampPlugin{panics: true})
	oh.start()
	st, _ := oh.Status("lamp")
	if st.State != StateBroken || !strings.Contains(st.Reason, "the lamp burst") {
		t.Fatalf("the lamp that fails at the start stands %+v", st)
	}
	if got := ids(oh.set.Serves()); got != "fixed" {
		t.Fatalf("the set holds %s", got)
	}
	if err := oh.SetEnabled("lamp", false); err != nil {
		t.Fatal(err)
	}
	if st, _ := oh.Status("lamp"); st.State != StateDisabled || st.Reason != "" {
		t.Fatalf("switched off, the lamp stands %+v", st)
	}
}

// No compiled plugin restarts or is removed, one always on does not switch.
// Its id stays taken for an external plugin, an optional one's also while
// off.
func TestBuiltInPluginsTakeOnlyTheirSwitch(t *testing.T) {
	oh := newOptionalHost(t, t.TempDir(), &lampPlugin{})
	oh.start()
	for _, change := range []func(string) error{oh.Restart, oh.Remove} {
		if err := change("lamp"); !IsInstallError(err) || !strings.Contains(err.Error(), "built in") {
			t.Fatalf("a change of the optional plugin answered %v", err)
		}
	}
	for _, change := range []func(string) error{func(id string) error { return oh.SetEnabled(id, false) }, oh.Restart, oh.Remove} {
		if err := change("fixed"); !IsInstallError(err) || !strings.Contains(err.Error(), "always on") {
			t.Fatalf("a change of the required plugin answered %v", err)
		}
	}
	writePlugin(t, oh.Dir(), "lamp", "ok", nil)
	if st := waitFolderState(t, oh.testHost, "lamp", StateBroken); !strings.Contains(st.Reason, "built in plugin") {
		t.Fatalf("an external plugin of the optional id stands %+v", st)
	}
	if err := oh.Remove("lamp"); err != nil {
		t.Fatalf("the clashing directory could not be removed: %v", err)
	}
	source := writePlugin(t, t.TempDir(), "lamp", "ok", nil)
	if _, _, err := oh.Install(source, false); !IsInstallError(err) || !strings.Contains(err.Error(), "built in plugin") {
		t.Fatalf("installing an external plugin of the optional id answered %v", err)
	}
}

// ConfigureServe skips the optional pair and answers the pairs always on in
// wiring order with their places.
func TestConfigureServeLeavesOptionalPairs(t *testing.T) {
	lamp := &lampPlugin{}
	serves, err := ConfigureServe([]plugin.Named[plugin.ServePlugin]{
		{ID: "first", Plugin: &testPlugin{}},
		{ID: "lamp", Plugin: lamp, Optional: true},
		{ID: "last", Plugin: &testPlugin{}},
	}, "", t.TempDir(), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if ids(serves) != "first,last" || lamp.configured() != 0 {
		t.Fatalf("ConfigureServe configured %s and the lamp %d times", ids(serves), lamp.configured())
	}
	for _, s := range serves {
		if s.Live() || s.Context() == nil || s.Context().Err() != nil {
			t.Fatalf("%s, always on, is not fixed with a context that lasts", s.ID())
		}
	}
	if serves[0].order != 0 || serves[1].order != 2 {
		t.Fatalf("the places are %d and %d, want 0 and 2", serves[0].order, serves[1].order)
	}
}
