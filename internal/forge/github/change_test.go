package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
)

// fakePulls answers the repository and the open of a pull request the way
// GitHub's REST API answers them, the pull request as it was sent, and
// refuses the open with code and body where code is set.
type fakePulls struct {
	sent     []map[string]any
	code     int
	body     string
	empty    bool
	pullsOff bool
}

func (f *fakePulls) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest","status":"401"}`)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.Path == repoAt:
		branch := `"main"`
		if f.empty {
			branch = "null"
		}
		fmt.Fprintf(w, `{"id":2,"name":"app","full_name":"octo/app","private":true,"default_branch":%s,"html_url":"https://github.example.com/octo/app","has_pull_requests":%t}`, branch, !f.pullsOff)
	case r.Method == http.MethodPost && r.URL.Path == repoAt+"/pulls" && !f.pullsOff:
		var sent map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		f.sent = append(f.sent, sent)
		if f.code != 0 {
			w.WriteHeader(f.code)
			fmt.Fprint(w, f.body)
			return
		}
		draft, _ := sent["draft"].(bool)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"url":"https://github.example.com/api/v3/repos/octo/app/pulls/9","id":1009,"html_url":"https://github.example.com/octo/app/pull/9","number":9,"state":"open","title":%q,"body":%q,"merged_at":null,"draft":%t,"head":{"label":"octo:feature/login","ref":"feature/login","sha":%q,"repo":{"full_name":"octo/app"}},"base":{"label":"octo:%s","ref":%q,"repo":{"full_name":"octo/app"}}}`,
			sent["title"], sent["body"], draft, head, sent["base"], sent["base"])
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}`)
	}
}

func openPull(t *testing.T, webURL string, o forge.NewChange) (forge.Change, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b := forge.Binding{Host: host(webURL), Path: "octo/app"}
	opened, err := Provider{}.OpenChange(ctx, forge.NewClient(), b, token, "feature/login", o)
	if err != nil && strings.Contains(err.Error(), token) {
		t.Fatalf("the error names the token: %v", err)
	}
	return opened, err
}

// The default branch is the repository's, and an empty repository says it
// has none.
func TestTheDefaultBranchIsTheRepositorys(t *testing.T) {
	f := &fakePulls{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	b := forge.Binding{Host: host(srv.URL), Path: "octo/app"}
	if got, err := (Provider{}).DefaultBranch(context.Background(), forge.NewClient(), b, token); err != nil || got != "main" {
		t.Fatalf("the default branch reads %q, %v", got, err)
	}
	f.empty = true
	if _, err := (Provider{}).DefaultBranch(context.Background(), forge.NewClient(), b, token); err == nil || !strings.HasSuffix(err.Error(), "/octo/app has no default branch on GitHub, its repository is empty.") {
		t.Fatalf("an empty repository answered %v", err)
	}
}

// A repository whose pull requests are switched off has no branch to merge
// into and opens none, and says so instead of a repository GitHub hides.
func TestPullRequestsSwitchedOffAreSaid(t *testing.T) {
	f := &fakePulls{pullsOff: true}
	srv := httptest.NewServer(f)
	defer srv.Close()
	says := "Pull requests are switched off in the settings of " + strings.TrimPrefix(srv.URL, "http://") + "/octo/app on GitHub."
	b := forge.Binding{Host: host(srv.URL), Path: "octo/app"}
	if _, err := (Provider{}).DefaultBranch(context.Background(), forge.NewClient(), b, token); err == nil || err.Error() != says {
		t.Fatalf("the default branch answered %v", err)
	}
	if _, err := openPull(t, srv.URL, forge.NewChange{Target: "main", Title: "Fix", Draft: true}); err == nil || err.Error() != says {
		t.Fatalf("the open answered %v", err)
	}
}

// A pull request opens from the repository's own branch, named with its
// owner, into the target, a draft by GitHub's own field.
func TestAPullRequestOpensFromTheOwnersBranch(t *testing.T) {
	f := &fakePulls{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	opened, err := openPull(t, srv.URL, forge.NewChange{Target: "main", Title: "Fix the login", Body: "Why.", Draft: true})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := json.Marshal(f.sent[0]); string(got) != `{"base":"main","body":"Why.","draft":true,"head":"octo:feature/login","title":"Fix the login"}` {
		t.Fatalf("sent %s", got)
	}
	if opened.Number != 9 || !opened.Draft || opened.State != forge.Open || opened.Target != "main" || opened.URL != "https://github.example.com/octo/app/pull/9" || opened.Head != head {
		t.Fatalf("opened %+v", opened)
	}
}

// What GitHub refuses is said, in its words where it has them: a pull request
// of the branch already, no commits between the branches, a base it lacks, a
// draft the repository does not take, a token that may not, a repository it
// hides.
func TestAnOpenGitHubRefusesIsSaid(t *testing.T) {
	scope := "a fine grained token needs the permission Pull requests read and write, a classic one the scope repo."
	for _, c := range []struct {
		name string
		code int
		body string
		says string
	}{
		{"open already", 422, `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"A pull request already exists for octo:feature/login."}],"documentation_url":"https://docs.github.com/rest/pulls/pulls#create-a-pull-request","status":"422"}`, "GitHub refused to open the pull request: A pull request already exists for octo:feature/login."},
		{"no commits", 422, `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"No commits between main and feature/login"}],"status":"422"}`, "GitHub refused to open the pull request: No commits between main and feature/login."},
		{"no base", 422, `{"message":"Validation Failed","errors":[{"resource":"PullRequest","field":"base","code":"invalid"}],"status":"422"}`, "GitHub refused to open the pull request: base: invalid."},
		{"no drafts", 422, `{"message":"Draft pull requests are not supported in this repository.","status":"422"}`, "GitHub refused to open the pull request: Draft pull requests are not supported in this repository."},
		{"scope", 403, `{"message":"Resource not accessible by personal access token","status":"403"}`, "The token may not open pull requests in %s: " + scope},
		{"hidden", 404, `{"message":"Not Found","status":"404"}`, "GitHub knows no repository %s, or the token may not open pull requests in it: " + scope},
	} {
		srv := httptest.NewServer(&fakePulls{code: c.code, body: c.body})
		says := c.says
		if strings.Contains(says, "%s") {
			says = fmt.Sprintf(says, strings.TrimPrefix(srv.URL, "http://")+"/octo/app")
		}
		if _, err := openPull(t, srv.URL, forge.NewChange{Target: "main", Title: "Fix", Draft: true}); err == nil || err.Error() != says {
			t.Errorf("%s: %v, want %q", c.name, err, says)
		}
		srv.Close()
	}
}

// GitHub opens change requests.
func TestGitHubOpensChangeRequests(t *testing.T) {
	var p forge.Provider = Provider{}
	if _, ok := p.(forge.ChangeOpener); !ok {
		t.Fatal("GitHub opens no change request")
	}
}
