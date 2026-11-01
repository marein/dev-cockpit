package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/marein/dev-cockpit/internal/forge"
	"github.com/marein/dev-cockpit/internal/forge/github"
	"github.com/marein/dev-cockpit/internal/forge/gitlab"
)

func newForgeSession(t *testing.T) *sharingSession {
	t.Helper()
	ss := newSharingSession(t)
	ss.s.SetForge(forge.New(t.TempDir(), []forge.Provider{github.Provider{}, gitlab.Provider{}}, github.Builtin(), gitlab.Builtin()))
	return ss
}

func hostForm(kind, webURL, aliases string) url.Values {
	return url.Values{"kind": {kind}, "web_url": {webURL}, "aliases": {aliases}}
}

// The list shows the built in hosts without an Edit and with the link to
// their token view, an added host stands on it with its kind, address and
// alias hosts and its Edit, and the dialog leads on to the host's token
// rather than a redirect. The add takes no token, not even one a request
// sends.
func TestAHostIsAddedFromTheDialog(t *testing.T) {
	ss := newForgeSession(t)
	list := ss.get(settingsForgesPath)
	for _, want := range []string{`data-forge-host="https://github.com"`, `data-forge-host="https://gitlab.com"`, "ssh.github.com", "altssh.gitlab.com", "GitHub, built in", `href="/settings/forges/github.com/token" aria-label="Add a token to https://github.com" data-forge-token-link>add token</a>`, `id="forge-host-gitlab.com"`, "no token"} {
		if !strings.Contains(list, want) {
			t.Fatalf("the list lacks %s", want)
		}
	}
	for _, unwanted := range []string{"data-forge-test", "data-forge-edit", "data-forge-token-page", "personal_access_tokens"} {
		if strings.Contains(list, unwanted) {
			t.Fatalf("the list of built in hosts without a token holds %s", unwanted)
		}
	}
	form := ss.get(settingsForgesNewPath + "?modal=1")
	if !strings.Contains(form, `action="/settings/forges/new?modal=1"`) || !strings.Contains(form, `<option value="gitlab">GitLab</option>`) || strings.Contains(form, "data-forge-delete") || strings.Contains(form, `name="token"`) {
		t.Fatalf("the new form: %s", form)
	}
	sent := hostForm("gitlab", "https://git.example.com/gitlab/", "gl, ssh.git.example.com")
	sent.Set("token", "glpat-Xq3vR8sT2uW5yZ7aB9cD")
	rec := ss.post(settingsForgesNewPath+"?modal=1", sent)
	if hints, _ := ss.s.forge.TokenHints(); len(hints) != 0 {
		t.Fatalf("the add stored a token: %v", hints)
	}
	hosts, _ := ss.s.forge.Hosts()
	added := hosts[len(hosts)-1]
	if len(hosts) != 3 || added.WebURL != "https://git.example.com/gitlab" || strings.Join(added.Aliases, " ") != "gl ssh.git.example.com" {
		t.Fatalf("hosts %+v", hosts)
	}
	view := rec.Body.String()
	for _, want := range []string{
		`action="` + forgeTokenPath(added.ID) + `?modal=1"`, `data-form-fixed data-form-leave="/settings/forges#` + forgeHostAnchor(added.ID) + `" data-form-title="https://git.example.com/gitlab"`,
		"A token lets the cockpit act on GitLab at https://git.example.com/gitlab as you, give it one now or later from its row.",
		`href="https://git.example.com/gitlab/-/user_settings/personal_access_tokens?name=dev-cockpit&amp;scopes=api" target="_blank" rel="noopener" data-forge-token-page>New token</a>`, "required>", "data-forge-token-cancel>Later<", "Save and test",
	} {
		if rec.Code != http.StatusOK || !strings.Contains(view, want) {
			t.Fatalf("the add answered %d without %s: %s", rec.Code, want, view)
		}
	}
	for _, unwanted := range []string{"<html", `name="kind"`, `name="token_remove"`, "data-forge-token-remove", "/-/profile/"} {
		if strings.Contains(view, unwanted) {
			t.Fatalf("the token view holds %s", unwanted)
		}
	}
	list = ss.get(settingsForgesPath)
	if strings.Contains(list, "Host added.") || !strings.Contains(list, `href="`+forgeHostPath(added.ID)+`"`) || !strings.Contains(list, `href="`+forgeTokenPath(added.ID)+`"`) || !strings.Contains(list, "gl, ssh.git.example.com") {
		t.Fatalf("the list after the add: %s", list)
	}
}

