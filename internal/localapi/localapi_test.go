package localapi

import (
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// serve binds the socket of a state directory and answers every request with
// handler, the way the cockpit serves its own routes over it.
func serve(t *testing.T, stateDir string, handler http.HandlerFunc) {
	t.Helper()
	listener, err := Listen(stateDir)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
}

func TestPostFormReachesTheCockpit(t *testing.T) {
	dir := t.TempDir()
	var gotPath, gotField string
	serve(t, dir, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotField = r.FormValue("terminal")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"a coder"}`))
	})

	client, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	answer, err := client.PostForm("/coders/x/stop", url.Values{"terminal": {"x"}}, time.Second)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if gotPath != "/coders/x/stop" || gotField != "x" {
		t.Fatalf("the request arrived as %q with terminal %q", gotPath, gotField)
	}
	if answer["name"] != "a coder" {
		t.Fatalf("read back %v", answer)
	}
}

// A refusal is an answer: the command says what the page would have flashed.
func TestPostReportsTheRefusalSentence(t *testing.T) {
	dir := t.TempDir()
	serve(t, dir, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"No running coder with that id."}`))
	})

	client, _ := Dial(dir)
	_, err := client.PostJSON("/coders/x/input", map[string]any{"items": []any{}}, time.Second)
	if err == nil || err.Error() != "No running coder with that id." {
		t.Fatalf("want the handler's sentence, got %v", err)
	}
}

// The directory carries the permission, so the socket cannot be reached by
// anybody who may not enter it.
func TestListenKeepsTheSocketPrivate(t *testing.T) {
	dir := t.TempDir()
	serve(t, dir, func(http.ResponseWriter, *http.Request) {})

	info, err := os.Stat(filepath.Dir(SocketPath(dir)))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Fatalf("the socket directory is the credential, want 0700, got %o", mode)
	}
}

// A socket file a killed process left behind listens to nobody, so binding has
// to replace it instead of refusing to start.
func TestListenReplacesADeadSocket(t *testing.T) {
	dir := t.TempDir()
	serve(t, dir, func(http.ResponseWriter, *http.Request) {})
	serve(t, dir, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":"second"}`))
	})

	client, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	answer, err := client.PostForm("/", url.Values{}, time.Second)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if answer["ok"] != "second" {
		t.Fatalf("the second process owns the socket, got %v", answer)
	}
}

func TestDialWithoutARunningCockpit(t *testing.T) {
	// A spent budget answers with the same message one attempt used to; zero
	// budget means one attempt, so this test does not sleep the real one out.
	t.Setenv(dialBudgetEnv, "0s")
	_, err := Dial(t.TempDir())
	if err == nil {
		t.Fatal("want a readable error when no cockpit is listening")
	}
	if !strings.Contains(err.Error(), "No running cockpit") {
		t.Fatalf("want the old message, got %q", err.Error())
	}
}

// lateCockpit binds the socket of a state directory after a delay, the way the
// new process of a self-update comes back: for a moment the socket is missing
// or refuses, then it answers again.
func lateCockpit(t *testing.T, stateDir string, delay time.Duration) {
	t.Helper()
	started := make(chan *http.Server, 1)
	go func() {
		time.Sleep(delay)
		listener, err := Listen(stateDir)
		if err != nil {
			close(started)
			return
		}
		server := &http.Server{Handler: func() http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"ok":"back"}`))
			}
		}()}
		started <- server
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		if server, ok := <-started; ok {
			_ = server.Close()
		}
	})
}

// A call that falls into the restart window of a self-update waits the socket
// out instead of failing at once: first the file is missing entirely, and the
// cockpit that binds a moment later is still reached.
func TestDialWaitsForASocketThatIsStillMissing(t *testing.T) {
	t.Setenv(dialBudgetEnv, "10s")
	dir := t.TempDir()
	lateCockpit(t, dir, 300*time.Millisecond)

	client, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	answer, err := client.PostForm("/", url.Values{}, time.Second)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if answer["ok"] != "back" {
		t.Fatalf("read back %v", answer)
	}
}

// The other half of the window: the socket file of the old process is still
// there, nobody listens on it, and the connection is refused until the new
// process replaces it.
func TestDialWaitsOutARefusingSocket(t *testing.T) {
	t.Setenv(dialBudgetEnv, "10s")
	dir := t.TempDir()
	listener, err := Listen(dir)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// Closing normally unlinks the file; a killed process leaves it behind,
	// which is the case this reproduces.
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = listener.Close()
	lateCockpit(t, dir, 300*time.Millisecond)

	client, err := Dial(dir)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	answer, err := client.PostForm("/", url.Values{}, time.Second)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if answer["ok"] != "back" {
		t.Fatalf("read back %v", answer)
	}
}

// A state directory is wherever the user pointed --state-dir, and a socket
// address has a hard length limit. A path that does not fit moves somewhere
// short instead of refusing to start, and both sides still find it.
func TestALongStateDirStillGetsASocket(t *testing.T) {
	long := filepath.Join(t.TempDir(), strings.Repeat("a-long-directory-name/", 6))
	if err := os.MkdirAll(long, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if path := SocketPath(long); len(path) > maxSocketPath {
		t.Fatalf("the socket path is still %d characters: %s", len(path), path)
	}

	serve(t, long, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":"yes"}`))
	})
	client, err := Dial(long)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	answer, err := client.PostForm("/", url.Values{}, time.Second)
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	if answer["ok"] != "yes" {
		t.Fatalf("read back %v", answer)
	}
}

// Two spellings of one state directory are one cockpit: a command may name it
// relative or with a ~, the server has already expanded it.
func TestTheSocketDoesNotDependOnTheSpelling(t *testing.T) {
	dir := t.TempDir()
	spelled := filepath.Join(dir, "sub", "..", "sub", "")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if SocketPath(spelled) != SocketPath(filepath.Join(dir, "sub")) {
		t.Fatalf("%q and %q resolve to different sockets", spelled, filepath.Join(dir, "sub"))
	}
}
