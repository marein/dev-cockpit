package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/project"
)

// gitMenuClone makes a clone of a fresh source repository, the shape of a
// project whose branch follows a remote: master tracks origin/master and the
// two stand level.
func gitMenuClone(t *testing.T) (origin, app string) {
	t.Helper()
	root := t.TempDir()
	origin = worktreeSourceRepo(t, t.TempDir(), "origin")
	worktreeGit(t, root, "clone", "-q", origin, "app")
	app = filepath.Join(root, "app")
	worktreeGit(t, app, "config", "user.email", "t@example.com")
	worktreeGit(t, app, "config", "user.name", "t")
	worktreeGit(t, app, "config", "commit.gpgsign", "false")
	return origin, app
}

// The flash after a fetch from the projects page says where the branch stands
// now, in every shape the distance can take, and only where there is one.
func TestFetchedMessageWordsTheDistanceToTheUpstream(t *testing.T) {
	ctx := context.Background()
	origin, app := gitMenuClone(t)
	p := project.Project{Name: "app", Path: app, GitBranch: "master"}

	if got := fetchedMessage(ctx, p, true); got != `Fetched "app". "master" is up to date with "origin/master".` {
		t.Fatalf("level with the upstream reads %q", got)
	}

	worktreeGit(t, origin, "commit", "-q", "--allow-empty", "-m", "one")
	worktreeGit(t, origin, "commit", "-q", "--allow-empty", "-m", "two")
	worktreeGit(t, app, "fetch", "-q")
	if got := fetchedMessage(ctx, p, true); got != `Fetched "app". "master" is 2 commits behind "origin/master".` {
		t.Fatalf("purely behind reads %q", got)
	}

	worktreeGit(t, app, "commit", "-q", "--allow-empty", "-m", "mine")
	if got := fetchedMessage(ctx, p, true); got != `Fetched "app". "master" has diverged from "origin/master": 1 commit ahead, 2 commits behind.` {
		t.Fatalf("diverged reads %q", got)
	}

	_, ahead := gitMenuClone(t)
	worktreeGit(t, ahead, "commit", "-q", "--allow-empty", "-m", "mine")
	if got := fetchedMessage(ctx, project.Project{Name: "app", Path: ahead, GitBranch: "master"}, true); got != `Fetched "app". "master" is 1 commit ahead of "origin/master".` {
		t.Fatalf("purely ahead reads %q", got)
	}
}

// A branch that follows nothing, a detached HEAD and a repository without a
// remote have no distance to word, and the flash says only what happened.
func TestFetchedMessageStopsWhereThereIsNoDistance(t *testing.T) {
	ctx := context.Background()
	_, app := gitMenuClone(t)

	worktreeGit(t, app, "checkout", "-q", "-b", "lonely")
	if got := fetchedMessage(ctx, project.Project{Name: "app", Path: app, GitBranch: "lonely"}, true); got != `Fetched "app".` {
		t.Fatalf("a branch without an upstream reads %q", got)
	}

	head := strings.TrimSpace(gitOutput(t, app, "rev-parse", "--short", "HEAD"))
	if got := fetchedMessage(ctx, project.Project{Name: "app", Path: app, GitBranch: head}, true); got != `Fetched "app".` {
		t.Fatalf("a detached head reads %q", got)
	}

	if got := fetchedMessage(ctx, project.Project{Name: "app", Path: app, GitBranch: "master"}, false); got != `Nothing to fetch, "app" has no remote.` {
		t.Fatalf("a repository without a remote reads %q", got)
	}
}

// The editor opens on the two views the git menu links into and on nothing
// else: a stale or hand written value is the plain editor, not an error.
func TestEditorViewKeepsOnlyTheTwoViews(t *testing.T) {
	for raw, want := range map[string]string{
		"commit":    "commit",
		" compare ": "compare",
		"":          "",
		"files":     "",
		"COMMIT":    "",
	} {
		if got := editorView(raw); got != want {
			t.Errorf("editorView(%q) = %q, want %q", raw, got, want)
		}
	}
}

// The fetch route answers the flag the resync acts on and the sentence the
// projects page toasts, one wording out of one call, for a repository that
// fetched and for one with nothing to fetch from.
func TestProjectFetchAnswersTheSentenceBesideTheFlag(t *testing.T) {
	gin.SetMode(gin.TestMode)
	origin, app := gitMenuClone(t)
	worktreeGit(t, origin, "commit", "-q", "--allow-empty", "-m", "one")
	worktreeGit(t, origin, "commit", "-q", "--allow-empty", "-m", "two")
	s := &Server{
		projects:  project.NewRepository(filepath.Dir(app), nil),
		gitWrites: newGitWrites(),
		bus:       eventbus.New(),
	}
	r := gin.New()
	r.POST("/projects/:name/fetch", s.handleProjectFetch)
	post := func() (int, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/projects/app/fetch", strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		r.ServeHTTP(rec, req)
		var body map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &body)
		return rec.Code, body
	}

	code, body := post()
	if code != http.StatusOK || body["fetched"] != true {
		t.Fatalf("a repository with a remote answered %d %v", code, body)
	}
	if body["message"] != `Fetched "app". "master" is 2 commits behind "origin/master".` {
		t.Fatalf("the sentence reads %q", body["message"])
	}

	worktreeGit(t, app, "remote", "remove", "origin")
	code, body = post()
	if code != http.StatusOK || body["fetched"] != false {
		t.Fatalf("a repository without a remote answered %d %v", code, body)
	}
	if body["message"] != `Nothing to fetch, "app" has no remote.` {
		t.Fatalf("the sentence reads %q", body["message"])
	}
}
