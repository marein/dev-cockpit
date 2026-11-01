package gitlab_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

// fakeOpen answers the project and the open of a merge request the way GitLab
// 19.5 answers them, the merge request with the title it was sent, a draft by
// the mark it opens with, and refuses the open with refused where set.
type fakeOpen struct {
	mu      sync.Mutex
	sent    []map[string]any
	refused *refusal
	empty   bool
}

func (f *fakeOpen) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
		return
	}
	switch {
	case r.Method == http.MethodGet && r.URL.EscapedPath() == projectAt:
		branch := `"main"`
		if f.empty {
			branch = "null"
		}
		fmt.Fprintf(w, `{"id":2,"path_with_namespace":"group/sub/app","default_branch":%s,"web_url":"https://gitlab.example.com/group/sub/app"}`, branch)
	case r.Method == http.MethodPost && r.URL.EscapedPath() == projectAt+"/merge_requests":
		var sent map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		f.mu.Lock()
		f.sent = append(f.sent, sent)
		f.mu.Unlock()
		if f.refused != nil {
			w.WriteHeader(f.refused.code)
			fmt.Fprint(w, f.refused.body)
			return
		}
		title, _ := sent["title"].(string)
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"id":145,"iid":5,"project_id":2,"title":%q,"description":%q,"state":"opened","target_branch":%q,"source_branch":%q,"source_project_id":2,"target_project_id":2,"draft":%t,"sha":%q,"web_url":"https://gitlab.example.com/group/sub/app/-/merge_requests/5"}`,
			title, sent["description"], sent["target_branch"], sent["source_branch"], strings.HasPrefix(title, "Draft:"), head)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Not Found"}`)
	}
}

func open(t *testing.T, webURL string, o forge.NewChange) (forge.Change, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b := forge.Binding{Host: host(webURL), Path: "group/sub/app"}
	opened, err := gitlab.Provider{}.OpenChange(ctx, forge.NewClient(), b, token, "feature/login", o)
	if err != nil && strings.Contains(err.Error(), token) {
		t.Fatalf("the error names the token: %v", err)
	}
	return opened, err
}

// The default branch is the project's, and a project without one, an empty
// repository, says so.
func TestTheDefaultBranchIsTheProjects(t *testing.T) {
	f := &fakeOpen{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	b := forge.Binding{Host: host(srv.URL), Path: "group/sub/app"}
	if got, err := (gitlab.Provider{}).DefaultBranch(context.Background(), forge.NewClient(), b, token); err != nil || got != "main" {
		t.Fatalf("the default branch reads %q, %v", got, err)
	}
	f.empty = true
	if _, err := (gitlab.Provider{}).DefaultBranch(context.Background(), forge.NewClient(), b, token); err == nil || err.Error() != strings.TrimPrefix(srv.URL, "http://")+"/group/sub/app has no default branch on GitLab, its repository is empty." {
		t.Fatalf("an empty repository answered %v", err)
	}
}

// A merge request opens from the branch into the target with its title and
// description; GitLab takes no draft field, a draft's title opens with its
// mark, never twice.
func TestAMergeRequestOpensADraftByItsTitle(t *testing.T) {
	f := &fakeOpen{}
	srv := httptest.NewServer(f)
	defer srv.Close()
	for _, c := range []struct {
		sent  forge.NewChange
		title string
		draft bool
	}{
		{forge.NewChange{Target: "main", Title: "Fix the login", Body: "Why.", Draft: true}, "Draft: Fix the login", true},
		{forge.NewChange{Target: "main", Title: "draft: Fix it", Draft: true}, "draft: Fix it", false},
		{forge.NewChange{Target: "main", Title: "[Draft] Fix it", Draft: true}, "[Draft] Fix it", false},
		{forge.NewChange{Target: "release", Title: "Ship it"}, "Ship it", false},
	} {
		opened, err := open(t, srv.URL, c.sent)
		sent := f.sent[len(f.sent)-1]
		if err != nil || sent["title"] != c.title || sent["source_branch"] != "feature/login" || sent["target_branch"] != c.sent.Target || sent["description"] != c.sent.Body {
			t.Fatalf("%+v sent %v, %v", c.sent, sent, err)
		}
		if _, ok := sent["draft"]; ok {
			t.Fatalf("%+v sent a draft field GitLab ignores", c.sent)
		}
		if opened.Number != 5 || opened.State != forge.Open || opened.Target != c.sent.Target || opened.URL != "https://gitlab.example.com/group/sub/app/-/merge_requests/5" || opened.Head != head || (c.draft && !opened.Draft) {
			t.Fatalf("%+v opened %+v", c.sent, opened)
		}
	}
}

// What GitLab refuses is said, in its words where it has them: one open for
// the branch already, a target it lacks, a token without the scope api, an
// account whose role may not, a project it hides.
func TestAnOpenGitLabRefusesIsSaid(t *testing.T) {
	for _, c := range []struct {
		name    string
		refused refusal
		says    string
	}{
		{"open already", refusal{409, `{"message":["Another open merge request already exists for this source branch: !1"]}`}, "GitLab refused to open a merge request: Another open merge request already exists for this source branch: !1."},
		{"no target", refusal{400, `{"message":{"target_branch":["does not exist"]}}`}, "GitLab refused to open a merge request: target_branch does not exist."},
		{"itself", refusal{422, `{"message":["You can't use same project/branch for source and target"]}`}, "GitLab refused to open a merge request: You can't use same project/branch for source and target."},
		{"scope", refusal{403, scopeAnswer}, "The token may not write to GitLab: give it the scope api."},
		{"role", refusal{403, `{"message":"403 Forbidden"}`}, "The token's account may not open a merge request in %s, it needs the Developer role there."},
		{"hidden", refusal{404, `{"message":"404 Project Not Found"}`}, "GitLab knows no project %s, or the token may not read it."},
	} {
		srv := httptest.NewServer(&fakeOpen{refused: &c.refused})
		says := c.says
		if strings.Contains(says, "%s") {
			says = fmt.Sprintf(says, strings.TrimPrefix(srv.URL, "http://")+"/group/sub/app")
		}
		if _, err := open(t, srv.URL, forge.NewChange{Target: "main", Title: "Fix"}); err == nil || err.Error() != says {
			t.Errorf("%s: %v, want %q", c.name, err, says)
		}
		srv.Close()
	}
}

// GitLab opens change requests.
func TestGitLabOpensChangeRequests(t *testing.T) {
	var p forge.Provider = gitlab.Provider{}
	if _, ok := p.(forge.ChangeOpener); !ok {
		t.Fatal("GitLab opens no change request")
	}
}
