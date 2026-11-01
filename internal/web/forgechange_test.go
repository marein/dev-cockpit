package web

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/marein/dev-cockpit/internal/approval"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
	"github.com/marein/dev-cockpit/internal/localapi"
)

// changeGitLab answers group/sub/app like GitLab: master its default branch
// unless told another, no pipeline, the merge request of feature it opened or
// was told stands open, and opens one on a post, a draft by the mark its
// title opens with, unless the token may only read. It keeps what each open
// sent.
type changeGitLab struct {
	mu       sync.Mutex
	requests int
	head     string
	base     string
	open     *forge.Change
	readOnly bool
	sent     []map[string]any
}

func (g *changeGitLab) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.requests++
	w.Header().Set("Content-Type", "application/json")
	if r.Header.Get("Authorization") != "Bearer "+forgeStatusToken {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message":"401 Unauthorized"}`)
		return
	}
	project := "/api/v4/projects/group%2Fsub%2Fapp"
	mergeRequest := func(c forge.Change) string {
		return fmt.Sprintf(`{"id":%d,"iid":%d,"project_id":2,"title":%q,"state":"opened","target_branch":%q,"source_branch":"feature","source_project_id":2,"target_project_id":2,"draft":%t,"sha":%q,"web_url":%q}`,
			100+c.Number, c.Number, c.Title, c.Target, c.Draft, g.head, c.URL)
	}
	switch path := r.URL.EscapedPath(); {
	case r.Method == http.MethodGet && path == project:
		base, _ := json.Marshal(cmp.Or(g.base, "master"))
		fmt.Fprintf(w, `{"id":2,"path_with_namespace":"group/sub/app","default_branch":%s}`, base)
	case r.Method == http.MethodPost && path == project+"/merge_requests":
		var sent map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		g.sent = append(g.sent, sent)
		if g.readOnly {
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, `{"error":"insufficient_scope","error_description":"The request requires higher privileges than provided by the access token.","scope":"api"}`)
			return
		}
		title, _ := sent["title"].(string)
		target, _ := sent["target_branch"].(string)
		g.open = &forge.Change{Number: 5, Title: title, Draft: strings.HasPrefix(title, "Draft: "), Target: target, URL: "https://gitlab.example.com/group/sub/app/-/merge_requests/5"}
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, mergeRequest(*g.open))
	case path == project+"/merge_requests":
		if g.open == nil || r.URL.Query().Get("source_branch") != "feature" {
			fmt.Fprint(w, `[]`)
			return
		}
		fmt.Fprint(w, "["+mergeRequest(*g.open)+"]")
	case path == project+"/pipelines":
		fmt.Fprint(w, `[]`)
	default:
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"404 Not found"}`)
	}
}

func (g *changeGitLab) asked() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.requests
}

func (g *changeGitLab) opens() []map[string]any {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]map[string]any(nil), g.sent...)
}

func (g *changeGitLab) stand(c *forge.Change) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.open = c
}

func (g *changeGitLab) name(base string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.base = base
}

// changeRepository makes the repository app in root, its branch feature two
// commits past master and pushed, both as origin has them, and checked out,
// with its origin on the fake GitLab at webURL. It answers the repository
// and the pushed head.
func changeRepository(t *testing.T, root, webURL string) (string, string) {
	t.Helper()
	repo := worktreeSourceRepo(t, root, "app")
	worktreeGit(t, repo, "switch", "-q", "feature")
	worktreeGit(t, repo, "commit", "-q", "--allow-empty", "-m", "Add the <login>", "-m", "It checks the name.")
	worktreeGit(t, repo, "commit", "-q", "--allow-empty", "-m", "Fix the login")
	worktreeGit(t, repo, "remote", "add", "origin", webURL+"/group/sub/app.git")
	worktreeGit(t, repo, "update-ref", "refs/remotes/origin/master", "master")
	worktreeGit(t, repo, "update-ref", "refs/remotes/origin/feature", "feature")
	out, err := exec.Command("git", "-C", repo, "rev-parse", "feature").Output()
	if err != nil {
		t.Fatal(err)
	}
	return repo, strings.TrimSpace(string(out))
}

func changeForge(t *testing.T, webURL string) *forge.Service {
	t.Helper()
	service := forge.New(t.TempDir(), []forge.Provider{gitlab.Provider{}})
	addForgeHost(t, service, forge.Input{Kind: "gitlab", WebURL: webURL}, forgeStatusToken)
	return service
}

// The form is prefilled from the commits the pushed branch carries since the
// default branch, a draft into it, everything the forge or a commit wrote
// escaped. Sent from the dialog it opens the merge request as edited and
// says so; then the branch has one open, which the form and a second send
// show in place of another.
func TestTheFormOpensAChangeRequestMadeOfTheCommits(t *testing.T) {
	g := &changeGitLab{}
	srv := httptest.NewServer(g)
	defer srv.Close()
	root := t.TempDir()
	_, pushed := changeRepository(t, root, srv.URL)
	g.head = pushed
	s, raw := ownerPages(t, worktreeProjects(t, root))
	s.SetForge(changeForge(t, srv.URL))
	address := strings.TrimPrefix(srv.URL, "http://") + "/group/sub/app"
	form := raw(http.MethodGet, "/projects/app/forge/change?modal=1&branch=feature", "", "").Body.String()
	for _, want := range []string{
		`action="/projects/app/forge/change?modal=1"`, `data-form-title="New merge request"`, `name="branch" value="feature"`,
		`value="Add the &lt;login&gt;"`, ">* Add the &lt;login&gt;\n* Fix the login</textarea>", `name="target" value="master"`,
		`name="draft" value="1" checked`, "Made of the 2 commits feature carries since master.",
		"From <code>feature</code> at <code>" + pushed[:12] + "</code> on GitLab · " + address, ">Open merge request</button>",
	} {
		if !strings.Contains(form, want) {
			t.Fatalf("the form lacks %s:\n%s", want, form)
		}
	}
	if strings.Contains(form, "<login>") || strings.Contains(form, "data-forge-change-local") {
		t.Fatalf("the form carries what it should not:\n%s", form)
	}
	if rec := raw(http.MethodGet, "/projects/app/forge/change", "", ""); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `action="/projects/app/forge/change"`) {
		t.Fatalf("the page answered %d", rec.Code)
	}
	sent := url.Values{"branch": {"feature"}, "title": {"Fix the login form"}, "body": {"Two fixes."}, "target": {"master"}, "draft": {"1"}}
	rec := raw(http.MethodPost, "/projects/app/forge/change?modal=1", "application/x-www-form-urlencoded", sent.Encode())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"message":"The merge request is open."`) {
		t.Fatalf("the open answered %d %s", rec.Code, rec.Body.String())
	}
	opens := g.opens()
	want := map[string]any{"source_branch": "feature", "target_branch": "master", "title": "Draft: Fix the login form", "description": "Two fixes."}
	if len(opens) != 1 || !reflect.DeepEqual(opens[0], want) {
		t.Fatalf("the forge was sent %v", opens)
	}
	shown := raw(http.MethodGet, "/projects/app/forge/change?modal=1", "", "").Body.String()
	again := raw(http.MethodPost, "/projects/app/forge/change?modal=1", "application/x-www-form-urlencoded", sent.Encode())
	for _, page := range []string{shown, again.Body.String()} {
		if !strings.Contains(page, "data-forge-change-exists") || !strings.Contains(page, `href="https://gitlab.example.com/group/sub/app/-/merge_requests/5"`) || !strings.Contains(page, "!5 Draft: Fix the login form") || strings.Contains(page, "<form") {
			t.Fatalf("the branch with an open one shows:\n%s", page)
		}
	}
	if again.Code != http.StatusOK || !strings.HasPrefix(again.Header().Get("Content-Type"), "text/html") {
		t.Fatalf("a second open answered %d %s", again.Code, again.Header().Get("Content-Type"))
	}
	if len(g.opens()) != 1 {
		t.Fatal("a second open reached the forge")
	}
}