// Without the dialog an add lands on the host's token page, which leads
// back to the list.
func TestAHostAddedOnThePageLandsOnItsToken(t *testing.T) {
	ss := newForgeSession(t)
	rec := ss.post(settingsForgesNewPath, hostForm("github", "https://ghe.example.com", ""))
	hosts, _ := ss.s.forge.Hosts()
	added := hosts[len(hosts)-1]
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != forgeTokenPath(added.ID) {
		t.Fatalf("the add answered %d to %s", rec.Code, rec.Header().Get("Location"))
	}
	page := ss.get(forgeTokenPath(added.ID))
	for _, want := range []string{"Host added.", `action="` + forgeTokenPath(added.ID) + `"`, "https://ghe.example.com/settings/tokens/new?description=dev-cockpit", `href="/settings/forges#` + forgeHostAnchor(added.ID) + `" data-forge-token-cancel>Later<`} {
		if !strings.Contains(page, want) {
			t.Fatalf("the token page lacks %s: %s", want, page)
		}
	}
	if strings.Contains(page, "data-form-leave") {
		t.Fatal("the page carries the dialog's way out")
	}
}

// The token view keeps the token typed, an empty field the one there is,
// and tests it: one that works lands on the host's row, one that fails
// answers the view again with why, in the dialog and on the page, which
// then names the kept token and offers to replace and remove it. A token
// refused before it is kept leaves the dialog's view standing, as JSON.
// Remove takes it away and lands on the row, typing one at once is refused.
func TestTheTokenViewKeepsAndTestsTheToken(t *testing.T) {
	const token = "glpat-Xq3vR8sT2uW5yZ7aB9cD"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"401 Unauthorized"}`))
			return
		}
		switch r.URL.Path {
		case "/api/v4/version":
			w.Write([]byte(`{"version":"17.5.1","revision":"8ff7d9d4d4a"}`))
		case "/api/v4/user":
			w.Write([]byte(`{"id":1,"username":"root","name":"Administrator","state":"active"}`))
		}
	}))
	defer fake.Close()
	ss := newForgeSession(t)
	h, err := ss.s.forge.Add(forge.Input{Kind: "gitlab", WebURL: fake.URL})
	if err != nil {
		t.Fatal(err)
	}
	path := forgeTokenPath(h.ID)
	answer := func(rec *httptest.ResponseRecorder) (int, string) {
		var got struct{ Error, Location string }
		if json.Unmarshal(rec.Body.Bytes(), &got) != nil {
			t.Fatalf("answered %d %s", rec.Code, rec.Body.String())
		}
		return rec.Code, got.Error + got.Location
	}
	view := func(rec *httptest.ResponseRecorder, wants ...string) {
		t.Helper()
		got := rec.Body.String()
		for _, want := range wants {
			if rec.Code != http.StatusConflict || !strings.Contains(got, want) {
				t.Fatalf("the dialog answered %d without %s: %s", rec.Code, want, got)
			}
		}
		if strings.Contains(got, "<html") {
			t.Fatalf("the dialog got a page: %s", got)
		}
	}
	view(ss.post(path+"?modal=1", url.Values{}), `<div class="alert alert-danger" role="alert" data-form-modal-error>The host has no token to test.</div>`, "A personal access token of your account on the server.", "Save and test")
	if code, said := answer(ss.post(path+"?modal=1", url.Values{"token": {"glpat x"}})); code != http.StatusBadRequest || said != "A token is one line of letters, digits and signs, without spaces." {
		t.Fatalf("a token with a space answered %d %q", code, said)
	}
	view(ss.post(path+"?modal=1", url.Values{"token": {"glpat-wrong-wrong-wrong"}}),
		`data-form-modal-error>GitLab refused the token: it is wrong, expired or revoked.</div>`, `action="`+path+`?modal=1"`, `data-form-leave="/settings/forges#`+forgeHostAnchor(h.ID)+`"`,
		"A token is set, it ends in <code>…rong</code>.", "A new token replaces the token of GitLab at "+fake.URL+", Remove takes it away.", "data-forge-token-remove>Remove<", "data-forge-token-cancel>Cancel<", "Replace and test")
	rec := ss.post(path, url.Values{})
	page := rec.Body.String()
	if strings.Contains(page, "data-form-modal-error") {
		t.Fatalf("the page carries the dialog's refusal: %s", page)
	}
	for _, want := range []string{
		"GitLab refused the token", `action="` + path + `"`, "A token is set, it ends in <code>…rong</code>.", "A new token replaces the token of GitLab at " + fake.URL + ", Remove takes it away.",
		"data-forge-token-remove>Remove<", `data-confirm="Remove the token of ` + fake.URL + `?"`, `name="token_remove" value="1"`, "data-forge-token-cancel>Cancel<", "Replace and test", "required>",
	} {
		if rec.Code != http.StatusConflict || !strings.Contains(page, want) {
			t.Fatalf("the page test of the kept wrong token answered %d without %s: %s", rec.Code, want, page)
		}
	}
	if strings.Contains(ss.get(settingsForgesPath), "GitLab refused the token") {
		t.Fatal("the refusal rode the session")
	}
	if code, said := answer(ss.post(path+"?modal=1", url.Values{"token": {token}})); code != http.StatusOK || said != settingsForgesPath+"#"+forgeHostAnchor(h.ID) {
		t.Fatalf("a working token answered %d %q", code, said)
	}
	list := ss.get(settingsForgesPath)
	if !strings.Contains(list, "The token works.") || !strings.Contains(list, "data-forge-tested>Connected to GitLab 17.5.1 as root (Administrator).<") || strings.Contains(list, token) {
		t.Fatalf("the list after the test: %s", list)
	}
	rec = ss.post(path, url.Values{})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != settingsForgesPath+"#"+forgeHostAnchor(h.ID) {
		t.Fatalf("the page test answered %d to %s", rec.Code, rec.Header().Get("Location"))
	}
	ss.get(settingsForgesPath)
	if code, said := answer(ss.post(path+"?modal=1", url.Values{"token": {token}, "token_remove": {"1"}})); code != http.StatusBadRequest || said != "Type a new token or remove the one there is, not both." {
		t.Fatalf("a token typed with Remove answered %d %q", code, said)
	}
	if code, said := answer(ss.post(path+"?modal=1", url.Values{"token_remove": {"1"}})); code != http.StatusOK || said != settingsForgesPath+"#"+forgeHostAnchor(h.ID) {
		t.Fatalf("Remove answered %d %q", code, said)
	}
	if hints, _ := ss.s.forge.TokenHints(); len(hints) != 0 {
		t.Fatalf("Remove left %v", hints)
	}
	list = ss.get(settingsForgesPath)
	if !strings.Contains(list, "Token removed.") || !strings.Contains(list, `aria-label="Add a token to `+fake.URL+`" data-forge-token-link>add token</a>`) || strings.Contains(list, "data-forge-test>") {
		t.Fatalf("the list after Remove: %s", list)
	}
	if err := ss.s.forge.ChangeToken(h.ID, token, false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ss.get(settingsForgesPath), `aria-label="Change the token of `+fake.URL+`" data-forge-token-link>change token</a>`) {
		t.Fatal("a row with a token does not offer to change it")
	}
	rec = ss.post(path, url.Values{"token_remove": {"1"}})
	if hints, _ := ss.s.forge.TokenHints(); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != settingsForgesPath+"#"+forgeHostAnchor(h.ID) || len(hints) != 0 {
		t.Fatalf("the page Remove answered %d to %s and left %v", rec.Code, rec.Header().Get("Location"), hints)
	}
	gone := forgeTokenPath("nope")
	if code, said := answer(ss.post(gone+"?modal=1", url.Values{"token": {token}})); code != http.StatusNotFound || said != forge.ErrNotFound.Error() {
		t.Fatalf("the dialog of no host answered %d %q", code, said)
	}
	send, _ := ownerSession(t, ss.s)
	if rec := send(httptest.NewRequest(http.MethodGet, gone, nil)); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `data-forge-host="https://github.com"`) {
		t.Fatalf("the token view of no host answered %d", rec.Code)
	}
	if rec := ss.post(gone, url.Values{}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `data-forge-host="https://github.com"`) {
		t.Fatalf("the page save of no host answered %d", rec.Code)
	}
}

// A refusal keeps the dialog standing with the sentence, the page shows the
// form again with what was sent under it, and nothing is stored or flashed.
func TestAHostTheServerRefusesIsNotStored(t *testing.T) {
	ss := newForgeSession(t)
	if got := refusal(t, ss.post(settingsForgesNewPath+"?modal=1", hostForm("gitlab", "https://user:pw@git.example.com", ""))); got != "A web address carries no user name or password." {
		t.Fatalf("refused with %q", got)
	}
	if got := refusal(t, ss.post(settingsForgesNewPath+"?modal=1", hostForm("gitlab", "https://git.example.com", "gl  git@x"))); got != `"git@x" is no host name.` {
		t.Fatalf("refused with %q", got)
	}
	rec := ss.post(settingsForgesNewPath, hostForm("svn", "https://git.example.com", "gl"))
	page := rec.Body.String()
	if rec.Code != http.StatusBadRequest || !strings.Contains(page, "Pick the kind of forge.") || !strings.Contains(page, `value="https://git.example.com"`) || !strings.Contains(page, `value="gl"`) {
		t.Fatalf("the page refusal answered %d: %s", rec.Code, page)
	}
	if strings.Contains(ss.get(settingsForgesNewPath), "Pick the kind of forge.") {
		t.Fatal("the refusal rode the session")
	}
	if hosts, _ := ss.s.forge.Hosts(); len(hosts) != 2 {
		t.Fatalf("a refusal stored %+v", hosts)
	}
}

// An added host's Edit shows it as it stands, saves a new kind, address and
// alias hosts, and deletes it; a host that is gone shows the list under why
// it is not found.
func TestAnAddedHostIsChangedAndDeleted(t *testing.T) {
	ss := newForgeSession(t)
	h, err := ss.s.forge.Add(forge.Input{Kind: "gitlab", WebURL: "https://git.example.com", Aliases: []string{"gl"}})
	if err != nil {
		t.Fatal(err)
	}
	path := forgeHostPath(h.ID)
	form := ss.get(path + "?modal=1")
	for _, want := range []string{`value="https://git.example.com"`, `value="gl"`, `<option value="gitlab" selected>GitLab</option>`, `name="form" value="save"`, "data-forge-delete", `data-form-title="https://git.example.com"`} {
		if !strings.Contains(form, want) {
			t.Fatalf("the Edit lacks %s: %s", want, form)
		}
	}
	for _, unwanted := range []string{`name="token"`, `name="token_remove"`, "data-forge-token-page", "personal_access_tokens"} {
		if strings.Contains(form, unwanted) {
			t.Fatalf("the Edit holds %s", unwanted)
		}
	}
	if got := refusal(t, ss.post(path+"?modal=1", url.Values{"form": {"save"}, "kind": {"github"}, "web_url": {"https://github.com"}})); got != "github.com already belongs to https://github.com." {
		t.Fatalf("refused with %q", got)
	}
	rec := ss.post(path, url.Values{"form": {"save"}, "kind": {"github"}, "web_url": {"https://github.com"}, "aliases": {"ghx"}})
	if page := rec.Body.String(); rec.Code != http.StatusBadRequest || !strings.Contains(page, "github.com already belongs to https://github.com.") || !strings.Contains(page, `value="ghx"`) || !strings.Contains(page, `<option value="github" selected>GitHub</option>`) {
		t.Fatalf("the page refusal answered %d: %s", rec.Code, page)
	}
	rec = ss.post(path+"?modal=1", url.Values{"form": {"save"}, "kind": {"github"}, "web_url": {"https://github.example.com"}, "aliases": {"ghe"}, "token": {"ghp_16C7e42F292c6912E7710c838347Ae178B4a"}})
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"location":"/settings/forges"`) {
		t.Fatalf("the save answered %d %s", rec.Code, rec.Body.String())
	}
	if hints, _ := ss.s.forge.TokenHints(); len(hints) != 0 {
		t.Fatalf("the Edit stored a token: %v", hints)
	}
	if got, _ := ss.s.forge.Host(h.ID); got.Kind != github.Kind || got.WebURL != "https://github.example.com" || strings.Join(got.Aliases, " ") != "ghe" {
		t.Fatalf("saved %+v", got)
	}
	rec = ss.post(path, url.Values{"form": {"delete"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != settingsForgesPath || !strings.Contains(ss.get(settingsForgesPath), "Host deleted.") {
		t.Fatalf("the delete answered %d to %s", rec.Code, rec.Header().Get("Location"))
	}
	if hosts, _ := ss.s.forge.Hosts(); len(hosts) != 2 {
		t.Fatalf("after the delete %+v", hosts)
	}
	send, _ := ownerSession(t, ss.s)
	for _, gone := range []string{path, forgeHostPath("nope")} {
		rec := send(httptest.NewRequest(http.MethodGet, gone, nil))
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), forge.ErrNotFound.Error()) || !strings.Contains(rec.Body.String(), `data-forge-host="https://github.com"`) {
			t.Fatalf("GET %s answered %d", gone, rec.Code)
		}
	}
	if rec := ss.post(path, url.Values{"form": {"save"}, "kind": {"gitlab"}, "web_url": {"https://x.example.com"}}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), forge.ErrNotFound.Error()) {
		t.Fatalf("a page save of a gone host answered %d", rec.Code)
	}
	if rec := ss.post(path+"?modal=1", url.Values{"form": {"save"}, "kind": {"gitlab"}, "web_url": {"https://x.example.com"}}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), forge.ErrNotFound.Error()) {
		t.Fatalf("a save of a gone host answered %d %s", rec.Code, rec.Body.String())
	}
	if rec := ss.post(path, url.Values{"form": {"other"}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("an unknown action answered %d", rec.Code)
	}
}

