package web

import (
	"context"
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/share"
)

// sharingSession is the owner signed in to a server with the router's own
// routes, the projects shop, blog and other and no link yet. post sends a
// sharing form to a path with the session's CSRF token, get reads a page of
// the owner's.
type sharingSession struct {
	s    *Server
	post func(string, url.Values) *httptest.ResponseRecorder
	get  func(string) string
}

func newSharingSession(t *testing.T) *sharingSession {
	t.Helper()
	_, s := modelSettingsServer(t)
	s.shares = share.NewStore(t.TempDir())
	s.bus = eventbus.New()
	root, err := s.projects.EnsureRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"shop", "blog", "other"} {
		if err := os.MkdirAll(filepath.Join(root, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	send, token := ownerSession(t, s)
	return &sharingSession{
		s: s,
		post: func(path string, form url.Values) *httptest.ResponseRecorder {
			form.Set(csrfFieldName, token)
			req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			return send(req)
		},
		get: func(path string) string {
			path, _, _ = strings.Cut(path, "#")
			rec := send(httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusOK {
				t.Fatalf("GET %s: %d", path, rec.Code)
			}
			return rec.Body.String()
		},
	}
}

func newLinkForm(name string, projects ...string) url.Values {
	return url.Values{"name": {name}, "project": projects, "amount": {"1"}, "unit": {"hours"}}
}

var shownLink = regexp.MustCompile(`data-share-created[\s\S]*?value="https?://[^"]*/share/([A-Za-z0-9_-]+)"`)

// shownToken reads the token of the link a page shows once, "" without one.
func shownToken(page string) string {
	m := shownLink.FindStringSubmatch(page)
	if m == nil {
		return ""
	}
	return m[1]
}

// refusal reads the message a dialog's post was refused with.
func refusal(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var answer struct{ Error string }
	if rec.Code != http.StatusBadRequest || json.Unmarshal(rec.Body.Bytes(), &answer) != nil {
		t.Fatalf("the refusal answered %d %s", rec.Code, rec.Body.String())
	}
	return answer.Error
}

// A link needs a name when it is made, also from a request no form built. A
// refusal in the dialog keeps it standing with the message, on the page it
// leads back to the form with the projects picked again, and stores nothing.
func TestALinkNeedsAName(t *testing.T) {
	ss := newSharingSession(t)
	for _, name := range []string{"", " \t "} {
		rec := ss.post(settingsSharingNewPath, newLinkForm(name, "shop", "blog"))
		want := settingsSharingNewPath + "?project=shop&project=blog"
		if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != want {
			t.Fatalf("the name %q answered %d %q", name, rec.Code, rec.Header().Get("Location"))
		}
		page := ss.get(rec.Header().Get("Location"))
		if !strings.Contains(page, "A link needs a name.") || shownToken(page) != "" {
			t.Fatalf("the name %q: the refusal reads wrong", name)
		}
		for _, project := range []string{"shop", "blog"} {
			if !strings.Contains(page, `name="project" value="`+project+`" hidden checked`) {
				t.Fatalf("the name %q: %s is no longer picked", name, project)
			}
		}
		if strings.Contains(page, `value="other" hidden checked`) {
			t.Fatalf("the name %q: other got picked", name)
		}
		if got := refusal(t, ss.post(settingsSharingNewPath+"?modal=1", newLinkForm(name, "shop"))); got != "A link needs a name." {
			t.Fatalf("the dialog's refusal reads %q", got)
		}
	}
	if got := ss.s.shares.List(); len(got) != 0 {
		t.Fatalf("a link without a name was made: %+v", got)
	}
}

// A link keeps the name it was made with: its Edit has no field for it, a
// save that names one changes the rest and leaves the name, and the old
// change of a link answers nothing.
func TestALinkCannotBeRenamed(t *testing.T) {
	ss := newSharingSession(t)
	sh, token, err := ss.s.shares.Create("Anna", linkTo("shop"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	edit := ss.get(shareEditPath(sh.ID) + "?modal=1")
	if strings.Contains(edit, `name="name"`) || !strings.Contains(edit, `data-form-title="Anna"`) {
		t.Fatal("the Edit offers the name")
	}
	rec := ss.post(shareEditPath(sh.ID), url.Values{"form": {"save"}, "name": {"Bert"}, "project": {"blog"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(ss.get(rec.Header().Get("Location")), "Link saved.") {
		t.Fatalf("the save answered %d", rec.Code)
	}
	for _, path := range []string{settingsSharingPath, shareEditPath(sh.ID) + "/name"} {
		if rec := ss.post(path, url.Values{"form": {"update"}, "id": {sh.ID}, "name": {"Carla"}}); rec.Code != http.StatusNotFound {
			t.Fatalf("a rename at %s answered %d", path, rec.Code)
		}
	}
	got, err := ss.s.shares.Lookup(token)
	if err != nil || got.Name != "Anna" || !slices.Equal(got.Targets(share.KindProject), []string{"shop", "blog"}) {
		t.Fatalf("the link = %+v, %v", got, err)
	}
}

// One link opens every project picked when it is made, in the order picked.
// Share from a project's row menu opens the list with New link on it, which
// picks the project. A pick of nothing, of a project that does not exist, of
// one twice and of more projects than there are is refused and makes nothing.
func TestALinkOpensTheProjectsPickedAtOnce(t *testing.T) {
	ss := newSharingSession(t)
	if list := ss.get(settingsSharingPath + "?project=blog&project=nope"); !strings.Contains(list, `data-open="/settings/sharing/new?project=blog&amp;project=nope"`) {
		t.Fatal("Share does not open New link")
	}
	if strings.Contains(ss.get(settingsSharingPath), "data-open") {
		t.Fatal("the bare list opens New link")
	}
	picked := regexp.MustCompile(`name="project" value="([^"]+)" hidden checked`)
	if got := picked.FindAllStringSubmatch(ss.get(settingsSharingNewPath+"?project=blog&project=nope&modal=1"), -1); len(got) != 1 || got[0][1] != "blog" {
		t.Fatal("the row menu's project is not the one picked")
	}
	for _, c := range []struct {
		name     string
		projects []string
		says     string
	}{
		{"nothing", nil, "Pick at least one project or assistant for the link."},
		{"a project that does not exist", []string{"shop", "nope"}, errShareGone.Error()},
		{"one twice", []string{"shop", "shop"}, "That project is already on the link."},
		{"more than there are", []string{"shop", "blog", "other", "shop"}, errShareMany.Error()},
	} {
		if got := refusal(t, ss.post(settingsSharingNewPath+"?modal=1", newLinkForm("Anna", c.projects...))); got != c.says {
			t.Fatalf("%s answered %q", c.name, got)
		}
	}
	if got := ss.s.shares.List(); len(got) != 0 {
		t.Fatalf("a refused pick made a link: %+v", got)
	}
	rec := ss.post(settingsSharingNewPath, newLinkForm("Anna", "blog", "shop"))
	token := shownToken(rec.Body.String())
	if rec.Code != http.StatusOK || token == "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("making the link answered %d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	sh, err := ss.s.shares.Lookup(token)
	if err != nil || sh.Name != "Anna" || !slices.Equal(sh.Targets(share.KindProject), []string{"blog", "shop"}) {
		t.Fatalf("the link = %+v, %v", sh, err)
	}
	dialog := ss.post(settingsSharingNewPath+"?modal=1", newLinkForm("Bert", "other"))
	if body := dialog.Body.String(); dialog.Code != http.StatusOK || shownToken(body) == "" || !strings.Contains(body, `data-form-view data-form-title="Bert"`) || strings.Contains(body, "<html") {
		t.Fatalf("the dialog's link answered %d", dialog.Code)
	}
}

// A link's Edit offers in its Add only what the link lacks, and its save
// changes the link whole: projects on and off, what each grants and the
// expiry, passing over what moved meanwhile. A save that would leave nothing
// is refused, and so is one naming more than the link opens.
func TestALinkChangesInOneSave(t *testing.T) {
	ss := newSharingSession(t)
	sh, token, err := ss.s.shares.Create("Anna", linkTo("shop"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	add := regexp.MustCompile(`name="project" value="([^"]+)" hidden( checked)?`).FindAllStringSubmatch(ss.get(shareEditPath(sh.ID)), -1)
	if len(add) != 2 || add[0][1] != "blog" || add[1][1] != "other" || add[0][2] != "" {
		t.Fatalf("the Edit's Add offers %q", add)
	}
	save := func(form url.Values) *httptest.ResponseRecorder {
		form.Set("form", "save")
		return ss.post(shareEditPath(sh.ID)+"?modal=1", form)
	}
	for _, c := range []struct {
		name string
		form url.Values
		says string
	}{
		{"the last taken off", url.Values{"resource": {"project:shop"}, "drop": {"project:shop"}}, "A link needs at least one project or assistant. Revoke it to end it."},
		{"one twice", url.Values{"project": {"blog", "blog"}}, "That project is already on the link."},
		{"more than it opens", url.Values{"resource": {"project:shop", "project:blog"}}, errShareShown.Error()},
		{"no kind", url.Values{"drop": {"shop"}}, errShareRemove.Error()},
		{"a long life", url.Values{"amount": {"31"}, "unit": {"days"}}, "The expiry is between 1 minute and 30 days."},
	} {
		if got := refusal(t, save(c.form)); got != c.says {
			t.Fatalf("%s answered %q", c.name, got)
		}
	}
	rec := save(url.Values{"resource": {"project:shop"}, "grant:comments": {"shop"}, "project": {"other", "blog"}, "amount": {"2"}, "unit": {"days"}})
	var answer struct{ Message string }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &answer) != nil || answer.Message != "Link saved." {
		t.Fatalf("the save answered %d %s", rec.Code, rec.Body.String())
	}
	got, err := ss.s.shares.Lookup(token)
	if err != nil || !slices.Equal(got.Targets(share.KindProject), []string{"shop", "other", "blog"}) || got.ExpiresAt.Sub(time.Now()) < 47*time.Hour {
		t.Fatalf("the link = %+v, %v", got, err)
	}
	if r, _ := got.Resource(share.KindProject, "shop"); !slices.Equal(r.Permissions, []share.Permission{share.Comments}) {
		t.Fatalf("shop grants %v", r.Permissions)
	}
	if strings.Contains(ss.get(shareEditPath(sh.ID)), `data-picker-source`) {
		t.Fatal("a link on every project still offers an Add")
	}
	expires := got.ExpiresAt
	if rec := save(url.Values{"resource": {"project:shop", "project:other"}, "drop": {"project:other", "project:gone"}}); rec.Code != http.StatusOK {
		t.Fatalf("taking other off answered %d %s", rec.Code, rec.Body.String())
	}
	got, _ = ss.s.shares.Lookup(token)
	if r, _ := got.Resource(share.KindProject, "shop"); len(r.Permissions) != 0 || !slices.Equal(got.Targets(share.KindProject), []string{"shop", "blog"}) || !got.ExpiresAt.Equal(expires) {
		t.Fatalf("after the second save %+v", got)
	}
}

// Revoke ends a link from its Edit, a link that no longer stands leads back
// to the list, and the local socket changes no link.
func TestALinkEndsFromItsEdit(t *testing.T) {
	ss := newSharingSession(t)
	sh, token, err := ss.s.shares.Create("Anna", linkTo("shop"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	send, csrf := ownerSession(t, ss.s)
	for _, c := range []struct{ path, form string }{{settingsSharingNewPath, "create"}, {shareEditPath(sh.ID), "save"}, {shareEditPath(sh.ID), "revoke"}} {
		form := url.Values{"form": {c.form}, "name": {"x"}, "project": {"blog"}, "amount": {"1"}, "unit": {"hours"}, csrfFieldName: {csrf}}
		req := httptest.NewRequest(http.MethodPost, c.path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		if rec := send(req); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), shareLocalRefusal) {
			t.Fatalf("%s over the socket answered %d", c.form, rec.Code)
		}
	}
	if got := ss.s.shares.List(); len(got) != 1 || len(got[0].Resources) != 1 {
		t.Fatalf("the socket changed the links: %+v", got)
	}
	rec := ss.post(shareEditPath(sh.ID), url.Values{"form": {"revoke"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(ss.get(rec.Header().Get("Location")), "Link revoked.") {
		t.Fatalf("the revoke answered %d", rec.Code)
	}
	if _, err := ss.s.shares.Lookup(token); err == nil {
		t.Fatal("the revoked link still opens")
	}
	if got := refusal(t, ss.post(shareEditPath(sh.ID)+"?modal=1", url.Values{"form": {"save"}})); got != errShareGoneLink.Error() {
		t.Fatalf("a save of the revoked link answered %q", got)
	}
	if rec := send(httptest.NewRequest(http.MethodGet, shareEditPath(sh.ID), nil)); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != settingsSharingPath {
		t.Fatalf("the revoked link's Edit answered %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

// Comment is off unless switched on and goes on only the projects of a new
// link whose entry switched it on, read the way a save of the Edit reads it.
// The list marks each project granting it and every link ending within a
// day. More switches than projects are refused, and a permission the cockpit
// does not keep yet grants nothing.
func TestALinkGrantsCommentPerProjectAndTheListShowsIt(t *testing.T) {
	ss := newSharingSession(t)
	grants := func(token string) map[string][]share.Permission {
		t.Helper()
		sh, err := ss.s.shares.Lookup(token)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][]share.Permission{}
		for _, r := range sh.Resources {
			out[r.Target] = r.Permissions
		}
		return out
	}
	plain := shownToken(ss.post(settingsSharingNewPath, newLinkForm("Anna", "shop")).Body.String())
	if got := grants(plain); len(got["shop"]) != 0 {
		t.Fatalf("a link made without a switch grants %v", got)
	}
	form := newLinkForm("Bert", "shop", "blog")
	form.Set("amount", "3")
	form.Set("unit", "days")
	form["grant:comments"] = []string{"shop", "nope"}
	rec := ss.post(settingsSharingNewPath, form)
	token := shownToken(rec.Body.String())
	if got := grants(token); !slices.Equal(got["shop"], []share.Permission{share.Comments}) || got["blog"] == nil || len(got["blog"]) != 0 {
		t.Fatalf("the switched Comment grants %v", got)
	}
	if body := rec.Body.String(); !strings.Contains(body, `shop</span> <span class="badge bg-secondary-lt" data-share-created-grants>Read, comment</span>, <span class="fw-medium text-break">blog</span> until`) || !strings.Contains(body, "The rest is read only.") {
		t.Fatalf("the new link does not say what each project grants: %s", body)
	}
	links := len(ss.s.shares.List())
	for name, form := range map[string]url.Values{
		"more switches than projects": {"grant:comments": {"shop", "shop"}},
		"a switch of every project":   {"grant:comments": {"shop", "blog"}},
	} {
		picked := newLinkForm("Carla", "shop")
		maps.Copy(picked, form)
		if got := refusal(t, ss.post(settingsSharingNewPath+"?modal=1", picked)); got != errShareGrant.Error() {
			t.Fatalf("%s answered %q", name, got)
		}
	}
	if got := len(ss.s.shares.List()); got != links {
		t.Fatalf("a refused grant made a link: %d links, want %d", got, links)
	}
	unkept := newLinkForm("Dora", "shop")
	unkept["grant:files"] = []string{"shop"}
	if got := grants(shownToken(ss.post(settingsSharingNewPath, unkept).Body.String())); len(got["shop"]) != 0 {
		t.Fatalf("a permission the cockpit keeps nowhere granted %v", got)
	}
	rows := regexp.MustCompile(`(?s)data-share-row="[^"]+">.*?data-share-edit`).FindAllString(ss.get(settingsSharingPath), -1)
	if len(rows) != 3 {
		t.Fatalf("the list holds %d rows", len(rows))
	}
	anna, bert := rows[0], rows[1]
	if mark := strings.Index(bert, `data-share-grant="comments"`); strings.Contains(anna, `data-share-grant`) || strings.Count(bert, `data-share-grant="comments"`) != 1 || mark < strings.Index(bert, `data-share-project="shop"`) || mark > strings.Index(bert, `data-share-project="blog"`) {
		t.Fatal("the Comment marks stand wrong")
	}
	if !strings.Contains(anna, "data-share-soon") || strings.Contains(bert, "data-share-soon") {
		t.Fatal("the links ending soon are marked wrong")
	}
}

// New link lists what the address picks, each with its kind, its name and
// its switches as the address sets them, and both dialogs carry for every
// choice the entry a pick adds, nothing switched on. A refusal on the page
// leads back to the form with the picks and the switches again.
func TestTheDialogsListEveryPickWithItsSwitches(t *testing.T) {
	ss := newSharingSession(t)
	page := ss.get(settingsSharingNewPath + "?modal=1&project=blog&project=shop&grant%3Acomments=shop")
	entries := regexp.MustCompile(`(?s)<div class="list-group mt-2" data-share-entries>(.*?)</div>\s*<template`).FindStringSubmatch(page)
	if entries == nil {
		t.Fatalf("New link lists no picks: %s", page)
	}
	listed := regexp.MustCompile(`data-share-project="([^"]+)" data-share-picked="project:[^"]+"`).FindAllStringSubmatch(entries[1], -1)
	if len(listed) != 2 || !strings.Contains(entries[1], `name="grant:comments" value="shop" checked`) || strings.Contains(entries[1], `value="blog" checked`) || strings.Count(entries[1], ">Project<") != 2 {
		t.Fatalf("New link lists %q: %s", listed, entries[1])
	}
	if strings.Count(page, `<template data-share-entry="project:`) != 3 || strings.Contains(page, `name="permission"`) || strings.Contains(page, ` checked data-share-grant`) && strings.Count(page, ` checked data-share-grant`) != 1 {
		t.Fatal("New link does not offer an entry for every project, or keeps the shared Permissions")
	}
	if empty := ss.get(settingsSharingNewPath + "?modal=1"); !strings.Contains(empty, `data-share-entries hidden`) {
		t.Fatal("New link without a pick shows its list")
	}
	sh, _, err := ss.s.shares.Create("Anna", linkTo("shop"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	edit := ss.get(shareEditPath(sh.ID) + "?modal=1")
	if strings.Count(edit, `<template data-share-entry="project:`) != 2 || !strings.Contains(edit, `<div class="list-group-item py-2 px-3" data-share-project="blog" data-share-picked="project:blog">`) || !strings.Contains(edit, `data-share-unpick="project:blog"`) {
		t.Fatalf("the Edit does not offer an entry for every pick: %s", edit)
	}
	form := newLinkForm("", "shop", "blog")
	form["grant:comments"] = []string{"blog"}
	back := ss.post(settingsSharingNewPath, form)
	if back.Code != http.StatusSeeOther {
		t.Fatalf("a refused page answered %d", back.Code)
	}
	again := ss.get(back.Header().Get("Location"))
	if !strings.Contains(again, `name="grant:comments" value="blog" checked`) || strings.Contains(again, `value="shop" checked`) || !regexp.MustCompile(`name="project" value="shop" hidden checked`).MatchString(again) {
		t.Fatalf("the refused page lost its picks: %s", back.Header().Get("Location"))
	}
}