// A branch that carries more than a hundred commits is titled by its oldest
// one, lists every subject and counts them all.
func TestTheFormIsMadeOfEveryCommitOfALongBranch(t *testing.T) {
	g := &changeGitLab{}
	srv := httptest.NewServer(g)
	defer srv.Close()
	root := t.TempDir()
	repo, _ := changeRepository(t, root, srv.URL)
	subjects := []string{"* Add the &lt;login&gt;", "* Fix the login"}
	for i := 1; i <= 103; i++ {
		worktreeGit(t, repo, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("Step %d", i))
		subjects = append(subjects, fmt.Sprintf("* Step %d", i))
	}
	worktreeGit(t, repo, "update-ref", "refs/remotes/origin/feature", "feature")
	out, err := exec.Command("git", "-C", repo, "rev-parse", "feature").Output()
	if err != nil {
		t.Fatal(err)
	}
	g.head = strings.TrimSpace(string(out))
	s, raw := ownerPages(t, worktreeProjects(t, root))
	s.SetForge(changeForge(t, srv.URL))
	form := raw(http.MethodGet, "/projects/app/forge/change?modal=1&branch=feature", "", "").Body.String()
	for _, want := range []string{`value="Add the &lt;login&gt;"`, ">" + strings.Join(subjects, "\n") + "</textarea>", "Made of the 105 commits feature carries since master."} {
		if !strings.Contains(form, want) {
			t.Fatalf("the form lacks %s:\n%s", want, form)
		}
	}
}

