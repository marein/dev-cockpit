package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

func guestRouteTable(t *testing.T) []gin.RouteInfo {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Server{}).registerShareRoutes(r)
	return r.Routes()
}

// No route of a guest deletes a coder or a shell, resizes a pane or reaches
// anything else no link may ever reach, and every one mirrors a path of the
// owner's or is one of the guest's own few.
func TestGuestRoutesNeverReachWhatNoLinkMay(t *testing.T) {
	guestOnly := map[string]bool{
		"":                                   true,
		"/projects/:name/docker/logs/output": true,
	}
	routes := guestRouteTable(t)
	if len(routes) == 0 {
		t.Fatal("no guest route registered")
	}
	for _, route := range routes {
		path, ok := strings.CutPrefix(route.Path, sharePathPrefix+":token")
		if !ok {
			t.Fatalf("%s %s stands outside the link's address", route.Method, route.Path)
		}
		if neverForGuest(path) {
			t.Fatalf("%s %s is never a guest's", route.Method, route.Path)
		}
		if strings.HasSuffix(path, "/delete") || strings.HasSuffix(path, "/resize") || strings.HasSuffix(path, "/input") {
			t.Fatalf("%s %s writes", route.Method, route.Path)
		}
		if guestOnly[path] {
			continue
		}
		if route.Method != http.MethodGet && !strings.HasSuffix(path, "/watch") && !strings.HasSuffix(path, "/preview") &&
			!strings.HasSuffix(path, "/file-links") && !strings.Contains(path, "/lsp/") {
			t.Fatalf("%s %s is a POST that is no read", route.Method, route.Path)
		}
	}
}

// Deleting a coder or a shell is never a guest's: the route table refuses to
// mirror it at all, so no permission added later can open it.
func TestGuestRoutesRefuseEveryTerminalDeletion(t *testing.T) {
	for _, path := range []string{
		"/coders/:id/delete", "/shells/:id/delete", "/coders/delete",
		"/coders/:id/resize", "/shells/:id/resize", "/terminal-theme",
		"/terminal-tabs/order", "/terminal-tabs/group", "/splits/:id",
		"/projects/delete", "/projects/:name/tags", "/settings/general", "/assistants/:id",
		"/projects/:name/editor/lsp/source", "/git/prompt", "/projects", "/editor",
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Fatalf("%s was mirrored for a guest", path)
				}
			}()
			r := gin.New()
			guestRoutes{group: r.Group(sharePathPrefix + ":token")}.read(http.MethodPost, path, func(*gin.Context) {})
		}()
	}
	for _, route := range guestRouteTable(t) {
		if strings.Contains(route.Path, "/coders/:id/delete") || strings.Contains(route.Path, "/shells/:id/delete") || strings.HasSuffix(route.Path, "/coders/delete") {
			t.Fatalf("%s %s deletes a terminal", route.Method, route.Path)
		}
	}
}

type guestFixture struct {
	s       *Server
	r       *gin.Engine
	root    string
	token   string
	created share.Share
}

// linkTo is what a link to the named projects opens, each read only.
func linkTo(names ...string) []share.Resource {
	out := make([]share.Resource, 0, len(names))
	for _, name := range names {
		out = append(out, share.Resource{Kind: share.KindProject, Target: name})
	}
	return out
}

