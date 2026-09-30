package ollama

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/statefile"
)

func withExecutableOnPath(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, Executable), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
}

func names(models []Model) string {
	var parts []string
	for _, m := range models {
		parts = append(parts, m.Source+":"+m.Name)
	}
	return strings.Join(parts, ",")
}

type fakeServer struct {
	*httptest.Server
	down        atomic.Bool
	catalogDown atomic.Bool
	mu          sync.Mutex
	hits        map[string]int
}

func (f *fakeServer) asked(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hits[key]
}

func newFake(t *testing.T, server, catalog []string, shows map[string]string) *fakeServer {
	t.Helper()
	f := &fakeServer{hits: map[string]int{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.URL.Path
		var req struct {
			Model string `json:"model"`
		}
		if r.Method == http.MethodPost {
			_ = json.NewDecoder(r.Body).Decode(&req)
			key += " " + req.Model
		}
		f.mu.Lock()
		f.hits[key]++
		f.mu.Unlock()
		switch {
		case r.URL.Path == "/catalog" && !f.catalogDown.Load():
			models := []map[string]any{}
			for _, name := range catalog {
				models = append(models, map[string]any{"name": name})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
		case f.down.Load() || r.URL.Path == "/catalog":
			http.Error(w, "down", http.StatusServiceUnavailable)
		case r.URL.Path == "/api/tags":
			models := []map[string]any{{"name": "llama3:8b"}}
			for _, name := range server {
				models = append(models, map[string]any{"name": name, "remote_host": "https://ollama.com"})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"models": models})
		case r.URL.Path == "/api/show" && shows[req.Model] != "":
			_, _ = w.Write([]byte(shows[req.Model]))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func window(n int) string {
	raw, _ := json.Marshal(map[string]any{"model_info": map[string]any{"general.architecture": "x", "llama.context_length": n}})
	return string(raw)
}

func client(t *testing.T, f *fakeServer, stateDir string) (*Client, *settings.Store) {
	t.Helper()
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	store.Set(HostKey, f.URL)
	return New(store, f.URL+"/catalog", stateDir), store
}

func TestAPassMergesServerAddedAndCatalogNamesOnce(t *testing.T) {
	withExecutableOnPath(t)
	f := newFake(t, []string{"deepseek-v4:cloud", "gpt-oss:120b-cloud"}, []string{"nemotron-3-ultra", "gpt-oss:120b", "deepseek-v4:cloud", "kimi-k2.7:cloud"}, map[string]string{"deepseek-v4:cloud": window(262144)})
	c, store := client(t, f, "")
	for _, name := range []string{" mistral-large:cloud ", "kimi-k2.7:cloud", "mistral-large:cloud"} {
		if err := c.Add(name); err != nil {
			t.Fatalf("Add(%q): %v", name, err)
		}
	}
	if got := names(c.Models()); got != "added:mistral-large:cloud,added:kimi-k2.7:cloud" {
		t.Fatalf("want nothing but the added names before a pass, got %s", got)
	}
	if !c.pass(time.Now()) {
		t.Fatal("want the pass to see the server answer")
	}
	want := "server:deepseek-v4:cloud,server:gpt-oss:120b-cloud,added:mistral-large:cloud,added:kimi-k2.7:cloud,catalog:nemotron-3-ultra:cloud"
	if got := names(c.Models()); got != want {
		t.Fatalf("want the server's names, then the added ones, then the catalog's spelled for the cloud, each once and no local one\n got %s\nwant %s", got, want)
	}
	if err := c.Add("gpt-oss:120b-cloud"); err != nil || strings.Contains(store.Get(ModelsKey), "gpt-oss") {
		t.Fatalf("want an offered name nothing to add, got %v %s", err, store.Get(ModelsKey))
	}
}

func TestCloudNamesSpellBothShapes(t *testing.T) {
	cases := map[string]string{
		"nemotron-3-ultra":   "nemotron-3-ultra:cloud",
		"gpt-oss:120b":       "gpt-oss:120b-cloud",
		"deepseek-v4:cloud":  "deepseek-v4:cloud",
		"gpt-oss:120b-cloud": "gpt-oss:120b-cloud",
		" kimi-k2.7 ":        "kimi-k2.7:cloud",
	}
	for raw, want := range cases {
		if got := cloudName(raw); got != want {
			t.Errorf("cloudName(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestTheWindowMatchesANameWithOrWithoutItsTag(t *testing.T) {
	withExecutableOnPath(t)
	f := newFake(t, []string{"nemotron-3-ultra:cloud", "gpt-oss:120b-cloud", "glm-5.3:cloud"}, []string{"deepseek-v4"}, map[string]string{
		"nemotron-3-ultra:cloud": `{"model_info":{".context_length":262144}}`,
		"gpt-oss:120b-cloud":     window(131072),
		"glm-5.3:cloud":          `{"model_info":{"general.architecture":"glm"}}`,
		"deepseek-v4:cloud":      `{"model_info":{"context_length":163840}}`,
	})
	c, _ := client(t, f, "")
	c.pass(time.Now())
	for _, name := range []string{"nemotron-3-ultra", "nemotron-3-ultra:cloud", " nemotron-3-ultra:cloud "} {
		if got := c.Window(name); got != 262144 {
			t.Fatalf("Window(%q) = %d, want the window with or without the cloud tag", name, got)
		}
	}
	for _, name := range []string{"gpt-oss:120b", "gpt-oss:120b-cloud"} {
		if got := c.Window(name); got != 131072 {
			t.Fatalf("Window(%q) = %d, want the window of the tag with the -cloud suffix", name, got)
		}
	}
	if c.Window("deepseek-v4:cloud") != 163840 {
		t.Fatalf("want a catalog name's window read by the pass, got %d", c.Window("deepseek-v4"))
	}
	if c.Window("glm-5.3:cloud") != 0 || c.Window("nobody:cloud") != 0 || c.Window("") != 0 {
		t.Fatal("want no window without the key or without a record")
	}
	shows := f.asked("/api/show nemotron-3-ultra:cloud")
	c.pass(time.Now())
	if f.asked("/api/show nemotron-3-ultra:cloud") != shows || f.asked("/api/show glm-5.3:cloud") != 2 {
		t.Fatalf("want a known window never asked again and a missing one asked on every pass, got %v", f.hits)
	}
}

func TestAFailingServerKeepsTheLastNamesAndWindowsAndIsReadAsDown(t *testing.T) {
	withExecutableOnPath(t)
	f := newFake(t, []string{"deepseek-v4:cloud"}, nil, map[string]string{"deepseek-v4:cloud": window(262144)})
	c, store := client(t, f, "")
	if !c.pass(time.Now()) || c.Warning() != "" || c.Check() != nil {
		t.Fatalf("want a silent page while the server answers, got %q %v", c.Warning(), c.Check())
	}
	f.down.Store(true)
	if c.pass(time.Now()) {
		t.Fatal("want the pass to see the server down")
	}
	if got := names(c.Models()); got != "server:deepseek-v4:cloud" || c.Window("deepseek-v4") != 262144 {
		t.Fatalf("want the last names and windows kept, got %s %d", got, c.Window("deepseek-v4"))
	}
	if c.Warning() != "The Ollama server does not answer." || c.Check() == nil || c.Check().Error() != "The Ollama server does not answer." {
		t.Fatalf("want the warning and the refusal, got %q %v", c.Warning(), c.Check())
	}
	store.Set(HostKey, "http://127.0.0.1:1")
	c.pass(time.Now())
	if got := names(c.Models()); got != "" {
		t.Fatalf("want a new host that does not answer to drop the old server's names, got %s", got)
	}
	t.Setenv("PATH", t.TempDir())
	if c.Warning() != "" || c.Check().Error() != "The ollama executable was not found on PATH." {
		t.Fatalf("want no warning and the executable named first while ollama is missing, got %q %v", c.Warning(), c.Check())
	}
}

func TestTheCatalogIsFetchedOnItsOwnAgeAndKeptOnFailure(t *testing.T) {
	withExecutableOnPath(t)
	f := newFake(t, nil, []string{"nemotron-3-ultra"}, nil)
	c, _ := client(t, f, "")
	now := time.Now()
	c.pass(now)
	c.pass(now.Add(catalogAge - time.Second))
	if f.asked("/catalog") != 1 {
		t.Fatalf("want the catalog asked once inside its age, got %d", f.asked("/catalog"))
	}
	f.catalogDown.Store(true)
	c.pass(now.Add(catalogAge))
	if f.asked("/catalog") != 2 || names(c.Models()) != "catalog:nemotron-3-ultra:cloud" {
		t.Fatalf("want the catalog asked again after its age and kept through the failure, got %d %s", f.asked("/catalog"), names(c.Models()))
	}
	f.catalogDown.Store(false)
	c.pass(now.Add(catalogAge + time.Second))
	if f.asked("/catalog") != 3 {
		t.Fatal("want a failed catalog asked again on the next pass")
	}
}

func TestThePassWritesTheSnapshotAndTheNextProcessReadsItAtOnce(t *testing.T) {
	withExecutableOnPath(t)
	f := newFake(t, []string{"deepseek-v4:cloud"}, []string{"nemotron-3-ultra"}, map[string]string{"deepseek-v4:cloud": window(262144)})
	stateDir := t.TempDir()
	c, store := client(t, f, stateDir)
	now := time.Now()
	c.pass(now)
	var file snapshot
	statefile.Load(filepath.Join(stateDir, CacheFile), &file)
	if file.Host != f.URL || strings.Join(file.Names, ",") != "deepseek-v4:cloud" || file.Windows["deepseek-v4"] != 262144 || strings.Join(file.Catalog, ",") != "nemotron-3-ultra:cloud" || !file.Reachable || file.CatalogAt.IsZero() || !file.CheckedAt.Equal(file.CatalogAt) {
		t.Fatalf("want names, windows, catalog, reachability and both times written, got %+v", file)
	}
	f.Close()
	again := New(store, f.URL+"/catalog", stateDir)
	if got := names(again.Models()); got != "server:deepseek-v4:cloud,catalog:nemotron-3-ultra:cloud" || again.Window("deepseek-v4:cloud") != 262144 || again.Warning() != "" {
		t.Fatalf("want the next process to answer from the written snapshot without a pass, got %s %q", got, again.Warning())
	}
	broken := t.TempDir()
	if err := os.WriteFile(filepath.Join(broken, CacheFile), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := names(New(store, "", broken).Models()); got != "" {
		t.Fatalf("want a corrupt file read as an empty snapshot, got %s", got)
	}
	if _, err := os.Stat(filepath.Join(broken, CacheFile+".broken")); err != nil {
		t.Fatalf("want the corrupt file quarantined, got %v", err)
	}
}

func TestWaitReachableReturnsOnceAPassSawTheServerOrAtTheTimeout(t *testing.T) {
	withExecutableOnPath(t)
	f := newFake(t, nil, nil, nil)
	f.down.Store(true)
	stateDir := t.TempDir()
	statefile.Save(filepath.Join(stateDir, CacheFile), 0o644, snapshot{Reachable: true})
	c, _ := client(t, f, stateDir)
	c.pass(time.Now())
	if c.WaitReachable(time.Millisecond) {
		t.Fatal("want a snapshot from disk and a failed pass not taken as an answer")
	}
	f.down.Store(false)
	c.pass(time.Now())
	if !c.WaitReachable(time.Hour) {
		t.Fatal("want the wait over once a pass saw the server answer")
	}
	f.down.Store(true)
	c.pass(time.Now())
	if !c.WaitReachable(time.Hour) {
		t.Fatal("want an answer seen once to stay seen")
	}
}

func TestSavingTheHostOrTheListWakesTheLoop(t *testing.T) {
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	c := New(store, "http://127.0.0.1:1/catalog", "")
	woken := func() bool {
		select {
		case <-c.wake:
			return true
		default:
			return false
		}
	}
	for _, step := range []struct {
		label  string
		change func() error
	}{
		{"host", func() error { return c.SetHost("http://127.0.0.1:11434") }},
		{"add", func() error { return c.Add("mistral-large:cloud") }},
		{"delete", func() error { return c.Delete("mistral-large:cloud") }},
	} {
		if err := step.change(); err != nil || !woken() {
			t.Fatalf("%s: want the loop woken, got %v", step.label, err)
		}
	}
	if err := c.SetHost("two words"); err == nil || woken() {
		t.Fatal("want a refused host to wake nothing")
	}
	c.Wake()
	c.Wake()
	if !woken() || woken() {
		t.Fatal("want wakes to fold into one")
	}
}

func TestAddAndDeleteGuardTheSharedList(t *testing.T) {
	withExecutableOnPath(t)
	f := newFake(t, []string{"deepseek-v4:cloud"}, []string{"nemotron-3-ultra"}, nil)
	c, store := client(t, f, "")
	c.pass(time.Now())
	if err := c.Add(" "); err == nil {
		t.Fatal("want an empty name refused")
	}
	if err := c.Add("ollama/mistral-large:cloud"); err == nil || !strings.Contains(err.Error(), "without the ollama/ prefix") {
		t.Fatalf("want a prefixed name refused, got %v", err)
	}
	for _, name := range []string{"deepseek-v4:cloud", "nemotron-3-ultra:cloud"} {
		if err := c.Delete(name); err == nil || err.Error() != name+" comes from Ollama and cannot be removed." {
			t.Fatalf("Delete(%q): want the refusal, got %v", name, err)
		}
	}
	if err := c.Delete("nobody-added:cloud"); err != nil {
		t.Fatalf("want a name nobody added to be nothing to do, got %v", err)
	}
	if err := c.Add("mistral-large:cloud"); err != nil {
		t.Fatal(err)
	}
	if err := c.Delete("mistral-large:cloud"); err != nil {
		t.Fatal(err)
	}
	if got := names(c.Models()); strings.Contains(got, "added:") {
		t.Fatalf("want the added name gone, got %s", got)
	}
	if _, ok := store.Lookup(ModelsKey); ok {
		t.Fatal("want an emptied list to take its key out of the store")
	}
}

func TestAnAddedNameLeavesTheListEvenWhileTheServerOffersIt(t *testing.T) {
	withExecutableOnPath(t)
	f := newFake(t, []string{"deepseek-v4:cloud"}, []string{"nemotron-3-ultra"}, nil)
	c, store := client(t, f, "")
	if err := c.Add("deepseek-v4:cloud"); err != nil || store.Get(ModelsKey) != `["deepseek-v4:cloud"]` {
		t.Fatalf("want the name added while nothing offers it, got %v %q", err, store.Get(ModelsKey))
	}
	c.pass(time.Now())
	if got := names(c.Models()); got != "server:deepseek-v4:cloud,catalog:nemotron-3-ultra:cloud" {
		t.Fatalf("want the name listed once as the server's, got %s", got)
	}
	if err := c.Delete("deepseek-v4:cloud"); err != nil {
		t.Fatalf("want an added name removed even while the server offers it, got %v", err)
	}
	if err := c.Delete("deepseek-v4:cloud"); err == nil || !strings.Contains(err.Error(), "comes from Ollama") {
		t.Fatalf("want a name that was never added refused, got %v", err)
	}
}

func TestModelsAreNoneWithoutTheExecutable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	stateDir := t.TempDir()
	statefile.Save(filepath.Join(stateDir, CacheFile), 0o644, snapshot{Names: []string{"deepseek-v4:cloud"}})
	if got := New(nil, "", stateDir).Models(); got != nil {
		t.Fatalf("want no names without the executable, got %v", got)
	}
	if Available() || New(nil, "", stateDir).Check().Error() != "The ollama executable was not found on PATH." {
		t.Fatal("want the executable reported missing with its sentence")
	}
}

func TestTheHostSetting(t *testing.T) {
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	c := New(store, "http://127.0.0.1:1/catalog", "")
	if c.Host() != "" {
		t.Fatal("want no host without a setting")
	}
	if err := c.SetHost(" http://ollama.home:11434 "); err != nil || c.Host() != "http://ollama.home:11434" {
		t.Fatalf("want the configured host trimmed, got %v %q", err, c.Host())
	}
	if err := c.SetHost(""); err != nil || c.Host() != "" {
		t.Fatalf("want an empty host to clear the setting, got %v %q", err, c.Host())
	}
	if _, ok := store.Lookup(HostKey); ok {
		t.Fatal("want the cleared setting out of the store")
	}
	var none *Client
	if none.Host() != "" {
		t.Fatal("want a missing client to answer the default")
	}
}

func TestSetHostTakesHTTPAndHTTPSAloneAndNoLinkLocalAddress(t *testing.T) {
	store := settings.New(filepath.Join(t.TempDir(), "settings.json"))
	c := New(store, "http://127.0.0.1:1/catalog", "")
	for _, host := range []string{"http://127.0.0.1:11434", "https://ollama.local", "127.0.0.1:11434", "localhost:11434", "10.0.0.5:11434", "192.168.1.9", "http://172.16.0.2:11434", "http://[::1]:11434", "http://ollama.home:11434", ""} {
		if err := c.SetHost(host); err != nil || c.Host() != host {
			t.Fatalf("SetHost(%q) = %v, want it taken, stored %q", host, err, c.Host())
		}
	}
	for _, host := range []string{"two words", "ftp://127.0.0.1:21", "file:///var/run/ollama", "socks5://127.0.0.1:1080", "ollama://host"} {
		if err := c.SetHost(host); err == nil || err.Error() != "The Ollama host is an address like http://127.0.0.1:11434." {
			t.Fatalf("SetHost(%q) = %v, want the plain refusal", host, err)
		}
	}
	for _, host := range []string{"http://169.254.169.254", "169.254.1.1:11434", "http://[fe80::1]:11434", "http://[fe80::1%25eth0]:11434"} {
		if err := c.SetHost(host); err == nil || err.Error() != "The Ollama host cannot be a link local address." {
			t.Fatalf("SetHost(%q) = %v, want the link local refusal", host, err)
		}
	}
	if got := c.Host(); got != "" {
		t.Fatalf("want a refused host to store nothing, got %q", got)
	}
}

func TestBaseURLFollowsOllamasOwnDefaults(t *testing.T) {
	cases := map[string]string{
		"":                        "http://127.0.0.1:11434",
		"localhost":               "http://localhost:11434",
		"10.0.0.5:11500":          "http://10.0.0.5:11500",
		"http://ollama.home":      "http://ollama.home:80",
		"https://ollama.home":     "https://ollama.home:443",
		"http://127.0.0.1:11434/": "http://127.0.0.1:11434",
		"::1":                     "http://[::1]:11434",
		"[::1]:11434":             "http://[::1]:11434",
		"http://ollama.home/sub":  "http://ollama.home:80/sub",
	}
	for raw, want := range cases {
		if got := baseURL(raw); got != want {
			t.Errorf("baseURL(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestTheLoopPassesEverySecondWhileSomeoneWaitsForTheFirstAnswer(t *testing.T) {
	withExecutableOnPath(t)
	f := newFake(t, nil, nil, nil)
	f.down.Store(true)
	c, _ := client(t, f, "")
	if c.sleep(false) != failedSleep || c.sleep(true) != answeredSleep {
		t.Fatal("want the usual sleeps while nobody waits")
	}
	if c.WaitReachable(0) || c.sleep(false) != failedSleep {
		t.Fatal("want a wait that ended to leave the usual sleep")
	}
	<-c.wake
	done := make(chan bool)
	go func() { done <- c.WaitReachable(time.Hour) }()
	<-c.wake
	if c.pass(time.Now()) || c.sleep(false) != time.Second {
		t.Fatalf("want a pass every second while someone waits and the server is down, got %s", c.sleep(false))
	}
	f.down.Store(false)
	if !c.pass(time.Now()) || !<-done {
		t.Fatal("want the waiter released by the pass that saw the server")
	}
	if c.sleep(true) != answeredSleep || c.sleep(false) != failedSleep {
		t.Fatalf("want the usual sleeps once the server answered and nobody waits, got %s and %s", c.sleep(true), c.sleep(false))
	}
}