// The page form sends the line breaks of its description as two characters,
// which count as the one each the text box counted: a description at its
// bound opens, with plain line breaks.
func TestThePageFormCountsALineBreakOnce(t *testing.T) {
	g := &changeGitLab{}
	srv := httptest.NewServer(g)
	defer srv.Close()
	root := t.TempDir()
	_, pushed := changeRepository(t, root, srv.URL)
	g.head = pushed
	s, raw := ownerPages(t, worktreeProjects(t, root))
	s.SetForge(changeForge(t, srv.URL))
	body := strings.Repeat(strings.Repeat("x", 63)+"\n", 1023) + strings.Repeat("x", 64)
	if n := len(body); n != forge.MaxChangeBody {
		t.Fatalf("the description holds %d characters", n)
	}
	var sent strings.Builder
	w := multipart.NewWriter(&sent)
	for _, field := range [][2]string{{"branch", "feature"}, {"title", "Fix"}, {"body", strings.ReplaceAll(body, "\n", "\r\n")}, {"target", "master"}} {
		if err := w.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	if rec := raw(http.MethodPost, "/projects/app/forge/change", w.FormDataContentType(), sent.String()); rec.Code != http.StatusSeeOther {
		t.Fatalf("the open answered %d %s", rec.Code, notice(rec.Body.String()))
	}
	if opens := g.opens(); len(opens) != 1 || opens[0]["description"] != body {
		t.Fatalf("the forge was sent %d opens", len(opens))
	}
}

// The page is titled what it opens, also over a refusal and over the open
// one. A local branch elsewhere than the pushed head says how it stands, and
// asks for a push only where it is ahead, since git refuses the push of one
// that is behind or gone apart.
func TestTheFormSaysHowTheLocalBranchStands(t *testing.T) {
	g := &changeGitLab{}
	srv := httptest.NewServer(g)
	defer srv.Close()
	root := t.TempDir()
	repo, pushed := changeRepository(t, root, srv.URL)
	g.head = pushed
	s, raw := ownerPages(t, worktreeProjects(t, root))
	s.SetForge(changeForge(t, srv.URL))
	const title = "<title>New merge request - Dev Cockpit</title>"
	refused := url.Values{"branch": {"feature"}, "title": {"Typed title"}, "target": {"-x"}}
	for _, page := range []string{
		raw(http.MethodGet, "/projects/app/forge/change", "", "").Body.String(),
		raw(http.MethodPost, "/projects/app/forge/change", "application/x-www-form-urlencoded", refused.Encode()).Body.String(),
	} {
		if !strings.Contains(page, title) || strings.Contains(page, "data-forge-change-local") {
			t.Fatalf("the page reads:\n%s", page)
		}
	}
	for _, c := range []struct {
		git  []string
		says string
	}{
		{[]string{"commit", "-q", "--allow-empty", "-m", "Not pushed"}, `alert-warning small py-2" data-forge-change-local>
      <div class="text-break min-w-0">The local branch stands at <code>%s</code>, 1 commit ahead of the pushed head: push first to carry it.</div>`},
		{[]string{"reset", "-q", "--hard", "HEAD~3"}, `alert-info small py-2" data-forge-change-local>
      <div class="text-break min-w-0">The local branch stands at <code>%s</code>, 2 commits behind the pushed head, which this merge request carries.</div>`},
		{[]string{"commit", "-q", "--allow-empty", "-m", "Apart"}, `alert-warning small py-2" data-forge-change-local>
      <div class="text-break min-w-0">The local branch stands at <code>%s</code>, 1 commit ahead of the pushed head and 2 behind it: this merge request lacks it.</div>`},
	} {
		worktreeGit(t, repo, c.git...)
		local, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
		if err != nil {
			t.Fatal(err)
		}
		page := raw(http.MethodGet, "/projects/app/forge/change", "", "").Body.String()
		if want := fmt.Sprintf(c.says, string(local[:12])); !strings.Contains(page, want) || !strings.Contains(page, title) {
			t.Fatalf("after git %s the page lacks %s:\n%s", c.git[0], want, page)
		}
	}
	sent := url.Values{"branch": {"feature"}, "title": {"Typed title"}, "target": {"master"}}
	if rec := raw(http.MethodPost, "/projects/app/forge/change", "application/x-www-form-urlencoded", sent.Encode()); rec.Code != http.StatusSeeOther {
		t.Fatalf("the open answered %d %s", rec.Code, rec.Body.String())
	}
	if page := raw(http.MethodPost, "/projects/app/forge/change", "application/x-www-form-urlencoded", sent.Encode()).Body.String(); !strings.Contains(page, "data-forge-change-exists") || !strings.Contains(page, title) {
		t.Fatalf("the page of the open one reads:\n%s", page)
	}
}

// An open is checked before the forge is asked: bounds, a target that reads
// as an option or is the branch itself, a branch not pushed; a token that
// may not write is refused in a sentence after it was asked.
func TestAnOpenIsCheckedBeforeTheForgeIsAsked(t *testing.T) {
	g := &changeGitLab{}
	srv := httptest.NewServer(g)
	defer srv.Close()
	root := t.TempDir()
	repo, pushed := changeRepository(t, root, srv.URL)
	g.head = pushed
	worktreeGit(t, repo, "branch", "local")
	s, raw := ownerPages(t, worktreeProjects(t, root))
	s.SetForge(changeForge(t, srv.URL))
	for _, c := range []struct {
		form url.Values
		code int
		says string
	}{
		{url.Values{"title": {strings.Repeat("t", forge.MaxChangeTitle+1)}}, 400, fmt.Sprintf("A title has at most %d characters.", forge.MaxChangeTitle)},
		{url.Values{"title": {"Fix"}, "body": {strings.Repeat("b", forge.MaxChangeBody+1)}}, 400, fmt.Sprintf("A description has at most %d characters.", forge.MaxChangeBody)},
		{url.Values{"title": {"Fix"}, "target": {"--upload-pack=x"}}, 400, "A branch name must not start with a dash."},
		{url.Values{"title": {"Fix"}, "target": {"main\x1b[2J"}}, 400, "A branch name holds no spaces, no control characters and no invisible characters, its character 5 is U+001B."},
		{url.Values{"title": {"Fix"}, "target": {"rel\u202eesae"}}, 400, "A branch name holds no spaces, no control characters and no invisible characters, its character 4 is U+202E."},
		{url.Values{"title": {"Fix"}, "branch": {"x\u009b2J"}}, 400, "A branch name holds no spaces, no control characters and no invisible characters, its character 2 is U+009B."},
		{url.Values{"title": {"Fix"}, "target": {"feature"}}, 409, `A merge request cannot merge "feature" into itself.`},
		{url.Values{"title": {" "}}, 409, "A merge request needs a title."},
		{url.Values{"title": {"Fix"}, "branch": {"local"}}, 409, `"local" is not pushed, push it first.`},
	} {
		rec := raw(http.MethodPost, "/projects/app/forge/change?modal=1", "application/x-www-form-urlencoded", c.form.Encode())
		var answer struct{ Error string }
		if rec.Code != c.code || json.Unmarshal(rec.Body.Bytes(), &answer) != nil || answer.Error != c.says {
			t.Errorf("%v answered %d %s, want %q", c.form, rec.Code, rec.Body.String(), c.says)
		}
	}
	if len(g.opens()) != 0 {
		t.Fatalf("a refused open reached the forge: %v", g.opens())
	}
	g.readOnly = true
	rec := raw(http.MethodPost, "/projects/app/forge/change?modal=1", "application/x-www-form-urlencoded", url.Values{"title": {"Fix"}}.Encode())
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "The token may not write to GitLab: give it the scope api.") {
		t.Fatalf("a read only token answered %d %s", rec.Code, rec.Body.String())
	}
	page := raw(http.MethodGet, "/projects/app/forge/change?modal=1&branch=local", "", "").Body.String()
	if !strings.Contains(page, "data-forge-change-problem") || !strings.Contains(page, html.EscapeString(`"local" is not pushed`)) || strings.Contains(page, "<form") {
		t.Fatalf("the form of a branch not pushed shows:\n%s", page)
	}
}