// A built in host has no Edit, its token view takes the token, which never
// comes back: the list and the view show its last characters, and Remove
// takes it away.
func TestABuiltInHostTakesATokenThatIsNeverShownBack(t *testing.T) {
	ss := newForgeSession(t)
	edit := forgeHostPath("github.com")
	send, _ := ownerSession(t, ss.s)
	if rec := send(httptest.NewRequest(http.MethodGet, edit+"?modal=1", nil)); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), forgeBuiltinRefusal) || strings.Contains(rec.Body.String(), "data-forge-host-form") {
		t.Fatalf("the Edit of a built in host answered %d", rec.Code)
	}
	if rec := ss.post(edit+"?modal=1", url.Values{"form": {"save"}, "kind": {"gitlab"}, "web_url": {"https://elsewhere.example.com"}}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"error":"`+forgeBuiltinRefusal+`"`) {
		t.Fatalf("a save of a built in host answered %d %s", rec.Code, rec.Body.String())
	}
	if rec := ss.post(edit, url.Values{"form": {"save"}, "kind": {"gitlab"}, "web_url": {"https://elsewhere.example.com"}}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), forgeBuiltinRefusal) || !strings.Contains(rec.Body.String(), `data-forge-host="https://github.com"`) {
		t.Fatalf("a page save of a built in host answered %d", rec.Code)
	}
	if h, _ := ss.s.forge.Host("github.com"); h.WebURL != "https://github.com" || h.Kind != github.Kind {
		t.Fatalf("a save changed the built in host: %+v", h)
	}
	path := forgeTokenPath("github.com")
	form := ss.get(path + "?modal=1")
	for _, want := range []string{`action="/settings/forges/github.com/token?modal=1"`, `data-form-title="https://github.com"`, "A token lets the cockpit act on GitHub at https://github.com as you", `name="token"`, `type="password"`, `href="https://github.com/settings/tokens/new?description=dev-cockpit&amp;scopes=repo,workflow,read:org"`} {
		if !strings.Contains(form, want) {
			t.Fatalf("the token view lacks %s: %s", want, form)
		}
	}
	const token = "ghp_16C7e42F292c6912E7710c838347Ae178B4a"
	if err := ss.s.forge.ChangeToken("github.com", token, false); err != nil {
		t.Fatal(err)
	}
	list := ss.get(settingsForgesPath)
	form = ss.get(path + "?modal=1")
	for _, page := range []string{list, form} {
		if strings.Contains(page, token) || strings.Contains(page, token[:12]) {
			t.Fatal("a page shows the token")
		}
	}
	if !strings.Contains(list, "token …8B4a") || !strings.Contains(list, "data-forge-test") || !strings.Contains(list, ">change token</a>") {
		t.Fatalf("the list does not show the token: %s", list)
	}
	if !strings.Contains(form, "<code>…8B4a</code>") || !strings.Contains(form, "data-forge-token-remove") {
		t.Fatalf("the token view does not show the token: %s", form)
	}
	if got := refusal(t, ss.post(path+"?modal=1", url.Values{"token": {"ghp_x y"}})); got != "A token is one line of letters, digits and signs, without spaces." {
		t.Fatalf("refused with %q", got)
	}
	ss.post(path+"?modal=1", url.Values{"token_remove": {"1"}})
	if hints, _ := ss.s.forge.TokenHints(); len(hints) != 0 {
		t.Fatalf("Remove left %v", hints)
	}
	if rec := ss.post(edit, url.Values{"form": {"delete"}}); rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), forge.ErrNotFound.Error()) {
		t.Fatalf("a delete of a built in host answered %d", rec.Code)
	}
	if hosts, _ := ss.s.forge.Hosts(); len(hosts) != 2 {
		t.Fatalf("a built in host was deleted: %+v", hosts)
	}
}

// Test asks the host's server whom its token belongs to: one that went
// through leads to the list, whose row says so in a fixed sentence once and
// shows what it found until the token changes; one that failed shows the
// list with why on the row, and nothing rides the session.
func TestATestLandsOnTheHostsRow(t *testing.T) {
	const token = "glpat-Xq3vR8sT2uW5yZ7aB9cD"
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"message":"401 Unauthorized"}`))
			return
		}
		switch r.URL.Path {
		case "/api/v4/version":
			w.Write([]byte(`{"version":"17.5.1","revision":"8ff7d9d4d4a"}`))
		case "/api/v4/user":
			w.Write([]byte(`{"id":1,"username":"root","name":"Administrator","state":"active"}`))
		}
	}))
	defer fake.Close()
	ss := newForgeSession(t)
	h := addForgeHost(t, ss.s.forge, forge.Input{Kind: "gitlab", WebURL: fake.URL}, token)
	anchor := forgeHostAnchor(h.ID)
	rec := ss.post(forgeHostPath(h.ID), url.Values{"form": {"test"}})
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != settingsForgesPath+"#"+anchor {
		t.Fatalf("the test answered %d to %s", rec.Code, rec.Header().Get("Location"))
	}
	row := func(page string) string {
		start := strings.Index(page, `id="`+anchor+`"`)
		end := strings.Index(page[start:], `data-forge-host="https://gitlab.com"`)
		if end < 0 {
			end = len(page) - start
		}
		return page[start : start+end]
	}
	tested := "Connected to GitLab 17.5.1 as root (Administrator)."
	list := ss.get(settingsForgesPath)
	if got := row(list); !strings.Contains(got, "data-forge-test-result") || !strings.Contains(got, "The token works.") || !strings.Contains(got, "data-forge-tested>"+tested+"<") {
		t.Fatalf("the row after the test: %s", got)
	}
	if strings.Contains(list, token) {
		t.Fatal("the list shows the token")
	}
	if got := row(ss.get(settingsForgesPath)); strings.Contains(got, "The token works.") || !strings.Contains(got, tested) {
		t.Fatalf("the row on the next visit: %s", got)
	}
	if err := ss.s.forge.ChangeToken(h.ID, "glpat-wrong-wrong-wrong", false); err != nil {
		t.Fatal(err)
	}
	if got := row(ss.get(settingsForgesPath)); strings.Contains(got, tested) {
		t.Fatalf("the row shows what an old token found: %s", got)
	}
	rec = ss.post(forgeHostPath(h.ID), url.Values{"form": {"test"}})
	if got := row(rec.Body.String()); rec.Code != http.StatusConflict || !strings.Contains(got, "GitLab refused the token: it is wrong, expired or revoked.") || !strings.Contains(got, "alert-danger") || strings.Contains(got, tested) {
		t.Fatalf("a refused test answered %d: %s", rec.Code, got)
	}
	if strings.Contains(ss.get(settingsForgesPath), "GitLab refused the token") {
		t.Fatal("the refusal rode the session")
	}
	rec = ss.post(forgeHostPath("gitlab.com"), url.Values{"form": {"test"}})
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "The host has no token to test.") {
		t.Fatalf("a test without a token answered %d", rec.Code)
	}
	rec = ss.post(forgeHostPath("nope"), url.Values{"form": {"test"}})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), forge.ErrNotFound.Error()) {
		t.Fatalf("a test of no host answered %d", rec.Code)
	}
}

