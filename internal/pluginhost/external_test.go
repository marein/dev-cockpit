package pluginhost

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/plugin"
)

// TestMain doubles as an external plugin: with PLUGINHOST_HELPER set the test
// binary speaks the protocol, so the host runs a real process group.
func TestMain(m *testing.M) {
	if mode := os.Getenv("PLUGINHOST_HELPER"); mode != "" {
		helperPlugin(mode)
		return
	}
	startBudget = 3 * time.Second
	probeEvery = 150 * time.Millisecond
	probeTimeout = 150 * time.Millisecond
	firstBackoff = 100 * time.Millisecond
	stopGrace = 500 * time.Millisecond
	scanInterval = 100 * time.Millisecond
	trialSettle = 300 * time.Millisecond
	checkFirst = time.Hour
	os.Exit(m.Run())
}

func helperPlugin(mode string) {
	fmt.Fprintln(os.Stderr, "helper", mode, "starting")
	if mode == "exit" {
		os.Exit(3)
	}
	if mode == "stubborn" {
		signal.Ignore(syscall.SIGTERM)
	}
	listener, err := net.Listen("unix", os.Getenv("DEV_COCKPIT_PLUGIN_SOCKET"))
	if err != nil {
		os.Exit(4)
	}
	if mode == "garbage" {
		for {
			conn, err := listener.Accept()
			if err != nil {
				os.Exit(5)
			}
			_, _ = conn.Write([]byte("this is not HTTP\r\n\r\n"))
			_ = conn.Close()
		}
	}
	cockpit := &http.Client{Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", os.Getenv("DEV_COCKPIT_SOCKET"))
	}}}
	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"path": r.URL.Path, "body": string(body), "cookie": r.Header.Get("Cookie"), "auth": r.Header.Get("Authorization"),
			"csrf": r.Header.Get("X-CSRF-Token"), "prefix": r.Header.Get("X-Forwarded-Prefix"),
		})
	})
	mux.HandleFunc("/cookie", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "tc_session", Value: "stolen"})
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/listen", func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequest(http.MethodGet, "http://cockpit/v1/events", nil)
		req.Header.Set("Authorization", "Bearer "+os.Getenv("DEV_COCKPIT_TOKEN"))
		res, err := cockpit.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		line, err := bufio.NewReader(res.Body).ReadString('\n')
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(line))
	})
	mux.HandleFunc("/call", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		req, _ := http.NewRequest(q.Get("method"), "http://cockpit"+q.Get("path"), strings.NewReader(q.Get("body")))
		if q.Get("token") != "none" {
			req.Header.Set("Authorization", "Bearer "+os.Getenv("DEV_COCKPIT_TOKEN"))
		}
		res, err := cockpit.Do(req)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer res.Body.Close()
		w.WriteHeader(res.StatusCode)
		_, _ = io.Copy(w, res.Body)
	})
	_ = http.Serve(listener, mux)
}

// testHost is a host over a fresh state and projects directory.
type testHost struct {
	*Host
	stateDir    string
	projectsDir string
	changed     atomic.Int32
	released    atomic.Int32
}

func newTestHost(t *testing.T, compiled ...*Serve) *testHost {
	t.Helper()
	th := &testHost{stateDir: t.TempDir(), projectsDir: t.TempDir()}
	th.Host = NewHost(HostConfig{
		StateDir:    th.stateDir,
		ProjectsDir: th.projectsDir,
		Compiled:    compiled,
		CreateProject: func(ctx context.Context, name string) (string, error) {
			dir := filepath.Join(th.projectsDir, name)
			if err := os.Mkdir(dir, 0o755); err != nil {
				return "", plugin.ErrProjectExists
			}
			return dir, nil
		},
		ProjectChanged: func(ctx context.Context) error { th.changed.Add(1); return nil },
		CSRFField:      "csrf_token",
	})
	t.Cleanup(func() {
		th.Close()
		_ = os.RemoveAll(runRoot(th.stateDir))
	})
	return th
}

func (th *testHost) start() {
	th.Scan()
	th.Start(Hooks{Release: func(string) { th.released.Add(1) }})
}