type forgeChangeFixture struct {
	*composeFixture
	gitlab  *changeGitLab
	repo    string
	pushed  string
	address string
}

func newForgeChangeFixture(t *testing.T) *forgeChangeFixture {
	t.Helper()
	f := &forgeChangeFixture{composeFixture: newComposeFixture(t), gitlab: &changeGitLab{}}
	srv := httptest.NewServer(f.gitlab)
	t.Cleanup(srv.Close)
	f.repo, f.pushed = changeRepository(t, filepath.Dir(f.dir), srv.URL)
	f.gitlab.head = f.pushed
	f.s.SetForge(changeForge(t, srv.URL))
	f.address = strings.TrimPrefix(srv.URL, "http://") + "/group/sub/app"
	f.router.POST("/projects/:name/forge/change", f.s.handleProjectForgeChangeOpen)
	return f
}

// open posts the form over the local socket as the assistant id names, none
// for a call that names none.
func (f *forgeChangeFixture) open(t *testing.T, id string, form url.Values) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/projects/app/forge/change", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	if id != "" {
		req.Header.Set(localapi.AssistantHeader, id)
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec.Code, decodeJSON(t, rec)
}

// An assistant's open fills what it leaves out the way the form is
// prefilled and waits for the user, who sees all of it. Approved, it opens,
// and the note names it with its page; asked again while one stands open,
// the open one answers and nobody is asked.
func TestAnAssistantsOpenWaitsForTheApproval(t *testing.T) {
	f := newForgeChangeFixture(t)
	if code, answer := f.open(t, "", url.Values{"draft": {"1"}}); code != http.StatusForbidden || !strings.Contains(answer["error"].(string), "--as") {
		t.Fatalf("an open without an assistant answered %d %v", code, answer)
	}
	code, answer := f.open(t, f.owner, url.Values{"draft": {"1"}})
	what := "Open draft merge request of feature into master in app"
	if code != http.StatusOK || answer["pending"] != true || answer["what"] != what {
		t.Fatalf("the open answered %d %v", code, answer)
	}
	q := f.waitQuestion(t)
	want := []askpass.Detail{
		{Label: "Action", Value: "Open draft merge request"},
		{Label: "Title", Value: "Add the <login>"},
		{Label: "Description", Value: "* Add the <login>\n* Fix the login"},
		{Label: "Branch", Value: "feature at " + f.pushed[:12]},
		{Label: "Into", Value: "master"},
		{Label: "Forge", Value: "GitLab · " + f.address},
		{Label: "Project", Value: "app"},
	}
	if q.Action != what || !reflect.DeepEqual(q.Details, want) || len(f.gitlab.opens()) != 0 {
		t.Fatalf("the question reads %+v, opened %v", q, f.gitlab.opens())
	}
	if code, answer := f.open(t, f.owner, url.Values{"title": {"Other"}}); code != http.StatusConflict || !strings.Contains(answer["error"].(string), "already waits") {
		t.Fatalf("a second open of the branch answered %d %v", code, answer)
	}
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	says := "On GitLab " + f.address + ` the draft merge request !5 "Draft: Add the \<login\>" of feature into master in app is open: https://gitlab.example.com/group/sub/app/-/merge_requests/5.`
	if note.Note.Verdict != approval.Done || !strings.Contains(note.Content, says) {
		t.Fatalf("the note reads %s", note.Content)
	}
	if opens := f.gitlab.opens(); len(opens) != 1 || opens[0]["title"] != "Draft: Add the <login>" || opens[0]["target_branch"] != "master" {
		t.Fatalf("the approval sent %v", opens)
	}
	f.waitNoQuestion(t)
	code, answer = f.open(t, f.owner, url.Values{})
	existing := "On GitLab " + f.address + ` feature of app has the open draft merge request !5 "Draft: Add the <login>" into master already: https://gitlab.example.com/group/sub/app/-/merge_requests/5.`
	if code != http.StatusOK || answer["existing"] != existing || len(f.broker.Questions()) != 0 || len(f.gitlab.opens()) != 1 {
		t.Fatalf("an open of a branch with an open one answered %d %v", code, answer)
	}
}

