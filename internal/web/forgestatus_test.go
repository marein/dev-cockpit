package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

const forgeStatusToken = "glpat-Xq3vR8sT2uW5yZ7aB9cD"

// addForgeHost adds the host in and gives it token, the way the settings add
// one and then ask for its token.
func addForgeHost(t *testing.T, f *forge.Service, in forge.Input, token string) forge.Host {
	t.Helper()
	h, err := f.Add(in)
	if err == nil {
		err = f.ChangeToken(h.ID, token, false)
	}
	if err != nil {
		t.Fatal(err)
	}
	return h
}

type forgeStatusAnswer struct {
	Project      string `json:"project"`
	Branch       string `json:"branch"`
	RemoteBranch string `json:"remoteBranch"`
	Head         string `json:"head"`
	Local        string `json:"local"`
	Forge        struct {
		Label, Address, URL, ChangeNoun string
	} `json:"forge"`
	Change *struct {
		Ref, Title, State, URL, Target, Head string
		Draft                                bool
	} `json:"change"`
	Pipeline *struct {
		ID, Status, URL string
		Partial         bool
		Jobs            []struct {
			ID, Name, Stage, Status, URL, NoLog string
			AllowFailure                        bool
		}
	} `json:"pipeline"`
	Logs []struct {
		ID, Name, Error string
		Lines           []string
		Cut             bool
	} `json:"logs"`
	LogsLeft int    `json:"logsLeft"`
	Error    string `json:"error"`
}

// fakeGitLabProject answers group/sub/app like GitLab: a merge request whose
// source is feature, standing at head, the pipeline 12 of head on feature
// with its one job, whose trace is coloured, a trigger job and three
// external statuses, all failed. It keeps the query of every request, and a
// branch named broken answers 500.
func fakeGitLabProject(head string, asked *[]string, mu *sync.Mutex) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		*asked = append(*asked, r.URL.EscapedPath()+"?"+r.URL.RawQuery)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+forgeStatusToken {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
			return
		}
		project := "/api/v4/projects/group%2Fsub%2Fapp"
		switch r.URL.EscapedPath() {
		case project + "/merge_requests":
			switch r.URL.Query().Get("source_branch") {
			case "feature":
				fmt.Fprintf(w, `[{"id":104,"iid":4,"project_id":2,"title":"Fix the login","state":"opened","target_branch":"master","source_branch":"feature","source_project_id":2,"target_project_id":2,"draft":true,"sha":%q,"web_url":"https://gitlab.example.com/group/sub/app/-/merge_requests/4"}]`, head)
			case "into-hidden":
				fmt.Fprintf(w, `[{"id":107,"iid":7,"project_id":2,"title":"Hide","state":"opened","target_branch":"rel\u009b2J","source_branch":"into-hidden","source_project_id":2,"target_project_id":2,"draft":false,"sha":%q,"web_url":"https://gitlab.example.com/group/sub/app/-/merge_requests/7"}]`, head)
			case "broken":
				w.WriteHeader(http.StatusInternalServerError)
				fmt.Fprint(w, `{"message":"500 Internal Server Error"}`)
			default:
				fmt.Fprint(w, `[]`)
			}
		case project + "/pipelines":
			fmt.Fprintf(w, `[{"id":12,"iid":3,"project_id":2,"sha":%q,"ref":"feature","status":"running","source":"push","web_url":"https://gitlab.example.com/group/sub/app/-/pipelines/12"}]`, head)
		case project + "/pipelines/12/jobs":
			fmt.Fprint(w, `[{"id":31,"status":"failed","stage":"test","name":"unit","allow_failure":false,"web_url":"https://gitlab.example.com/group/sub/app/-/jobs/31"}]`)
		case project + "/pipelines/12/bridges":
			fmt.Fprint(w, `[{"id":32,"status":"failed","stage":"deploy","name":"child","allow_failure":false,"web_url":"https://gitlab.example.com/group/sub/app/-/jobs/32","downstream_pipeline":null}]`)
		case project + "/repository/commits/" + head + "/statuses":
			statuses := []string{}
			for id := 31; id <= 35; id++ {
				statuses = append(statuses, fmt.Sprintf(`{"id":%d,"sha":%q,"ref":"feature","status":"failed","name":"ci/%d","target_url":null,"allow_failure":false,"pipeline_id":12}`, id, head, id))
			}
			fmt.Fprint(w, "["+strings.Join(statuses, ",")+"]")
		case project + "/jobs/31/trace":
			w.Header().Set("Content-Type", "text/plain")
			fmt.Fprint(w, "\x1b[32;1m$ go test ./...\x1b[0;m\n--- FAIL: TestLogin\nFAIL\n")
		default:
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprint(w, `{"message":"404 Project Not Found"}`)
		}
	})
}