func newGuestFixture(t *testing.T) *guestFixture {
	t.Helper()
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	for _, name := range []string{"shop", "blog", "other"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	s := &Server{projects: project.NewRepository(root, nil), shares: share.NewStore(t.TempDir())}
	p, err := s.projects.FindByName("shop")
	if err != nil {
		t.Fatal(err)
	}
	created, token, err := s.shares.Create("", linkTo(p.Name), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.SetHTMLTemplate(render.HTMLTemplate(func(p string) string { return p }, "test", "test", nil, nil))
	r.NoRoute(s.handleNotFound)
	g := guestRoutes{group: r.Group(sharePathPrefix+":token", s.openShare)}
	g.read(http.MethodGet, "", s.handleShareEntry)
	g.read(http.MethodGet, "/time", s.handleServerTime)
	g.read(http.MethodPost, "/projects/:name/editor/preview", s.handleEditorPreview)
	return &guestFixture{s: s, r: r, root: root, token: token, created: created}
}

func (f *guestFixture) do(method, target, body string, header http.Header) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	for key, values := range header {
		req.Header[key] = values
	}
	rec := httptest.NewRecorder()
	f.r.ServeHTTP(rec, req)
	return rec
}

func TestALinkLandsOnItsProjectsEditorAndMintsNoSession(t *testing.T) {
	f := newGuestFixture(t)
	rec := f.do(http.MethodGet, sharePathPrefix+f.token, "", nil)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != sharePathPrefix+f.token+"/projects/shop/editor" {
		t.Fatalf("entry = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Fatalf("headers %v", rec.Header())
	}
	if cookies := rec.Header().Values("Set-Cookie"); len(cookies) > 0 {
		t.Fatalf("a guest got a cookie: %v", cookies)
	}
}

// Unknown, revoked, expired and deleted links, and a link asked for another
// project, answer exactly what an address that does not exist answers.
func TestEveryDeadLinkAnswersTheSame404(t *testing.T) {
	f := newGuestFixture(t)
	missing := f.do(http.MethodGet, "/no/such/address", "", nil)
	if missing.Code != http.StatusNotFound {
		t.Fatalf("no route = %d", missing.Code)
	}
	revoked, revokedToken, _ := f.s.shares.Create("", linkTo("shop"), time.Hour)
	if err := f.s.shares.Revoke(revoked.ID); err != nil {
		t.Fatal(err)
	}
	_, otherToken, _ := f.s.shares.Create("", linkTo("other"), time.Hour)
	if err := os.RemoveAll(filepath.Join(f.root, "other")); err != nil {
		t.Fatal(err)
	}
	emptied, emptiedToken, _ := f.s.shares.Create("", linkTo("blog"), time.Hour)
	if _, err := f.s.shares.Remove(emptied.ID, share.KindProject, "blog"); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"unknown token":         sharePathPrefix + strings.Repeat("A", 43),
		"malformed token":       sharePathPrefix + "nope",
		"revoked":               sharePathPrefix + revokedToken,
		"deleted project":       sharePathPrefix + otherToken,
		"deleted project, time": sharePathPrefix + otherToken + "/time",
		"last project removed":  sharePathPrefix + emptiedToken,
		"other project by name": sharePathPrefix + f.token + "/projects/blog/editor/preview",
	} {
		method := http.MethodGet
		if strings.HasSuffix(target, "/preview") {
			method = http.MethodPost
		}
		rec := f.do(method, target, "", nil)
		if rec.Code != http.StatusNotFound || rec.Body.String() != missing.Body.String() {
			t.Fatalf("%s = %d, want the plain 404", name, rec.Code)
		}
	}
}

// A project added to a standing link opens under the same token, and one
// taken off answers the plain 404 under it while the others still open.
func TestALinksProjectsComeAndGoUnderOneToken(t *testing.T) {
	f := newGuestFixture(t)
	missing := f.do(http.MethodGet, "/no/such/address", "", nil)
	preview := func(project string) *httptest.ResponseRecorder {
		header := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {"application/json"}}
		header.Set("X-CSRF-Token", guestCSRF(f.token))
		return f.do(http.MethodPost, sharePathPrefix+f.token+"/projects/"+project+"/editor/preview", "content=x", header)
	}
	if rec := preview("blog"); rec.Code != http.StatusNotFound {
		t.Fatalf("blog before it went on the link = %d", rec.Code)
	}
	if err := f.s.shares.Add(f.created.ID, share.Resource{Kind: share.KindProject, Target: "blog"}); err != nil {
		t.Fatal(err)
	}
	if rec := preview("blog"); rec.Code != http.StatusOK {
		t.Fatalf("blog on the link = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := f.s.shares.Remove(f.created.ID, share.KindProject, "shop"); err != nil {
		t.Fatal(err)
	}
	if rec := f.do(http.MethodPost, sharePathPrefix+f.token+"/projects/shop/editor/preview", "", nil); rec.Code != http.StatusNotFound || rec.Body.String() != missing.Body.String() {
		t.Fatalf("shop off the link = %d, want the plain 404", rec.Code)
	}
	if rec := preview("blog"); rec.Code != http.StatusOK {
		t.Fatalf("blog after shop went = %d", rec.Code)
	}
	if rec := f.do(http.MethodGet, sharePathPrefix+f.token, "", nil); rec.Header().Get("Location") != sharePathPrefix+f.token+"/projects/blog/editor" {
		t.Fatalf("the entry leads to %q", rec.Header().Get("Location"))
	}
}

// A link opened while its project is gone from disk is dropped, so the same
// name made again after that opens nothing. A project that only cannot be read
// keeps its link.
func TestAProjectGoneFromDiskTakesItsLinks(t *testing.T) {
	f := newGuestFixture(t)
	missing := f.do(http.MethodGet, "/no/such/address", "", nil)
	shop := filepath.Join(f.root, "shop")
	if err := os.RemoveAll(shop); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(shop, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := f.do(http.MethodGet, sharePathPrefix+f.token, "", nil); rec.Code != http.StatusNotFound || len(f.s.shares.List()) != 1 {
		t.Fatalf("unreadable project = %d with %d links, want the 404 and the link kept", rec.Code, len(f.s.shares.List()))
	}
	if err := os.Remove(shop); err != nil {
		t.Fatal(err)
	}
	rec := f.do(http.MethodGet, sharePathPrefix+f.token, "", nil)
	if rec.Code != http.StatusNotFound || rec.Body.String() != missing.Body.String() || len(f.s.shares.List()) != 0 {
		t.Fatalf("gone project = %d with %d links, want the plain 404 and no link", rec.Code, len(f.s.shares.List()))
	}
	if err := os.MkdirAll(shop, 0o755); err != nil {
		t.Fatal(err)
	}
	if rec := f.do(http.MethodGet, sharePathPrefix+f.token, "", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("the name made again = %d, want the 404", rec.Code)
	}
}

func TestAGuestPostNeedsTheLinksOwnCSRFToken(t *testing.T) {
	f := newGuestFixture(t)
	target := sharePathPrefix + f.token + "/projects/shop/editor/preview"
	form := url.Values{"content": {"# hi"}}.Encode()
	plain := http.Header{"Content-Type": {"application/x-www-form-urlencoded"}, "Accept": {"application/json"}}
	if rec := f.do(http.MethodPost, target, form, plain); rec.Code != http.StatusForbidden {
		t.Fatalf("without a token = %d", rec.Code)
	}
	wrong := plain.Clone()
	wrong.Set("X-CSRF-Token", f.token)
	if rec := f.do(http.MethodPost, target, form, wrong); rec.Code != http.StatusForbidden {
		t.Fatalf("with the link's token as CSRF = %d", rec.Code)
	}
	right := plain.Clone()
	right.Set("X-CSRF-Token", guestCSRF(f.token))
	if rec := f.do(http.MethodPost, target, form, right); rec.Code != http.StatusOK {
		t.Fatalf("with the CSRF token = %d %s", rec.Code, rec.Body.String())
	}
}

func TestAGuestsEventStreamCarriesOnlyItsProjectsSignals(t *testing.T) {
	cases := []struct {
		ev   eventbus.Event
		want bool
	}{
		{eventbus.Event{Type: "terminals", Data: map[string]string{"project": "shop"}}, true},
		{eventbus.Event{Type: "terminals", Data: map[string]string{"project": ""}}, true},
		{eventbus.Event{Type: "terminals", Data: map[string]string{"project": "other"}}, false},
		{eventbus.Event{Type: "git", Data: map[string]any{"project": "shop", "base": true}}, true},
		{eventbus.Event{Type: "lsp", Data: map[string]string{"project": "blog"}}, true},
		{eventbus.Event{Type: "files", Data: map[string]any{"project": "other"}}, false},
		{eventbus.Event{Type: "docker"}, true},
		{eventbus.Event{Type: "gitprompt"}, false},
		{eventbus.Event{Type: "notifications"}, false},
		{eventbus.Event{Type: "projects"}, false},
		{eventbus.Event{Type: "commitdraft", Data: map[string]string{"project": "shop"}}, false},
		{eventbus.Event{Type: "searchdraft", Data: map[string]string{"project": "shop"}}, false},
		{eventbus.Event{Type: "assistant", Data: map[string]string{}}, false},
		{eventbus.Event{Type: "host"}, false},
		{eventbus.Event{Type: "something-new"}, false},
	}
	for _, tc := range cases {
		if got := guestEvent([]string{"shop", "blog"}, tc.ev); got != tc.want {
			t.Fatalf("%s %v = %v, want %v", tc.ev.Type, tc.ev.Data, got, tc.want)
		}
	}
}

func TestTheZeroAccessIsTheOwnersAndAGuestsGrantsNothingYet(t *testing.T) {
	owner := render.Access{}
	guest := render.GuestAccess("/share/x", share.Share{Resources: linkTo("shop")}, "shop")
	for _, p := range share.All {
		if !owner.Can(p) || guest.Can(p) {
			t.Fatalf("%s: owner %v, guest %v", p, owner.Can(p), guest.Can(p))
		}
	}
	granted := render.GuestAccess("/share/x", share.Share{Resources: []share.Resource{
		{Kind: share.KindProject, Target: "shop", Permissions: []share.Permission{share.Files}},
		{Kind: share.KindProject, Target: "blog"},
	}}, "blog")
	if granted.Can(share.Files) || granted.Project != "blog" {
		t.Fatalf("blog borrowed what shop grants: %+v", granted)
	}
	if guest.Grants() != "" || len(strings.Fields(owner.Grants())) != len(share.All) {
		t.Fatalf("grants: owner %q, guest %q", owner.Grants(), guest.Grants())
	}
}

// A guest's event stream carries the snapshot of its projects' signals and
// ends the moment its link is revoked, the project's deletion included, also
// one outside the cockpit.
func TestAGuestsEventStreamEndsWithItsLink(t *testing.T) {
	for _, end := range []string{"revoke", "delete project", "gone from disk"} {
		f := newGuestFixture(t)
		f.s.bus = eventbus.New()
		f.r.GET(sharePathPrefix+":token/events", f.s.openShare, f.s.handleShareEvents)
		rec := httptest.NewRecorder()
		done := make(chan struct{})
		go func() {
			defer close(done)
			f.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, sharePathPrefix+f.token+"/events", nil))
		}()
		time.Sleep(100 * time.Millisecond)
		f.s.bus.Publish(eventbus.Event{Type: "gitprompt"})
		f.s.bus.Publish(eventbus.Event{Type: "terminals", Data: map[string]string{"project": "other"}})
		time.Sleep(50 * time.Millisecond)
		switch end {
		case "revoke":
			if err := f.s.shares.Revoke(f.created.ID); err != nil {
				t.Fatal(err)
			}
		case "delete project":
			f.s.unshareProject("shop")
		default:
			if err := os.RemoveAll(filepath.Join(f.root, "shop")); err != nil {
				t.Fatal(err)
			}
			f.s.bus.Publish(eventbus.Event{Type: "projects"})
		}
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: the guest's stream outlived its link", end)
		}
		body := rec.Body.String()
		if !strings.Contains(body, `"type":"terminals"`) || !strings.Contains(body, `"type":"docker"`) {
			t.Fatalf("%s: no snapshot reached the guest: %q", end, body)
		}
		for _, unwanted := range []string{"gitprompt", `"other"`, "notifications"} {
			if strings.Contains(body, unwanted) {
				t.Fatalf("%s: the guest's stream carries %s: %q", end, unwanted, body)
			}
		}
	}
}