// A test of the longest web address the server takes, in four byte runes,
// lands its sentence on the row.
func TestATestOfTheLongestAddressLandsOnTheRow(t *testing.T) {
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"404 Not Found"}`))
	}))
	defer fake.Close()
	for _, kind := range []forge.Kind{gitlab.Kind, github.Kind} {
		ss := newForgeSession(t)
		prefix := fake.URL + "/" + kind.ID + "/"
		webURL := prefix + strings.Repeat("😀", forge.MaxWebURLLength-utf8.RuneCountInString(prefix))
		h := addForgeHost(t, ss.s.forge, forge.Input{Kind: kind.ID, WebURL: webURL}, "glpat-Xq3vR8sT2uW5yZ7aB9cD")
		rec := ss.post(forgeHostPath(h.ID), url.Values{"form": {"test"}})
		if list := rec.Body.String(); rec.Code != http.StatusConflict || !strings.Contains(list, "No "+kind.Label+" API answers at "+prefix+"😀") || !strings.Contains(list, "…: check the kind and the web address.") {
			t.Fatalf("the %s test answered %d: %s", kind.Label, rec.Code, list)
		}
	}
}

// The local socket changes no host, no token and tests none.
func TestTheLocalSocketChangesNoHost(t *testing.T) {
	ss := newForgeSession(t)
	h, err := ss.s.forge.Add(forge.Input{Kind: "gitlab", WebURL: "https://git.example.com"})
	if err != nil {
		t.Fatal(err)
	}
	send, csrf := ownerSession(t, ss.s)
	for _, c := range []struct{ path, form string }{{settingsForgesNewPath, ""}, {forgeHostPath(h.ID), "save"}, {forgeHostPath(h.ID), "delete"}, {forgeHostPath(h.ID), "test"}, {forgeHostPath("github.com"), "save"}, {forgeTokenPath(h.ID), ""}} {
		form := hostForm("github", "https://github.example.com", "")
		form.Set("form", c.form)
		form.Set("token", "ghp_from_the_socket")
		form.Set(csrfFieldName, csrf)
		req := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		if rec := send(req); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), forgeLocalRefusal) {
			t.Fatalf("%s %q over the socket answered %d", c.path, c.form, rec.Code)
		}
	}
	if hosts, _ := ss.s.forge.Hosts(); len(hosts) != 3 || hosts[2].WebURL != "https://git.example.com" {
		t.Fatalf("the socket changed the hosts: %+v", hosts)
	}
	if hints, _ := ss.s.forge.TokenHints(); len(hints) != 0 {
		t.Fatalf("the socket stored a token: %v", hints)
	}
}

func TestTheAliasHostsFieldSplitsOnSpacesAndCommas(t *testing.T) {
	if got := strings.Join(forgeAliases(" gl,ssh.git.example.com\tgitlab-work ,, "), "|"); got != "gl|ssh.git.example.com|gitlab-work" {
		t.Fatalf("split %q", got)
	}
}