// A project's forge status reads the change request and the pipeline of the
// commit origin's tracking ref names, never the local commit that is ahead
// of it, for HEAD's branch or the one asked; a branch never pushed is an
// answer of its own. What cannot be read is refused in a sentence: a
// project that is no repository or is bound to no forge, bindings that cannot
// be read, a host without a token, a detached HEAD, a branch name past its
// bound, a forge that fails. Asked for, the answer carries the end of the
// logs of the first jobs that failed the pipeline with the count of the
// rest, or of one job of it, a job without a log saying why; a job the
// pipeline lacks, a job id or a count of lines past its bound is refused.
func TestAProjectsForgeStatusReadsThePushedHead(t *testing.T) {
	var (
		asked []string
		mu    sync.Mutex
	)
	root := t.TempDir()
	dir := worktreeSourceRepo(t, root, "app")
	pushedOut, err := exec.Command("git", "-C", dir, "rev-parse", "feature").Output()
	if err != nil {
		t.Fatal(err)
	}
	pushed := strings.TrimSpace(string(pushedOut))
	srv := httptest.NewServer(fakeGitLabProject(pushed, &asked, &mu))
	defer srv.Close()
	worktreeGit(t, dir, "remote", "add", "origin", srv.URL+"/group/sub/app.git")
	worktreeGit(t, dir, "update-ref", "refs/remotes/origin/feature", "feature")
	worktreeGit(t, dir, "switch", "-q", "feature")
	worktreeGit(t, dir, "commit", "-q", "--allow-empty", "-m", "not pushed")
	localOut, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	local := strings.TrimSpace(string(localOut))
	if err := os.Mkdir(filepath.Join(root, "plain"), 0o755); err != nil {
		t.Fatal(err)
	}
	s, raw := ownerPages(t, worktreeProjects(t, root))
	forgeDir := t.TempDir()
	f := forge.New(forgeDir, []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin())
	s.SetForge(f)
	host := addForgeHost(t, f, forge.Input{Kind: "gitlab", WebURL: srv.URL}, forgeStatusToken)
	read := func(path string) (int, forgeStatusAnswer) {
		t.Helper()
		rec := raw(http.MethodGet, path, "", "")
		var answer forgeStatusAnswer
		if err := json.Unmarshal(rec.Body.Bytes(), &answer); err != nil {
			t.Fatalf("%s answered %d %q", path, rec.Code, rec.Body.String())
		}
		return rec.Code, answer
	}

	code, answer := read("/projects/app/forge/status")
	if code != http.StatusOK || answer.Project != "app" || answer.Branch != "feature" || answer.RemoteBranch != "feature" || answer.Head != pushed || answer.Local != local {
		t.Fatalf("answered %d %+v", code, answer)
	}
	address := strings.TrimPrefix(srv.URL, "http://") + "/group/sub/app"
	if answer.Forge.Label != "GitLab" || answer.Forge.Address != address || answer.Forge.URL != srv.URL+"/group/sub/app" || answer.Forge.ChangeNoun != "merge request" {
		t.Fatalf("the forge is %+v", answer.Forge)
	}
	if c := answer.Change; c == nil || c.Ref != "!4" || c.Title != "Fix the login" || !c.Draft || c.State != "open" || c.Target != "master" || c.Head != pushed {
		t.Fatalf("the change is %+v", answer.Change)
	}
	if p := answer.Pipeline; p == nil || p.ID != "12" || p.Status != "running" || len(p.Jobs) != 5 || p.Jobs[0].ID != "31" || p.Jobs[0].Status != "failed" || p.Jobs[0].Stage != "test" || p.Jobs[0].NoLog != "" || p.Jobs[1].NoLog == "" {
		t.Fatalf("the pipeline is %+v", answer.Pipeline)
	}
	if answer.Logs != nil {
		t.Fatalf("logs nobody asked for: %+v", answer.Logs)
	}
	mu.Lock()
	sawPushed := len(asked) == 5 && strings.Contains(asked[1], "sha="+pushed)
	mu.Unlock()
	if !sawPushed {
		t.Fatalf("the forge was asked %v, not for the pushed head %s", asked, pushed)
	}

	code, answer = read("/projects/app/forge/status?log=failed")
	if code != http.StatusOK || len(answer.Logs) != forge.MaxFailedLogs || answer.LogsLeft != 2 {
		t.Fatalf("the failed logs answered %d %+v, %d left", code, answer.Logs, answer.LogsLeft)
	}
	if l := answer.Logs[0]; l.ID != "31" || l.Name != "unit" || l.Error != "" || l.Cut || strings.Join(l.Lines, "|") != "$ go test ./...|--- FAIL: TestLogin|FAIL" {
		t.Fatalf("the trace reads %+v", l)
	}
	if l := answer.Logs[1]; l.ID != "32" || l.Lines != nil || l.Error != "A trigger job has no log, the jobs of its downstream pipeline do." {
		t.Fatalf("the trigger job reads %+v", l)
	}
	if l := answer.Logs[2]; l.ID != "33" || l.Error != "An external commit status has no log on GitLab." {
		t.Fatalf("the external status reads %+v", l)
	}
	code, answer = read("/projects/app/forge/status?log=31&lines=1")
	if code != http.StatusOK || len(answer.Logs) != 1 || !answer.Logs[0].Cut || strings.Join(answer.Logs[0].Lines, "|") != "FAIL" || answer.LogsLeft != 0 {
		t.Fatalf("one job's log answered %d %+v", code, answer.Logs)
	}

	code, answer = read("/projects/app/forge/status?branch=master")
	if code != http.StatusOK || answer.Branch != "master" || answer.Head != "" || answer.Local == "" || answer.Change != nil || answer.Pipeline != nil {
		t.Fatalf("a branch never pushed answered %d %+v", code, answer)
	}

	for _, c := range []struct {
		path string
		code int
		says string
	}{
		{"/projects/gone/forge/status", http.StatusNotFound, "Unknown project."},
		{"/projects/plain/forge/status", http.StatusBadRequest, `"plain" is no git repository, it binds to no forge.`},
		{"/projects/app/forge/status?branch=" + strings.Repeat("b", forge.MaxBranch+1), http.StatusBadRequest, "A branch name has at most 255 characters."},
		{"/projects/app/forge/status?branch=-x", http.StatusBadRequest, "A branch name must not start with a dash."},
		{"/projects/app/forge/status?branch=broken", http.StatusBadGateway, "The server answered 500 Internal Server Error."},
		{"/projects/app/forge/status?log=99", http.StatusNotFound, `The pipeline of "feature" has no job "99".`},
		{"/projects/app/forge/status?branch=master&log=31", http.StatusNotFound, `"master" has no pipeline at its pushed head, so no job "31".`},
		{"/projects/app/forge/status?log=" + strings.Repeat("9", maxForgeJobID+1), http.StatusBadRequest, "A job id has at most 64 characters."},
		{"/projects/app/forge/status?log=failed&lines=0", http.StatusBadRequest, "The lines of a log are a number from 1 to 1000."},
		{"/projects/app/forge/status?log=failed&lines=1001", http.StatusBadRequest, "The lines of a log are a number from 1 to 1000."},
		{"/projects/app/forge/status?log=failed&lines=ten", http.StatusBadRequest, "The lines of a log are a number from 1 to 1000."},
	} {
		if code, answer := read(c.path); code != c.code || answer.Error != c.says {
			t.Errorf("%s answered %d %q, want %d %q", c.path, code, answer.Error, c.code, c.says)
		}
	}

	hidden := "x\u202egnp"
	for _, branch := range []string{hidden, "into-hidden"} {
		worktreeGit(t, dir, "branch", branch, "feature")
		worktreeGit(t, dir, "update-ref", "refs/remotes/origin/"+branch, "feature")
	}
	off := "The forge features are off for this branch, since %s breaks the cockpit's rule: a branch name holds no spaces, no control characters and no invisible characters, its character %d is U+%s."
	mu.Lock()
	before := len(asked)
	mu.Unlock()
	if code, answer := read("/projects/app/forge/status?branch=" + url.QueryEscape(hidden)); code != http.StatusBadRequest || answer.Error != "A branch name holds no spaces, no control characters and no invisible characters, its character 2 is U+202E." {
		t.Fatalf("a branch named with a direction mark answered %d %q", code, answer.Error)
	}
	worktreeGit(t, dir, "switch", "-q", hidden)
	if code, answer := read("/projects/app/forge/status?fresh=1"); code != http.StatusConflict || answer.Error != fmt.Sprintf(off, "its name", 2, "202E") {
		t.Fatalf("HEAD on a branch with a direction mark answered %d %q", code, answer.Error)
	}
	mu.Lock()
	unasked := len(asked) == before
	mu.Unlock()
	if !unasked {
		t.Fatalf("a refused branch asked the forge %v", asked[before:])
	}
	worktreeGit(t, dir, "switch", "-q", "feature")
	if code, answer := read("/projects/app/forge/status?branch=into-hidden"); code != http.StatusConflict || answer.Error != fmt.Sprintf(off, "the branch its merge request !7 merges into", 4, "009B") || answer.Change != nil {
		t.Fatalf("a merge request into a hidden branch answered %d %+v", code, answer)
	}

	if err := f.ChangeToken(host.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if code, answer := read("/projects/app/forge/status"); code != http.StatusConflict || answer.Error != forge.ErrNoToken.Error() {
		t.Fatalf("a host without a token answered %d %q", code, answer.Error)
	}
	if err := f.SetOverride("app", forge.Override{How: forge.Off}); err != nil {
		t.Fatal(err)
	}
	if code, answer := read("/projects/app/forge/status"); code != http.StatusConflict || answer.Error != `"app" is bound to no forge: its Forge is switched off.` {
		t.Fatalf("a project whose forge is off answered %d %q", code, answer.Error)
	}
	bindingsPath := filepath.Join(forgeDir, "forge", "bindings.json")
	if err := os.Remove(bindingsPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(bindingsPath, 0o700); err != nil {
		t.Fatal(err)
	}
	if code, answer := read("/projects/app/forge/status"); code != http.StatusServiceUnavailable || answer.Error != "The forge bindings could not be read." {
		t.Fatalf("unreadable bindings answered %d %q, want no read past an override nobody knows", code, answer.Error)
	}
	if err := os.Remove(bindingsPath); err != nil {
		t.Fatal(err)
	}
	if err := f.ChangeToken(host.ID, forgeStatusToken, false); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, dir, "switch", "-q", "--detach")
	if code, answer := read("/projects/app/forge/status"); code != http.StatusConflict || answer.Error != `HEAD of "app" is detached, name the branch.` {
		t.Fatalf("a detached HEAD answered %d %q", code, answer.Error)
	}
	if code, answer := read("/projects/app/forge/status?branch=feature"); code != http.StatusOK || answer.Head != pushed {
		t.Fatalf("a named branch beside a detached HEAD answered %d %+v", code, answer)
	}
}