// An approved open resolves the branch again: one that has an open change
// request by the time the user approves answers that one and opens nothing.
func TestAnApprovedOpenOfABranchOpenedMeanwhileOpensNothing(t *testing.T) {
	f := newForgeChangeFixture(t)
	f.open(t, f.owner, url.Values{"title": {"Mine"}, "body": {""}})
	q := f.waitQuestion(t)
	f.gitlab.stand(&forge.Change{Number: 4, Title: "Theirs", Target: "master", URL: "https://gitlab.example.com/group/sub/app/-/merge_requests/4"})
	f.decide(t, q, true, false)
	note := f.waitApprovalNotes(t, f.owner, 1)[0]
	if note.Note.Verdict != approval.Done || !strings.Contains(note.Content, `has the open merge request !4 "Theirs" into master already: `) || len(f.gitlab.opens()) != 0 {
		t.Fatalf("the approval sent %v, note %s", f.gitlab.opens(), note.Content)
	}
	if q.Details[2] != (askpass.Detail{Label: "Description", Value: "(none)"}) || q.Details[0].Value != "Open merge request" {
		t.Fatalf("the question reads %+v", q.Details)
	}
}

// With the approval off an assistant's open goes through at once as named,
// ready to merge where it asks so, and answers what its note would say.
func TestAnAssistantsOpenGoesThroughAtOnceWithTheApprovalOff(t *testing.T) {
	f := newForgeChangeFixture(t)
	setApprovalAsks(f.s.settings, approvalForgeChange, false)
	code, answer := f.open(t, f.owner, url.Values{"title": {"Ship the login"}, "target": {"master"}})
	says := "On GitLab " + f.address + ` the merge request !5 "Ship the login" of feature into master in app is open: https://gitlab.example.com/group/sub/app/-/merge_requests/5.`
	if code != http.StatusOK || answer["opened"] != says || len(f.broker.Questions()) != 0 {
		t.Fatalf("the open answered %d %v", code, answer)
	}
	if opens := f.gitlab.opens(); len(opens) != 1 || opens[0]["title"] != "Ship the login" || opens[0]["description"] != "* Add the <login>\n* Fix the login" {
		t.Fatalf("the open sent %v", opens)
	}
}

