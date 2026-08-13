package web

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/askpass"
	"github.com/local/dev-cockpit/internal/project"
)

// gitProxyServer builds a server over a throwaway projects root holding one
// project that is a repository with a first commit.
func gitProxyServer(t *testing.T) (*gin.Engine, *Server, string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-q"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "t"},
		{"config", "commit.gpgsign", "false"},
		{"commit", "-q", "--allow-empty", "-m", "init"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = projectDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	s := &Server{projects: project.NewRepository(root, nil)}
	r := gin.New()
	r.POST("/git", s.handleGitProxy)
	return r, s, projectDir
}

type gitProxyAnswer struct {
	ExitCode *int   `json:"exitCode"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Error    string `json:"error"`
}

// postGitProxy posts one proxied call the way the CLI does: the caller's own
// working directory plus the arguments, on the one path there is.
func postGitProxy(t *testing.T, r *gin.Engine, cwd string, args []string) (int, gitProxyAnswer) {
	t.Helper()
	payload, _ := json.Marshal(map[string]any{"cwd": cwd, "args": args})
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/git", strings.NewReader(string(payload)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	var answer gitProxyAnswer
	_ = json.Unmarshal(rec.Body.Bytes(), &answer)
	return rec.Code, answer
}

func decodeStream(t *testing.T, raw string) string {
	t.Helper()
	out, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		t.Fatalf("the stream is not base64: %v", err)
	}
	return string(out)
}

// The proxy promises git's own answer: stdout as git wrote it and the exit
// code as git decided it, a non-zero one included, which is a 200 and never
// an HTTP failure.
func TestProjectGitAnswersOutputAndExitCodeOneToOne(t *testing.T) {
	r, _, projectDir := gitProxyServer(t)

	code, answer := postGitProxy(t, r, projectDir, []string{"rev-parse", "--is-inside-work-tree"})
	if code != http.StatusOK || answer.ExitCode == nil || *answer.ExitCode != 0 {
		t.Fatalf("want 200 and exit 0, got %d, %+v", code, answer)
	}
	if got := strings.TrimSpace(decodeStream(t, answer.Stdout)); got != "true" {
		t.Fatalf("stdout %q", got)
	}

	code, answer = postGitProxy(t, r, projectDir, []string{"rev-parse", "--verify", "-q", "zz-nothing"})
	if code != http.StatusOK || answer.ExitCode == nil || *answer.ExitCode == 0 {
		t.Fatalf("a failing git call is still git's answer: got %d, %+v", code, answer)
	}
}

// This is a proxy and no project surface: a checkout outside every project,
// somebody's repository in /tmp, runs like any other. Refusing it would only
// send them back to the plain git that cannot ask for the passphrase. It is
// safe because -C, -c and --git-dir are refused, so the directory is the only
// thing that decides where git runs.
func TestProjectGitRunsOutsideEveryProject(t *testing.T) {
	r, _, _ := gitProxyServer(t)
	outside := t.TempDir()
	for _, args := range [][]string{{"init", "-q"}, {"commit", "-q", "--allow-empty", "-m", "x",
		"--author", "t <t@example.com>"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = outside
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git %v in the scratch dir: %v: %s", args, err, out)
		}
	}

	code, answer := postGitProxy(t, r, outside, []string{"rev-parse", "--is-inside-work-tree"})
	if code != http.StatusOK || answer.ExitCode == nil || *answer.ExitCode != 0 {
		t.Fatalf("a directory outside every project must run: got %d, %+v", code, answer)
	}
	if got := strings.TrimSpace(decodeStream(t, answer.Stdout)); got != "true" {
		t.Fatalf("git did not run in the directory that was sent: %q", got)
	}
}

// The one thing about the directory the route insists on: it has to be an
// absolute path, because everything downstream is built on it.
func TestProjectGitNeedsAnAbsoluteDirectory(t *testing.T) {
	r, _, _ := gitProxyServer(t)
	for _, cwd := range []string{"", "relative/path"} {
		code, answer := postGitProxy(t, r, cwd, []string{"status"})
		if code != http.StatusBadRequest {
			t.Fatalf("want 400 for %q, got %d", cwd, code)
		}
		if answer.Error != gitProxyNoDir {
			t.Fatalf("unexpected refusal for %q: %q", cwd, answer.Error)
		}
	}
}

// Whoever answers a passphrase for a proxied call is answering for a caller
// they cannot see, a terminal or a coding agent, so the question carries the
// command line the dialog shows. An argument holding a space stays one
// argument to read, and a runaway line is cut rather than handed to a dialog.
func TestCommandLineReadsLikeTheCallThatRuns(t *testing.T) {
	cases := []struct {
		args []string
		want string
	}{
		{[]string{"push"}, "git push"},
		{[]string{"push", "--force-with-lease"}, "git push --force-with-lease"},
		{[]string{"commit", "-m", "two words"}, `git commit -m "two words"`},
		{nil, "git"},
	}
	for _, c := range cases {
		if got := commandLine(c.args); got != c.want {
			t.Fatalf("commandLine(%v) = %q, want %q", c.args, got, c.want)
		}
	}
	// The dialog renders this block line by line, so an argument carrying a
	// line break could write lines of its own into it and forge the picture
	// somebody reads before handing over a passphrase.
	forged := commandLine([]string{"push", "origin\ncwd: /home/me/projects/trusted\n$ git push"})
	if strings.Contains(forged, "\n") || strings.Contains(forged, "\r") {
		t.Fatalf("an argument wrote its own lines into the dialog: %q", forged)
	}
	long := commandLine([]string{"commit", "-m", strings.Repeat("x", maxCommandLine*2)})
	if len([]rune(long)) > maxCommandLine+1 {
		t.Fatalf("a runaway command line reached the dialog whole: %d runes", len([]rune(long)))
	}
	if !strings.HasSuffix(long, "…") {
		t.Fatal("a cut command line has to say that it was cut")
	}
}

// A proxied line may describe a git operation and may not name what this
// server runs. The refusal happens before anything is started, and before the
// dialog is told what the action is called.
func TestProjectGitRefusesGitsOwnOptions(t *testing.T) {
	r, _, projectDir := gitProxyServer(t)

	for _, args := range [][]string{
		{"-c", "core.sshCommand=/tmp/evil", "push"},
		{"-C", "/elsewhere", "status"},
		{"fetch", "--upload-pack=/tmp/evil"},
	} {
		code, answer := postGitProxy(t, r, projectDir, args)
		if code != http.StatusBadRequest {
			t.Fatalf("%v: want 400, got %d", args, code)
		}
		if answer.Error == "" {
			t.Fatalf("%v: the refusal has to say what is wrong", args)
		}
	}
}

// The caller is one terminal and nobody else can answer for it. A coder that
// pressed Ctrl-C leaves a question nobody is behind, and holding it would hold
// the project's bridge for the two minutes a person would have had, with every
// editor write on that working copy refused meanwhile.
func TestProjectGitDropsTheQuestionWhenTheCallerIsGone(t *testing.T) {
	broker := askpass.New(t.TempDir())
	action := broker.BeginCommand("demo", "push", "git push", "/srv/projects/demo")
	if action == nil {
		t.Fatal("the bridge must open")
	}

	gone := make(chan struct{})
	stop := endWhenCallerGone(action, gone)
	if broker.Find("demo") == nil {
		t.Fatal("the action must stand while the caller waits")
	}
	close(gone)
	waitFor(t, "the caller's question is taken away with it", func() bool {
		return broker.Find("demo") == nil
	})
	stop()

	// A caller that stayed keeps its question: only the handler's own return
	// ends the action then.
	second := broker.BeginCommand("demo", "push", "git push", "/srv/projects/demo")
	if second == nil {
		t.Fatal("the bridge must open again once the first action ended")
	}
	endWhenCallerGone(second, make(chan struct{}))()
	time.Sleep(50 * time.Millisecond)
	if broker.Find("demo") == nil {
		t.Fatal("a caller that is still waiting must keep its question")
	}
	second.End()
}

// One asking action per project: while a bridged action stands, a second call
// is refused in the same words the editor's writes use, because two open
// bridges would interleave two dialogs.
func TestProjectGitRefusesWhileTheProjectAlreadyAsks(t *testing.T) {
	r, s, projectDir := gitProxyServer(t)
	broker := askpass.New(t.TempDir())
	s.askpassBroker = broker
	if broker.Begin("demo", "push") == nil {
		t.Fatal("the first bridge must open")
	}

	code, answer := postGitProxy(t, r, projectDir, []string{"status"})
	if code != http.StatusConflict {
		t.Fatalf("want 409 while the project asks, got %d", code)
	}
	if answer.Error != gitInUse {
		t.Fatalf("the refusal has to read like the editor's, got %q", answer.Error)
	}
}

// The scope is what the dialog label, the one-question-at-a-time bridge and
// the notification target hang on. A directory inside a project is scoped by
// the project name, which is what a person reads; anything else is scoped by
// its own path, so a checkout in /tmp gets the same guarantees under a name
// that still says where it is. The project resolution is the repository's own
// rule, which is also why the tilde bug cannot come back here: the repository
// resolves its root itself, there is no second spelling to drift.
func TestGitProxyScopeNamesTheProjectOrThePath(t *testing.T) {
	root := t.TempDir()
	deep := filepath.Join(root, "demo", "internal", "web")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{projects: project.NewRepository(root, nil)}

	for _, dir := range []string{filepath.Join(root, "demo"), deep} {
		gotDir, scope, ok := s.gitProxyScope(dir)
		if !ok || scope != "demo" || gotDir != dir {
			t.Fatalf("gitProxyScope(%q) = %q, %q, %v; want the dir and demo", dir, gotDir, scope, ok)
		}
	}
	// Outside every project the path is the scope, and it is still accepted:
	// a project name is one segment, a path scope starts with a separator, so
	// the two can never collide.
	outside := filepath.Dir(root)
	gotDir, scope, ok := s.gitProxyScope(outside)
	if !ok || scope != outside || gotDir != outside {
		t.Fatalf("gitProxyScope(%q) = %q, %q, %v; want the path as its own scope", outside, gotDir, scope, ok)
	}
	// Only a directory that is no absolute path at all is refused.
	for _, dir := range []string{"", "not/absolute", "   "} {
		if _, _, ok := s.gitProxyScope(dir); ok {
			t.Fatalf("gitProxyScope(%q) answered a scope", dir)
		}
	}
}
