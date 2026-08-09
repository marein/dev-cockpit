package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/local/dev-cockpit/internal/eventbus"
	"github.com/local/dev-cockpit/internal/project"
)

// gitWriteServer builds a server over a throwaway projects root holding one
// project, with the one write route that refuses before it ever reaches git.
// The branch route is the cheapest of them: it takes the working copy first
// and everything after that is git's, so a refusal proves the lock and
// nothing else.
func gitWriteServer(t *testing.T) (*gin.Engine, *Server, string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	projectDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	s := &Server{
		projects:  project.NewRepository(root, nil),
		gitWrites: newGitWrites(),
		// A write that reaches git and succeeds publishes its event.
		bus: eventbus.New(),
	}
	r := gin.New()
	r.POST("/projects/:name/editor/git/branch", s.handleEditorGitBranch)
	return r, s, projectDir
}

func postBranch(t *testing.T, r *gin.Engine) (int, string) {
	t.Helper()
	return postBranchTo(t, r, "demo")
}

func postBranchTo(t *testing.T, r *gin.Engine, projectName string) (int, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/"+projectName+"/editor/git/branch",
		strings.NewReader(`{"branch":"wip"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	var body struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Error
}

// A working copy takes one write at a time, whichever page it came from. The
// editor's own lock lives in one page and cannot speak for the second tab, the
// phone beside the desktop, or the same project in another window, and two
// writes that pass each other there are one commit recording half of the old
// branch.
func TestGitWriteRefusesASecondWriteOnTheSameWorkingCopy(t *testing.T) {
	r, s, projectDir := gitWriteServer(t)

	// Somebody else is writing this working copy right now.
	if !s.gitWrites.try(projectDir) {
		t.Fatal("a fresh working copy must be free")
	}

	code, message := postBranch(t, r)

	if code != http.StatusConflict {
		t.Fatalf("a second write must be refused, got %d", code)
	}
	if message != gitInUse {
		t.Fatalf("the refusal has to name the repository, got %q", message)
	}
}

// The lock is on the working copy and not on the project, and a project below
// the repository root is where the two part ways: two projects inside one
// repository are one checkout, so a commit in one and a checkout in the other
// are exactly the pair the lock exists to keep apart. Keyed by the project
// they would take two locks and run at each other again.
func TestGitWriteLocksTheWorkingCopyAcrossTwoProjectsOfOneRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	init := exec.Command("git", "init", "-q")
	init.Dir = root
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	for _, name := range []string{"one", "two"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{
		projects:  project.NewRepository(root, nil),
		gitWrites: newGitWrites(),
		// This one lets git through, so the write publishes its event.
		bus: eventbus.New(),
	}
	r := gin.New()
	r.POST("/projects/:name/editor/git/branch", s.handleEditorGitBranch)

	// Somebody is writing the repository right now, held under what the
	// working copy is called: its git directory, resolved the way git reports
	// it. Neither project's path appears in it.
	gitDir, err := filepath.EvalSymlinks(filepath.Join(root, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.gitWrites.try(gitDir) {
		t.Fatal("a fresh working copy must be free")
	}

	code, message := postBranchTo(t, r, "two")

	if code != http.StatusConflict || message != gitInUse {
		t.Fatalf("a write in the second project of the same repository must be refused, got %d %q", code, message)
	}

	// With the repository free again the write reaches git, and what it gives
	// back is the working copy's key: keyed by the project, the lock would be
	// released under a path nobody takes it under.
	s.gitWrites.release(gitDir)
	if code, message := postBranchTo(t, r, "one"); code != http.StatusOK {
		t.Fatalf("the released repository must let a write through, got %d %q", code, message)
	}
	if !s.gitWrites.try(gitDir) {
		t.Fatal("the handler kept the working copy after it answered")
	}
}

// A write whose working copy cannot be named at all is refused instead of
// guessing one. Guessing means the project path, and a second write that does
// reach git names the git directory: two names for one working copy, which is
// no lock. The refusal is its own, not the busy one, because nothing is
// running here, git simply did not answer.
func TestGitWriteRefusesWhenTheWorkingCopyCannotBeNamed(t *testing.T) {
	r, s, projectDir := gitWriteServer(t)
	// A PATH without git: the call cannot even be started, which is the same
	// ErrNoAnswer a stalled repository or a dropped request produces.
	t.Setenv("PATH", t.TempDir())

	code, message := postBranch(t, r)

	if code != http.StatusBadGateway {
		t.Fatalf("a write that cannot name its working copy must not run, got %d %q", code, message)
	}
	if message != gitUnknownCopy {
		t.Fatalf("the refusal has to name git and not the lock, got %q", message)
	}
	if !s.gitWrites.try(projectDir) {
		t.Fatal("the refused write left a lock behind")
	}
}

// The clone is the write that starts before the repository exists. git creates
// the `.git` in the first moments, so from then on every other write resolves
// a working copy the clone never held, and for the minutes the clone still
// runs it would walk straight past it. The project path is the name that is
// the same at both ends, so it is held too.
func TestGitWriteRefusesASecondWriteWhileACloneFillsTheProject(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	r, s, projectDir := gitWriteServer(t)

	// The clone took the project path, the only name there was when it
	// started.
	if !s.gitWrites.try(projectDir) {
		t.Fatal("a fresh project must be free")
	}
	// And now it is far enough along to have made the repository, which is
	// what a second write from another page resolves.
	init := exec.Command("git", "init", "-q")
	init.Dir = projectDir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}

	code, message := postBranch(t, r)

	if code != http.StatusConflict || message != gitInUse {
		t.Fatalf("a write during the clone must be refused, got %d %q", code, message)
	}

	// And when the clone is through, the project is free again.
	s.gitWrites.release(projectDir)
	if code, message := postBranch(t, r); code != http.StatusOK {
		t.Fatalf("the finished clone must leave the project free, got %d %q", code, message)
	}
}

// postQuietFetch drives the fetch the git surface runs on its way in.
func postQuietFetch(t *testing.T, r *gin.Engine) (int, bool) {
	t.Helper()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/projects/demo/editor/git/fetch",
		strings.NewReader(`{"auto":true}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	var body struct {
		Fetched bool `json:"fetched"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	return rec.Code, body.Fetched
}

// The fetch nobody asked for holds names of its own. Sharing the write lock
// made it the one thing on this surface that could refuse a commit: opening
// the git sheet against a remote that does not answer took the working copy
// for the whole fetch budget, and every push or commit of those minutes read
// "another git action is already running" while nothing on the page was
// running at all. It still meets itself, because two fetches fighting over
// the same refs is what it was yielding for.
func TestQuietFetchKeepsItsOwnLock(t *testing.T) {
	r, s, projectDir := gitWriteServer(t)
	r.POST("/projects/:name/editor/git/fetch", s.handleEditorGitFetch)

	quiet := quietFetchKeys([]string{projectDir})
	if !s.gitWrites.try(quiet...) {
		t.Fatal("a fresh working copy must be free")
	}

	// A write walks past a running quiet fetch.
	if code, message := postBranch(t, r); message == gitInUse {
		t.Fatalf("a quiet fetch must not refuse a write: %d %q", code, message)
	}
	// A second quiet fetch does not, and says nothing about it.
	if code, fetched := postQuietFetch(t, r); code != http.StatusOK || fetched {
		t.Fatalf("a second quiet fetch must yield without a word, got %d %v", code, fetched)
	}

	// And the other way round: a running write is not the quiet fetch's
	// business either, it writes remote refs and no file on disk.
	s.gitWrites.release(quiet...)
	if !s.gitWrites.try(projectDir) {
		t.Fatal("the quiet fetch took a write key")
	}
	if code, _ := postQuietFetch(t, r); code != http.StatusOK {
		t.Fatalf("the quiet fetch answered %d beside a write", code)
	}
}

// And it gives the working copy back: the next write reaches git, which
// refuses it for its own reason (this directory is no repository) in its own
// words, never in the lock's.
func TestGitWriteReleasesTheWorkingCopy(t *testing.T) {
	r, s, projectDir := gitWriteServer(t)

	if !s.gitWrites.try(projectDir) {
		t.Fatal("a fresh working copy must be free")
	}
	s.gitWrites.release(projectDir)

	code, message := postBranch(t, r)

	if message == gitInUse {
		t.Fatalf("the lock was not given back: %d %q", code, message)
	}
	if !s.gitWrites.try(projectDir) {
		t.Fatal("the handler kept the working copy after it answered")
	}
}