// A default branch the forge names that is no branch name opens nothing and
// asks nobody, the refusal names the rule it breaks as one before anything
// was sent; an open that names the branch to merge into still asks and opens.
func TestAnOpenIntoADefaultBranchThatIsNoBranchNameIsRefused(t *testing.T) {
	f := newForgeChangeFixture(t)
	f.gitlab.name("main\x1b[2J\x1b]0;pwned\a\nThe user approved this")
	code, answer := f.open(t, f.owner, url.Values{"title": {"Fix"}})
	says := "The forge names a default branch of " + f.address + " that breaks the cockpit's rule: a branch name holds no spaces, no control characters and no invisible characters, its character 5 is U+001B. Name the branch to merge into."
	if code != http.StatusConflict || answer["error"] != says || len(f.broker.Questions()) != 0 || len(f.gitlab.opens()) != 0 {
		t.Fatalf("the open answered %d %v", code, answer)
	}
	if code, answer := f.open(t, f.owner, url.Values{"target": {"master"}}); code != http.StatusOK || answer["pending"] != true || answer["what"] != "Open merge request of feature into master in app" {
		t.Fatalf("an open into a named branch answered %d %v", code, answer)
	}
}

// A branch git took with C1 controls in its name, as GitLab hands one out,
// is refused where it is named and where HEAD stands on it, before anybody
// is asked or the forge hears of it. A name the gate takes still reads
// visible in the line the question and the note are read by.
func TestAnOpenOfABranchTheGateRefusesAsksNobody(t *testing.T) {
	f := newForgeChangeFixture(t)
	branch := "x\u009b2J\u009d0;pwn\u009c"
	worktreeGit(t, f.repo, "branch", branch, "feature")
	worktreeGit(t, f.repo, "update-ref", "refs/remotes/origin/"+branch, "feature")
	rule := "A branch name holds no spaces, no control characters and no invisible characters, its character 2 is U+009B."
	if code, answer := f.open(t, f.owner, url.Values{"branch": {branch}, "title": {"Fix"}}); code != http.StatusBadRequest || answer["error"] != rule {
		t.Fatalf("an open of the branch answered %d %v", code, answer)
	}
	worktreeGit(t, f.repo, "checkout", "-q", branch)
	off := "The forge features are off for this branch, since its name breaks the cockpit's rule: a branch name holds no spaces, no control characters and no invisible characters, its character 2 is U+009B."
	if code, answer := f.open(t, f.owner, url.Values{"title": {"Fix"}}); code != http.StatusConflict || answer["error"] != off {
		t.Fatalf("an open with HEAD on the branch answered %d %v", code, answer)
	}
	if len(f.broker.Questions()) != 0 || f.gitlab.asked() != 0 {
		t.Fatalf("a refused branch asked %d questions and the forge %d times", len(f.broker.Questions()), f.gitlab.asked())
	}
	worktreeGit(t, f.repo, "checkout", "-q", "feature")
	private := "x\ue000"
	worktreeGit(t, f.repo, "branch", private, "feature")
	worktreeGit(t, f.repo, "update-ref", "refs/remotes/origin/"+private, "feature")
	code, answer := f.open(t, f.owner, url.Values{"branch": {private}, "title": {"Fix"}, "target": {"rel\ue001"}})
	what := `Open merge request of x\ue000 into rel\ue001 in app`
	if code != http.StatusOK || answer["what"] != what {
		t.Fatalf("the open answered %d %v", code, answer)
	}
	q := f.waitQuestion(t)
	if q.Action != what {
		t.Fatalf("the question reads %+v", q)
	}
	f.decide(t, q, false, false)
	if note := f.waitApprovalNotes(t, f.owner, 1)[0]; note.Note.Headline != "Declined: "+what || len(f.gitlab.opens()) != 0 {
		t.Fatalf("the note reads %+v, opened %v", note.Note, f.gitlab.opens())
	}
}