// A project added to a link reaches its open guest as the projects signal
// after the snapshot's own, one of two going keeps the stream and signals
// again, the last one ends it.
func TestAGuestsEventStreamFollowsItsLinksProjects(t *testing.T) {
	f := newGuestFixture(t)
	f.s.bus = eventbus.New()
	f.r.GET(sharePathPrefix+":token/events", f.s.openShare, f.s.handleShareEvents)
	rec := &lockedRecorder{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, sharePathPrefix+f.token+"/events", nil))
	}()
	waitBody := func(what string, want int) {
		t.Helper()
		until := time.Now().Add(3 * time.Second)
		for time.Now().Before(until) {
			if strings.Count(rec.body(), `"type":"projects"`) >= want {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("%s: %d projects signals, want %d: %q", what, strings.Count(rec.body(), `"type":"projects"`), want, rec.body())
	}
	time.Sleep(100 * time.Millisecond)
	if strings.Count(rec.body(), `"type":"projects"`) != 1 {
		t.Fatalf("the snapshot's projects signal is not the only one before anything moved: %q", rec.body())
	}
	f.s.bus.Publish(eventbus.Event{Type: "files", Data: map[string]any{"project": "blog"}})
	if err := f.s.shares.Add(f.created.ID, share.Resource{Kind: share.KindProject, Target: "blog"}); err != nil {
		t.Fatal(err)
	}
	waitBody("blog added", 2)
	f.s.bus.Publish(eventbus.Event{Type: "files", Data: map[string]any{"project": "blog"}})
	until := time.Now().Add(3 * time.Second)
	for !strings.Contains(rec.body(), `"project":"blog"`) && time.Now().Before(until) {
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Count(rec.body(), `"project":"blog"`) != 1 {
		t.Fatalf("blog's signals before and after it went on the link: %q", rec.body())
	}
	if _, err := f.s.shares.Remove(f.created.ID, share.KindProject, "shop"); err != nil {
		t.Fatal(err)
	}
	waitBody("shop removed", 3)
	select {
	case <-done:
		t.Fatal("the stream ended while blog is still on the link")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := f.s.shares.Remove(f.created.ID, share.KindProject, "blog"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("the stream outlived its link's last project")
	}
}

// A terminal's stream follows the one project it belongs to: another project
// leaving the link keeps it, its own leaving ends it.
func TestATerminalStreamEndsWithItsProjectOnTheLink(t *testing.T) {
	f := newGuestFixture(t)
	f.s.bus = eventbus.New()
	if err := f.s.shares.Add(f.created.ID, share.Resource{Kind: share.KindProject, Target: "blog"}); err != nil {
		t.Fatal(err)
	}
	sh, err := f.s.shares.Lookup(f.token)
	if err != nil {
		t.Fatal(err)
	}
	g := &guest{share: sh, base: sharePathPrefix + f.token}
	blog, _ := f.s.projects.FindByName("blog")
	g.bind(blog)
	ctx, cancel := f.s.followShare(context.Background(), g, func(current share.Share) bool {
		_, ok := f.s.liveShareProject(current, g.project.Name)
		return ok
	})
	defer cancel()
	if _, err := f.s.shares.Remove(f.created.ID, share.KindProject, "shop"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ctx.Done():
		t.Fatal("blog's stream ended when shop left the link")
	case <-time.After(200 * time.Millisecond):
	}
	if err := f.s.shares.Update(f.created.ID, "renamed", 0); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(f.root, "blog")); err != nil {
		t.Fatal(err)
	}
	f.s.bus.Publish(eventbus.Event{Type: "projects"})
	select {
	case <-ctx.Done():
	case <-time.After(3 * time.Second):
		t.Fatal("blog's stream outlived blog")
	}
	if len(f.s.shares.List()) != 0 {
		t.Fatal("the link outlived its last project")
	}
}

// lockedRecorder lets a test read a stream's body while the handler writes it.
type lockedRecorder struct {
	*httptest.ResponseRecorder
	mu sync.Mutex
}

func (r *lockedRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ResponseRecorder.Write(p)
}

func (r *lockedRecorder) WriteString(text string) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ResponseRecorder.WriteString(text)
}

func (r *lockedRecorder) body() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.Body.String()
}