// writePlugin writes a plugin directory into folder, its run file starts
// this test binary in the given helper mode.
func writePlugin(t *testing.T, folder, dir, mode string, change func(m map[string]any)) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, dir)
	if err := os.MkdirAll(filepath.Join(path, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := map[string]any{
		"protocol": 1, "id": dir, "name": "Demo", "version": "1.0.0", "run": "run.sh", "assets": "assets",
		"elements": []map[string]string{{"name": "button", "module": "button.js"}},
		"slots":    []map[string]any{{"slot": plugin.SlotProjectsActions, "html": "slot.html"}},
		"page":     map[string]any{"title": "Demo page", "icon": "ti-box", "html": "page.html"},
	}
	if change != nil {
		change(m)
	}
	raw, _ := json.Marshal(m)
	files := map[string]string{
		ManifestFile:        string(raw),
		"run.sh":            fmt.Sprintf("#!/bin/sh\nPLUGINHOST_HELPER=%s exec '%s'\n", mode, exe),
		"assets/button.js":  "export default class extends HTMLElement {}",
		"assets/helper.txt": "x",
		"slot.html":         `<` + dir + `-button data-url="{{base}}/echo"></` + dir + `-button>`,
		"page.html":         `<p>Demo page</p>`,
	}
	for name, content := range files {
		mode := os.FileMode(0o644)
		if name == "run.sh" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(path, name), []byte(content), mode); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

func waitState(t *testing.T, h *testHost, id string, want State) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, ok := h.Status(id)
		if ok && st.State == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("plugin %s stands %q (%s), want %q", id, st.State, st.Reason, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitFolderState waits for the folder's entry of an id, which a compiled
// plugin of the same id hides from Status.
func waitFolderState(t *testing.T, h *testHost, id string, want State) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var st Status
		for _, listed := range h.List() {
			if listed.ID == id && !listed.BuiltIn {
				st = listed
			}
		}
		if st.State == want {
			return st
		}
		if time.Now().After(deadline) {
			t.Fatalf("the folder's %s stands %q (%s), want %q", id, st.State, st.Reason, want)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func proxied(t *testing.T, s *Serve, r *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	http.StripPrefix("/plugins/"+s.ID(), s.Handler()).ServeHTTP(rec, r)
	return rec
}

// A running plugin answers its subtree through the proxy without any cockpit
// credential: no cookie, authorization or CSRF token in a header or the form,
// parsed or not, and no cookie of its own goes back.
func TestAProxiedRequestCarriesNoCockpitCredentials(t *testing.T) {
	h := newTestHost(t)
	writePlugin(t, h.Dir(), "demo", "ok", nil)
	h.start()
	waitState(t, h, "demo", StateRunning)
	s := NewSet(nil, h.Host).Lookup("demo")
	if s == nil || !s.Live() || !s.Running() {
		t.Fatalf("the set holds no running demo: %+v", s)
	}
	views := SlotMounts([]*Serve{s}, plugin.SlotProjectsActions, func(p string) string { return p })
	if len(views) != 1 || !strings.Contains(string(views[0].HTML), `<demo-button data-url="/plugins/demo/echo"></demo-button>`) || s.Page().HTML != "<p>Demo page</p>" {
		t.Fatalf("the plugin's markup did not arrive: %+v, page %q", views, s.Page().HTML)
	}

	form := url.Values{"csrf_token": {"secret"}, "note": {"kept"}}
	r := httptest.NewRequest(http.MethodPost, "/plugins/demo/echo", strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Cookie", "tc_session=abc")
	r.Header.Set("Authorization", "Basic x")
	r.Header.Set("X-CSRF-Token", "secret")
	parsed := r.Clone(r.Context())
	parsed.Body = io.NopCloser(strings.NewReader(form.Encode()))
	if err := parsed.ParseForm(); err != nil {
		t.Fatal(err)
	}
	var got map[string]string
	for _, req := range []*http.Request{r, parsed} {
		rec := proxied(t, s, req)
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatalf("echo answered %d %q", rec.Code, rec.Body.String())
		}
		if got["cookie"] != "" || got["auth"] != "" || got["csrf"] != "" || strings.Contains(got["body"], "secret") || got["body"] != "note=kept" {
			t.Fatalf("the plugin saw credentials: %+v", got)
		}
	}
	if got["path"] != "/echo" || got["prefix"] != "/plugins/demo" {
		t.Fatalf("the plugin saw path %q under prefix %q", got["path"], got["prefix"])
	}
	rec := proxied(t, s, httptest.NewRequest(http.MethodGet, "/plugins/demo/cookie", nil))
	if rec.Code != http.StatusOK || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("the plugin's cookie reached the browser: %d %q", rec.Code, rec.Header().Get("Set-Cookie"))
	}
}

// The callback socket serves the readers and the project surface, only with
// the token the process got at start.
func TestTheCallbackAnswersOnlyThePluginsToken(t *testing.T) {
	h := newTestHost(t)
	writePlugin(t, h.Dir(), "demo", "ok", nil)
	h.start()
	waitState(t, h, "demo", StateRunning)
	s := NewSet(nil, h.Host).Lookup("demo")
	call := func(method, path, body, token string) (int, string) {
		q := url.Values{"method": {method}, "path": {path}, "body": {body}, "token": {token}}
		rec := proxied(t, s, httptest.NewRequest(http.MethodGet, "/plugins/demo/call?"+q.Encode(), nil))
		return rec.Code, rec.Body.String()
	}
	if code, body := call("GET", "/v1/plugin", "", "none"); code != http.StatusUnauthorized {
		t.Fatalf("a call without the token answered %d %s", code, body)
	}
	code, body := call("GET", "/v1/plugin", "", "")
	var readers map[string]string
	_ = json.Unmarshal([]byte(body), &readers)
	if code != http.StatusOK || readers["id"] != "demo" || readers["projects_dir"] != h.projectsDir || readers["state_dir"] != h.stateDir || readers["plugin_state_dir"] != filepath.Join(h.stateDir, "plugins", "demo") {
		t.Fatalf("the readers answered %d %s", code, body)
	}
	if code, body := call("POST", "/v1/projects", `{"name":"made"}`, ""); code != http.StatusCreated || !strings.Contains(body, filepath.Join(h.projectsDir, "made")) {
		t.Fatalf("create answered %d %s", code, body)
	}
	if code, body := call("POST", "/v1/projects", `{"name":"made"}`, ""); code != http.StatusConflict || !strings.Contains(body, plugin.ErrProjectExists.Error()) {
		t.Fatalf("a second create answered %d %s", code, body)
	}
	if code, body := call("POST", "/v1/projects/made/changed", "", ""); code != http.StatusNoContent || h.changed.Load() != 1 {
		t.Fatalf("changed answered %d %s, announced %d times", code, body, h.changed.Load())
	}
	if code, _ := call("POST", "/v1/projects/..%2Fetc/changed", "", ""); code != http.StatusNotFound {
		t.Fatalf("a changed for a path that leaves the root answered %d", code)
	}
	info, err := os.Stat(filepath.Dir(filepath.Join(runRoot(h.stateDir), "demo", "c")))
	if err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("the socket directory is not the owner's alone: %v %v", info.Mode(), err)
	}
}

// A plugin that does not start shows why with its last lines, its routes
// answer 503 without waiting, its slot stays empty and it is restarted.
func TestACrashingPluginShowsWhyAndLeavesItsSlotEmpty(t *testing.T) {
	h := newTestHost(t)
	writePlugin(t, h.Dir(), "demo", "exit", nil)
	h.start()
	st := waitState(t, h, "demo", StateCrashed)
	if !strings.Contains(st.Reason, "status 3") || !strings.Contains(strings.Join(st.Tail, "\n"), "helper exit starting") {
		t.Fatalf("the crash says %q with %q", st.Reason, st.Tail)
	}
	set := NewSet(nil, h.Host)
	if views := SlotMounts(set.Serves(), plugin.SlotProjectsActions, func(p string) string { return p }); len(views) != 0 {
		t.Fatalf("a crashed plugin mounts %d elements", len(views))
	}
	began := time.Now()
	rec := proxied(t, set.Lookup("demo"), httptest.NewRequest(http.MethodGet, "/plugins/demo/echo", nil))
	if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "Demo is not running.") || time.Since(began) > 100*time.Millisecond {
		t.Fatalf("a crashed plugin's route answered %d %q after %s", rec.Code, rec.Body.String(), time.Since(began))
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		lines, _ := h.Log("demo")
		if strings.Count(strings.Join(lines, "\n"), "helper exit starting") >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the plugin was not started again: %q", lines)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A process that speaks something other than HTTP never counts as running.
func TestGarbageOnTheSocketIsNeverRunning(t *testing.T) {
	h := newTestHost(t)
	writePlugin(t, h.Dir(), "demo", "garbage", nil)
	h.start()
	st := waitState(t, h, "demo", StateCrashed)
	if !strings.Contains(st.Reason, "did not answer HTTP") {
		t.Fatalf("the plugin crashed with %q", st.Reason)
	}
}

// A plugin that stops answering is ended with its process group and
// restarted.
func TestAHungPluginIsEndedAndStartedAgain(t *testing.T) {
	h := newTestHost(t)
	writePlugin(t, h.Dir(), "demo", "ok", nil)
	h.start()
	waitState(t, h, "demo", StateRunning)
	group := pidOf(t, h, "demo")
	if err := syscall.Kill(-group, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		st, _ := h.Status("demo")
		if st.State == StateRunning && pidOf(t, h, "demo") != group {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the hung plugin stands %q (%s)", st.State, st.Reason)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if syscall.Kill(group, 0) == nil {
		t.Fatal("the hung process still lives")
	}
}

func pidOf(t *testing.T, h *testHost, id string) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(runRoot(h.stateDir), id, "pid"))
	if err != nil {
		return 0
	}
	var pid int
	_, _ = fmt.Sscanf(string(raw), "%d", &pid)
	return pid
}

// Off ends the process group and takes the plugin out of every surface, on
// brings it back, and remove deletes its directory.
func TestDisableEnableAndRemove(t *testing.T) {
	h := newTestHost(t)
	writePlugin(t, h.Dir(), "demo", "ok", nil)
	h.start()
	waitState(t, h, "demo", StateRunning)
	group := pidOf(t, h, "demo")
	version := h.Version()
	if err := h.SetEnabled("demo", false); err != nil {
		t.Fatal(err)
	}
	waitState(t, h, "demo", StateDisabled)
	if len(h.Serves()) != 0 || h.released.Load() != 1 || syscall.Kill(-group, 0) == nil || h.Version() == version {
		t.Fatalf("a disabled plugin stands: %d serves, %d releases, version %s", len(h.Serves()), h.released.Load(), h.Version())
	}
	raw, _ := os.ReadFile(enabledPath(h.stateDir))
	if !strings.Contains(string(raw), `"demo"`) {
		t.Fatalf("the switch is not kept: %s", raw)
	}
	if err := h.SetEnabled("demo", true); err != nil {
		t.Fatal(err)
	}
	waitState(t, h, "demo", StateRunning)
	if err := h.Remove("demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.Dir(), "demo")); !errors.Is(err, os.ErrNotExist) || len(h.List()) != 0 {
		t.Fatalf("the removed plugin is still there: %v, %d listed", err, len(h.List()))
	}
	if err := h.Remove("demo"); !errors.Is(err, ErrUnknownPlugin) {
		t.Fatalf("a second remove answered %v", err)
	}
}

// A plugin that ends on SIGTERM stops without waiting for the grace, one
// that ignores it is killed when the grace is up.
func TestAStopTakesAsLongAsThePluginNeeds(t *testing.T) {
	defer func(grace time.Duration) { stopGrace = grace }(stopGrace)
	stopGrace = 5 * time.Second
	h := newTestHost(t)
	writePlugin(t, h.Dir(), "demo", "ok", nil)
	writePlugin(t, h.Dir(), "stubborn", "stubborn", nil)
	h.start()
	waitState(t, h, "demo", StateRunning)
	waitState(t, h, "stubborn", StateRunning)
	timed := func(what string, change func() error) {
		t.Helper()
		began := time.Now()
		if err := change(); err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		if took := time.Since(began); took > time.Second {
			t.Fatalf("%s took %s for a plugin that ends at once", what, took)
		}
	}
	timed("restart", func() error { return h.Restart("demo") })
	waitState(t, h, "demo", StateRunning)
	timed("disable", func() error { return h.SetEnabled("demo", false) })
	timed("enable", func() error { return h.SetEnabled("demo", true) })
	waitState(t, h, "demo", StateRunning)
	timed("remove", func() error { return h.Remove("demo") })

	stopGrace = 300 * time.Millisecond
	group := pidOf(t, h, "stubborn")
	began := time.Now()
	if err := h.SetEnabled("stubborn", false); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(began); took < stopGrace || took > 3*time.Second {
		t.Fatalf("a plugin that ignores SIGTERM was stopped after %s, the grace is %s", took, stopGrace)
	}
	if syscall.Kill(-group, 0) == nil {
		t.Fatal("the stubborn plugin's group still lives")
	}
}

// A directory dropped into the folder is picked up while the cockpit runs.
func TestADroppedDirectoryIsPickedUpLive(t *testing.T) {
	h := newTestHost(t)
	h.start()
	writePlugin(t, h.Dir(), "late", "ok", nil)
	waitState(t, h, "late", StateRunning)
}

// No two plugins share an id, neither with a compiled one nor through a
// manifest id that differs from its directory.
func TestExternalIDsNeverCollide(t *testing.T) {
	compiled := configured(t, plugin.Named[plugin.ServePlugin]{ID: "taken", Plugin: &testPlugin{}})
	h := newTestHost(t, compiled...)
	writePlugin(t, h.Dir(), "taken", "ok", nil)
	writePlugin(t, h.Dir(), "other", "ok", func(m map[string]any) { m["id"] = "own" })
	h.start()
	if st := waitFolderState(t, h, "taken", StateBroken); !strings.Contains(st.Reason, "built in plugin") {
		t.Fatalf("the clash with a compiled id says %q", st.Reason)
	}
	if st, _ := h.Status("taken"); !st.BuiltIn || st.State != StateRunning {
		t.Fatalf("the id answers %+v, want the compiled plugin", st)
	}
	if st := waitState(t, h, "other", StateBroken); !strings.Contains(st.Reason, "but the manifest id is own") {
		t.Fatalf("the misnamed directory says %q", st.Reason)
	}
	if len(NewSet(compiled, h.Host).Serves()) != 1 {
		t.Fatal("a broken plugin joined the set")
	}
}

// An asset that resolves outside the plugin directory marks the plugin
// broken before any of it is served.
func TestAnAssetCannotLeaveThePluginDirectory(t *testing.T) {
	h := newTestHost(t)
	dir := writePlugin(t, h.Dir(), "demo", "ok", nil)
	if err := os.Symlink("/etc/passwd", filepath.Join(dir, "assets", "passwd")); err != nil {
		t.Fatal(err)
	}
	h.start()
	if st := waitState(t, h, "demo", StateBroken); !strings.Contains(st.Reason, "leaves the plugin directory") {
		t.Fatalf("the escaping asset says %q", st.Reason)
	}
}

// Install copies a host directory into the folder and starts it, a second
// install from the same path updates it, invalid input is refused with a
// sentence.
func TestInstallCopiesAndUpdates(t *testing.T) {
	compiled := configured(t, plugin.Named[plugin.ServePlugin]{ID: "taken", Plugin: &testPlugin{}})
	h := newTestHost(t, compiled...)
	h.start()
	source := writePlugin(t, t.TempDir(), "demo", "ok", nil)
	if err := os.MkdirAll(filepath.Join(source, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	id, replaced, err := h.Install(source, false)
	if err != nil || id != "demo" || replaced {
		t.Fatalf("Install() = %q %v %v", id, replaced, err)
	}
	waitState(t, h, "demo", StateRunning)
	if _, err := os.Stat(filepath.Join(h.Dir(), "demo", ".git")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("the .git directory was copied")
	}
	if _, replaced, err := h.Install(source, false); err != nil || !replaced {
		t.Fatalf("a second install answered %v %v", replaced, err)
	}
	waitState(t, h, "demo", StateRunning)
	for _, tc := range []struct{ path, want string }{
		{"", "Enter the plugin's directory"},
		{"relative/dir", "absolute path"},
		{filepath.Join(source, "missing"), "no directory"},
		{filepath.Join(h.Dir(), "demo"), "plugins folder"},
		{writePlugin(t, t.TempDir(), "taken", "ok", nil), "built in plugin"},
		{writePlugin(t, t.TempDir(), "bad", "ok", func(m map[string]any) { m["run"] = "../escape.sh" }), "relative path inside"},
	} {
		if _, _, err := h.Install(tc.path, false); !IsInstallError(err) || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("Install(%q) = %v, want a refusal with %q", tc.path, err, tc.want)
		}
	}
}

// Sweep ends a group an earlier cockpit left running only when the pid
// file's nonce proves it is the plugin's.
func TestSweepEndsOnlyWhatAPidFileProves(t *testing.T) {
	h := newTestHost(t)
	exe, _ := os.Executable()
	runDir, err := h.runDir("demo")
	if err != nil {
		t.Fatal(err)
	}
	start := func(nonce string) int {
		cmd := startHelper(t, exe, "DEV_COCKPIT_PLUGIN_SOCKET="+filepath.Join(runDir, "p-"+nonce), runEnv+"="+nonce)
		return cmd
	}
	ours := start("ours")
	if err := os.WriteFile(filepath.Join(runDir, "pid"), []byte(fmt.Sprintf("%d ours\n", ours)), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Sweep()
	waitGone(t, ours)

	stranger := start("stranger")
	t.Cleanup(func() { _ = syscall.Kill(-stranger, syscall.SIGKILL) })
	if err := os.WriteFile(filepath.Join(runDir, "pid"), []byte(fmt.Sprintf("%d ours\n", stranger)), 0o600); err != nil {
		t.Fatal(err)
	}
	h.Sweep()
	time.Sleep(200 * time.Millisecond)
	if syscall.Kill(stranger, 0) != nil {
		t.Fatal("the sweep ended a process whose nonce is not the pid file's")
	}
}

// After a self-update a plugin the old image started is an unwaited child of
// this process. Sweep reaps it instead of waiting out the grace on its
// zombie.
func TestSweepReapsItsOwnChildAtOnce(t *testing.T) {
	h := newTestHost(t)
	exe, _ := os.Executable()
	runDir, err := h.runDir("demo")
	if err != nil {
		t.Fatal(err)
	}
	attr := &os.ProcAttr{Env: append(os.Environ(), "PLUGINHOST_HELPER=ok", "DEV_COCKPIT_PLUGIN_SOCKET="+filepath.Join(runDir, "p"), runEnv+"=lost"), Sys: &syscall.SysProcAttr{Setpgid: true}}
	p, err := os.StartProcess(exe, []string{exe}, attr)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "pid"), []byte(fmt.Sprintf("%d lost\n", p.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	began := time.Now()
	h.Sweep()
	if took := time.Since(began); took >= stopGrace {
		t.Fatalf("the sweep took %s, the whole grace", took)
	}
	if err := syscall.Kill(p.Pid, 0); err == nil {
		t.Fatal("the sweep left the plugin or its zombie")
	}
}

func startHelper(t *testing.T, exe string, env ...string) int {
	t.Helper()
	attr := &os.ProcAttr{Env: append(os.Environ(), append(env, "PLUGINHOST_HELPER=ok")...), Sys: &syscall.SysProcAttr{Setpgid: true}}
	p, err := os.StartProcess(exe, []string{exe}, attr)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = p.Wait() }()
	return p.Pid
}

func waitGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("process %d still lives", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
