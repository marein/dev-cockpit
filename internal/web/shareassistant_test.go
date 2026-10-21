package web

import (
	"context"
	"html/template"
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

	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/assistant"
	"github.com/marein/dev-cockpit/internal/docker"
	"github.com/marein/dev-cockpit/internal/share"
	"github.com/marein/dev-cockpit/internal/web/render"
)

// sharedAssistant is the owner signed in with two assistants, the first one's
// thread linking a file of its workspace and naming files of shop and blog,
// and a guest who reaches the share routes alone, with no session.
type sharedAssistant struct {
	*sharingSession
	guest     func(string) *httptest.ResponseRecorder
	id, other string
	missing   string
}

func newSharedAssistant(t *testing.T) *sharedAssistant {
	t.Helper()
	ss := newSharingSession(t)
	s := ss.s
	stateDir := t.TempDir()
	assistants, workspace, err := assistant.New(stateDir, sessionTurnCoder{dir: t.TempDir()}, assistant.Cockpit{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.StateDir, s.assistants, s.workspace = stateDir, assistants, workspace
	s.watcher = assistant.NewWatcher(assistants, assistant.NewJobs(assistant.NewStore(stateDir)), nil, nil)
	s.docker = docker.NewService(s.cfg.StateDir, func() string { return "" })
	s.approvals = s.newApprovals(s.cfg.StateDir)
	root, err := s.projects.EnsureRoot()
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"shop/README.md", "blog/notes.md"} {
		if err := os.WriteFile(filepath.Join(root, file), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	inst, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.assistants.Create("claude")
	if err != nil {
		t.Fatal(err)
	}
	files := filepath.Join(s.workspace.Dir(inst.ID), assistant.FilesDirName)
	for name, body := range map[string]string{"proof.txt": "proof", "secret.txt": "secret"} {
		if err := os.MkdirAll(files, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(files, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	inst.Title = "Shop helper"
	inst.Messages = append(inst.Messages,
		assistant.Message{ID: "m1", Role: assistant.RoleUser, Content: "Show me", State: assistant.StateComplete, CreatedAt: time.Now()},
		assistant.Message{ID: "m2", Role: assistant.RoleAssistant, State: assistant.StateComplete, CreatedAt: time.Now(), Content: "Here: [the proof](assistant-files/proof.txt), see " +
			filepath.Join(root, "shop", "README.md") + " and " + filepath.Join(root, "blog", "notes.md") +
			", the [run](/projects/shop/docker/runs/1), the [settings](/settings), [the editor](/projects/shop/editor?file=README.md) and [elsewhere](https://example.com)."},
	)
	assistant.NewStore(s.cfg.StateDir).Save(inst)
	r := gin.New()
	r.SetHTMLTemplate(render.HTMLTemplate(s.assets.assetPath, "test", "test", s.plugins, nil))
	r.NoRoute(s.handleNotFound)
	s.registerShareRoutes(r)
	guest := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}
	return &sharedAssistant{sharingSession: ss, guest: guest, id: inst.ID, other: other.ID, missing: guest("/no/such/address").Body.String()}
}

// link makes a link in Settings › Sharing and answers its address.
func (sa *sharedAssistant) link(t *testing.T, projects []string, assistants ...string) string {
	t.Helper()
	rec := sa.post(settingsSharingNewPath, url.Values{"name": {"Anna"}, "project": projects, "assistant": assistants, "amount": {"1"}, "unit": {"hours"}})
	token := shownToken(rec.Body.String())
	if rec.Code != http.StatusOK || token == "" {
		t.Fatalf("making the link answered %d", rec.Code)
	}
	return sharePathPrefix + token
}

func (sa *sharedAssistant) notFound(t *testing.T, what, target string) {
	t.Helper()
	if rec := sa.guest(target); rec.Code != http.StatusNotFound || rec.Body.String() != sa.missing {
		t.Fatalf("%s = %d, want the plain 404", what, rec.Code)
	}
}

// A guest reads the assistant of its link whole and read only, in the tab of
// the link's projects: its files under the link, a project's path into the
// share where the project is on it and text where it is not, and every other
// address of the cockpit as its words alone. Another assistant is an address
// that does not exist.
func TestAGuestReadsTheAssistantOfItsLinkBesideItsProjects(t *testing.T) {
	sa := newSharedAssistant(t)
	base := sa.link(t, []string{"shop"}, sa.id)
	rec := sa.guest(base + "/assistants/" + sa.id)
	page := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("the conversation answered %d", rec.Code)
	}
	if os.Getenv("DUMP") != "" {
		t.Log(page)
	}
	for _, want := range []string{
		"Shop helper", "Show me", "Read only", `<dc-share-chat`, `stream-url="` + base + `/assistants/` + sa.id + `/stream"`,
		`id="assistant-aside"`, `list-url="` + base + `/assistants/jobs?assistant=` + sa.id + `"`, `list-url="` + base + `/assistants/triggers?assistant=` + sa.id + `"`,
		`href="` + base + `/assistants/` + sa.id + `/media/assistant-files/proof.txt`,
		`href="` + base + `/projects/shop/editor?file=README.md`,
		`href="` + base + `/assistants"`, `href="` + base + `/terminals"`, `href="` + base + `"`,
		"blog/notes.md", ", the run, the settings, ", `href="https://example.com"`,
	} {
		if !strings.Contains(page, want) {
			t.Fatalf("the conversation lacks %s", want)
		}
	}
	for _, never := range []string{
		`/projects/blog/editor`, `href="/settings"`, `/docker/runs/`, `href="/projects/shop/editor`,
		"data-assistant-input", "data-assistant-speak", "data-assistant-menu", "assistant-memory", "data-assistant-trigger-new",
		"/assistants/" + sa.other, "/draft", "/user-upload", "/stt",
	} {
		if strings.Contains(page, never) {
			t.Fatalf("the conversation carries %s", never)
		}
	}
	if entry := sa.guest(base + "/assistants"); entry.Code != http.StatusSeeOther || entry.Header().Get("Location") != base+"/assistants/"+sa.id {
		t.Fatalf("the Assistants entry = %d %q", entry.Code, entry.Header().Get("Location"))
	}
	if entry := sa.guest(base); entry.Header().Get("Location") != base+"/projects/shop/editor" {
		t.Fatalf("the link's entry leads to %q", entry.Header().Get("Location"))
	}
	for name, target := range map[string]string{
		"another assistant":           base + "/assistants/" + sa.other,
		"another assistant's stream":  base + "/assistants/" + sa.other + "/stream",
		"another assistant's media":   base + "/assistants/" + sa.other + "/media/assistant-files/proof.txt",
		"an assistant that is no id":  base + "/assistants/nope",
		"the assistant's memory":      base + "/assistants/memory",
		"the assistant's jobs":        base + "/assistants/jobs",
		"the assistant's draft":       base + "/assistants/" + sa.id + "/draft",
		"one message":                 base + "/assistants/" + sa.id + "/messages/m2",
		"one message read aloud":      base + "/assistants/" + sa.id + "/messages/m2/audio",
		"the owner's assistants list": base + "/ctx/assistant",
	} {
		sa.notFound(t, name, target)
	}
}

// A block the owner answered halfway stands read only for a guest, with
// nothing of the owner's draft in it, and the draft is no address of the link.
func TestAGuestSeesNothingOfTheOwnersAnswers(t *testing.T) {
	sa := newSharedAssistant(t)
	store := assistant.NewStore(sa.s.cfg.StateDir)
	inst, _ := store.Load(sa.id)
	inst.Messages = append(inst.Messages, assistant.Message{ID: "m3", Role: assistant.RoleAssistant, State: assistant.StateComplete, CreatedAt: time.Now(), Content: "```ask\nWhich one?\n- This\n- That\n```\n"})
	store.Save(inst)
	if _, err := sa.s.assistants.SaveAnswers(sa.id, "m3", []assistant.Pick{{Option: 1, Text: "halfway there"}}); err != nil {
		t.Fatal(err)
	}
	base := sa.link(t, []string{"shop"}, sa.id)
	page := sa.guest(base + "/assistants/" + sa.id).Body.String()
	if !strings.Contains(page, `data-choices-state="readonly"`) || strings.Contains(page, "halfway there") || strings.Contains(page, `id="choices-m3-0-1" autocomplete="off" checked`) || strings.Contains(page, "draft-url") {
		t.Fatalf("the guest sees the owner's answers: %s", page)
	}
	sa.notFound(t, "the draft", base+"/assistants/"+sa.id+"/draft")
}

// A guest's list of the link's assistants and the switcher's rows of them are
// the owner's rows of those assistants alone, each leading inside the share,
// and the list pulls itself again under the link's address, naming its page
// by the path below it, so no query carries the token.
func TestAGuestsAssistantRowsAreTheOwnersOfItsLink(t *testing.T) {
	sa := newSharedAssistant(t)
	base := sa.link(t, []string{"shop"}, sa.id)
	path := url.QueryEscape("/assistants/" + sa.id)
	column := sa.guest(base + "/ctx/assistants?path=" + path)
	strip := sa.guest(base + "/terminal-tabs?path=" + path)
	if column.Code != http.StatusOK || strip.Code != http.StatusOK {
		t.Fatalf("the column answered %d, the strip %d", column.Code, strip.Code)
	}
	for _, want := range []string{
		`<dc-assistant-list`, `list-url="` + base + `/ctx/assistants?path=` + path + `"`,
		`active" href="` + base + `/assistants/` + sa.id + `"`, "Shop helper",
	} {
		if !strings.Contains(column.Body.String(), want) {
			t.Fatalf("the guest's column lacks %s: %s", want, column.Body.String())
		}
	}
	if want := `data-tabs-assistant-instance="` + sa.id + `" data-assistant-name="Shop helper" href="` + base + `/assistants/` + sa.id + `"`; !strings.Contains(strip.Body.String(), want) {
		t.Fatalf("the guest's switcher lacks %s: %s", want, strip.Body.String())
	}
	for _, body := range []string{column.Body.String(), strip.Body.String()} {
		for _, never := range []string{sa.other, "data-assistant-menu", ` history`, "data-tabs-assistant ", `href="/assistants`} {
			if strings.Contains(body, never) {
				t.Fatalf("a guest's rows carry %s: %s", never, body)
			}
		}
	}
}

// Of the workspace a guest reaches exactly the files the shown messages link,
// served sandboxed, and nothing beside them.
func TestAGuestReadsOnlyTheFilesTheMessagesLink(t *testing.T) {
	sa := newSharedAssistant(t)
	base := sa.link(t, nil, sa.id) + "/assistants/" + sa.id + "/media/"
	rec := sa.guest(base + "assistant-files/proof.txt")
	if rec.Code != http.StatusOK || rec.Body.String() != "proof" || rec.Header().Get("Content-Security-Policy") != "sandbox" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("the linked file = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	for _, rel := range []string{"assistant-files/secret.txt", "CLAUDE.md", "../transcript.json", "assistant-files/../../transcript.json", "user-upload/x.png"} {
		sa.notFound(t, rel, base+rel)
	}
	inst, err := sa.s.assistants.Get(sa.id)
	if err != nil {
		t.Fatal(err)
	}
	inst.Messages = append(inst.Messages, assistant.Message{ID: "m3", Role: assistant.RoleAssistant, State: assistant.StateComplete, CreatedAt: time.Now(), Content: "And [this one](assistant-files/secret.txt)."})
	assistant.NewStore(sa.s.cfg.StateDir).Save(inst)
	if rec := sa.guest(base + "assistant-files/secret.txt"); rec.Code != http.StatusOK || rec.Body.String() != "secret" {
		t.Fatalf("a file a new message links = %d", rec.Code)
	}
}

// A link of an assistant alone opens on its conversation, its rail carries
// no editor, and Settings › Sharing lists the assistant as read only.
func TestALinkOfAnAssistantAloneOpensOnItsConversation(t *testing.T) {
	sa := newSharedAssistant(t)
	base := sa.link(t, nil, sa.id)
	if entry := sa.guest(base); entry.Code != http.StatusSeeOther || entry.Header().Get("Location") != base+"/assistants/"+sa.id {
		t.Fatalf("the entry = %d %q", entry.Code, entry.Header().Get("Location"))
	}
	if page := sa.guest(base + "/assistants/" + sa.id).Body.String(); strings.Contains(page, "Editor</span>") {
		t.Fatal("a link without a project offers the editor")
	}
	settings := sa.get(settingsSharingPath)
	for _, want := range []string{`data-share-assistant="` + sa.id + `"`, "Shop helper"} {
		if !strings.Contains(settings, want) {
			t.Fatalf("Settings › Sharing lacks %s", want)
		}
	}
	edit := sa.get(shareEditPath(sa.s.shares.List()[0].ID))
	for _, want := range []string{`name="resource" value="assistant:` + sa.id + `"`, "Assistant, read only", `name="assistant" value="` + sa.other + `" hidden`} {
		if !strings.Contains(edit, want) {
			t.Fatalf("the link's Edit lacks %s", want)
		}
	}
}

// An assistant picked to share has to exist, a project and an assistant go
// on one link together and come off it in one save, and the last of them
// stays until the link is revoked.
func TestAssistantsGoOnAndOffALinkInSettings(t *testing.T) {
	sa := newSharedAssistant(t)
	for name, form := range map[string]url.Values{
		"no such assistant": {"name": {"Anna"}, "assistant": {"11111111-1111-4111-8111-111111111111"}, "amount": {"1"}, "unit": {"hours"}},
		"no id":             {"name": {"Anna"}, "assistant": {"../x"}, "amount": {"1"}, "unit": {"hours"}},
		"one twice":         {"name": {"Anna"}, "assistant": {sa.id, sa.id}, "amount": {"1"}, "unit": {"hours"}},
	} {
		if rec := sa.post(settingsSharingNewPath, form); rec.Code != http.StatusSeeOther {
			t.Fatalf("%s answered %d", name, rec.Code)
		}
	}
	if got := sa.s.shares.List(); len(got) != 0 {
		t.Fatalf("a refused pick made a link: %+v", got)
	}
	base := sa.link(t, []string{"shop"})
	id := sa.s.shares.List()[0].ID
	if rec := sa.post(shareEditPath(id), url.Values{"form": {"save"}, "assistant": {sa.id, sa.other}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("adding the assistants answered %d", rec.Code)
	}
	if rec := sa.guest(base + "/assistants/" + sa.other); rec.Code != http.StatusOK {
		t.Fatalf("an assistant added to the link answered %d", rec.Code)
	}
	shown := []string{"project:shop", "assistant:" + sa.id, "assistant:" + sa.other}
	if rec := sa.post(shareEditPath(id), url.Values{"form": {"save"}, "resource": shown, "drop": {"project:shop", "assistant:" + sa.other}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("removing two answered %d", rec.Code)
	}
	sa.notFound(t, "an assistant off the link", base+"/assistants/"+sa.other)
	if got := sa.s.shares.List(); len(got) != 1 || !slices.Equal(got[0].Targets(share.KindAssistant), []string{sa.id}) || len(got[0].Targets(share.KindProject)) != 0 {
		t.Fatalf("the link = %+v", got)
	}
	sa.post(shareEditPath(id), url.Values{"form": {"save"}, "resource": {"assistant:" + sa.id}, "drop": {"assistant:" + sa.id}})
	if got := sa.s.shares.List(); len(got) != 1 || len(got[0].Resources) != 1 {
		t.Fatalf("a save took the last one off: %+v", got)
	}
	sa.post(shareEditPath(id), url.Values{"form": {"revoke"}})
	if got := sa.s.shares.List(); len(got) != 0 {
		t.Fatalf("a revoked link stands: %+v", got)
	}
}

// Deleting an assistant takes it off every link before it goes: a link of it
// alone ends, one with a project keeps the project.
func TestDeletingAnAssistantTakesItOffEveryLink(t *testing.T) {
	sa := newSharedAssistant(t)
	alone := sa.link(t, nil, sa.id)
	both := sa.link(t, []string{"shop"}, sa.id)
	if _, err := sa.s.deleteAssistant(sa.id); err != nil {
		t.Fatal(err)
	}
	sa.notFound(t, "the link of the deleted assistant alone", alone)
	sa.notFound(t, "the deleted assistant", both+"/assistants/"+sa.id)
	got := sa.s.shares.List()
	if len(got) != 1 || len(got[0].Targets(share.KindAssistant)) != 0 || !slices.Equal(got[0].Targets(share.KindProject), []string{"shop"}) {
		t.Fatalf("the links after the delete = %+v", got)
	}
}

// An assistant whose directory is gone from disk, however it went, leaves
// every link, and one whose transcript only cannot be read stays on it.
func TestAnAssistantGoneFromDiskTakesItsLinks(t *testing.T) {
	sa := newSharedAssistant(t)
	base := sa.link(t, nil, sa.id, sa.other)
	transcript := filepath.Join(assistant.NewStore(sa.s.cfg.StateDir).InstanceDir(sa.other), "transcript.json")
	if err := os.WriteFile(transcript, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	sa.notFound(t, "an assistant that cannot be read", base+"/assistants/"+sa.other)
	if got := sa.s.shares.List(); len(got) != 1 || len(got[0].Targets(share.KindAssistant)) != 2 {
		t.Fatalf("an unreadable assistant left the link: %+v", got)
	}
	if err := os.RemoveAll(assistant.NewStore(sa.s.cfg.StateDir).InstanceDir(sa.id)); err != nil {
		t.Fatal(err)
	}
	sa.notFound(t, "an assistant gone from disk", base+"/assistants/"+sa.id)
	if got := sa.s.shares.List(); len(got) != 1 || !slices.Equal(got[0].Targets(share.KindAssistant), []string{sa.other}) {
		t.Fatalf("a gone assistant stays on the link: %+v", got)
	}
}

// No assistant shares itself: the local socket can make no link and put
// nothing on one.
func TestTheLocalSocketCannotShareAnAssistant(t *testing.T) {
	sa := newSharedAssistant(t)
	send, token := ownerSession(t, sa.s)
	sh, _, err := sa.s.shares.Create("Anna", linkTo("shop"), time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	for path, form := range map[string]url.Values{
		settingsSharingNewPath: {"name": {"Anna"}, "assistant": {sa.id}, "amount": {"1"}, "unit": {"hours"}},
		shareEditPath(sh.ID):   {"form": {"save"}, "assistant": {sa.id}},
	} {
		form.Set(csrfFieldName, token)
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
		if rec := send(req); rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), shareLocalRefusal) {
			t.Fatalf("%s over the socket answered %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if got := sa.s.shares.List(); len(got) != 1 || len(got[0].Resources) != 1 {
		t.Fatalf("the socket changed the links: %+v", got)
	}
}

// Of the assistants' tree a guest route may mirror the reads alone, each a
// GET, so no grant added later opens the memory, the drafts, the uploads, the
// model picks, the speech or a write.
func TestAGuestMirrorsOnlyTheReadsOfAnAssistant(t *testing.T) {
	for _, path := range guestAssistantReads {
		if !guestMayRead(http.MethodGet, path) || guestMayRead(http.MethodPost, path) {
			t.Fatalf("%s: GET %v, POST %v", path, guestMayRead(http.MethodGet, path), guestMayRead(http.MethodPost, path))
		}
	}
	for _, path := range []string{
		"/assistants/memory", "/assistants/order", "/assistants/new",
		"/assistants/instances", "/assistants/instances/:id", "/assistants/models", "/assistants/:id/draft",
		"/assistants/:id/user-upload", "/assistants/:id/stt", "/assistants/:id/messages/:messageId",
		"/assistants/:id/messages/:messageId/audio", "/assistant/:id",
	} {
		if guestMayRead(http.MethodGet, path) {
			t.Fatalf("GET %s is open to a guest", path)
		}
	}
}

// Beside the conversation a guest reads that assistant's jobs and triggers
// and nothing of another's, on the page and on the lists it pulls, with no
// control that changes one: a job's editor leads into the share where its
// project is on the link, a coder that does not run leads nowhere, and the
// trigger forms the owner's path answers are never a guest's.
func TestAGuestReadsTheJobsAndTriggersOfItsAssistantAlone(t *testing.T) {
	sa := newSharedAssistant(t)
	jobs := assistant.NewJobs(assistant.NewStore(sa.s.cfg.StateDir))
	now := time.Now().UTC()
	jobs.Of(sa.id).Save(assistant.Job{Terminal: "own-shop", Name: "shop-coder", Project: "shop", CoderID: "claude", DoneWhen: "done", State: assistant.JobSteering, CreatedAt: now})
	jobs.Of(sa.id).Save(assistant.Job{Terminal: "own-blog", Name: "blog-coder", Project: "blog", CoderID: "claude", DoneWhen: "done", State: assistant.JobDone, CreatedAt: now.Add(-time.Minute)})
	jobs.Of(sa.other).Save(assistant.Job{Terminal: "theirs", Name: "their-coder", Project: "shop", CoderID: "claude", DoneWhen: "done", State: assistant.JobSteering, CreatedAt: now})
	own, err := sa.s.assistants.Events().Add(assistant.TriggerSpec{Owner: sa.id, Event: "compose-ended", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := sa.s.assistants.Events().Add(assistant.TriggerSpec{Owner: sa.other, Event: "compose-failed", Task: "say so"})
	if err != nil {
		t.Fatal(err)
	}
	base := sa.link(t, []string{"shop"}, sa.id)
	page := sa.guest(base + "/assistants/" + sa.id).Body.String()
	list := func(what string) string {
		rec := sa.guest(base + what)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s answered %d", what, rec.Code)
		}
		return rec.Body.String()
	}
	jobsList := list("/assistants/jobs?assistant=" + sa.id)
	triggersList := list("/assistants/triggers?assistant=" + sa.id)
	formAsked := list("/assistants/triggers?form=new&assistant=" + sa.id)
	for name, html := range map[string]string{"page": page, "jobs": jobsList, "triggers": triggersList, "form": formAsked} {
		never := []string{
			`data-assistant-job="theirs"`, `data-assistant-trigger="` + theirs.ID + `"`, "data-assistant-job-stop", "data-assistant-job-again",
			"data-assistant-trigger-remove", "data-assistant-trigger-edit", "data-assistant-trigger-new", "data-assistant-job-open",
			"/projects/blog/editor", `href="/coders/`, `href="/projects`,
		}
		if name != "page" {
			never = append(never, "<form")
			for _, href := range regexp.MustCompile(`href="(/[^"]*)"`).FindAllStringSubmatch(html, -1) {
				if !strings.HasPrefix(href[1], base+"/") {
					t.Fatalf("the guest's %s links %s outside the share", name, href[1])
				}
			}
		}
		for _, never := range never {
			if strings.Contains(html, never) {
				t.Fatalf("the guest's %s carries %s", name, never)
			}
		}
	}
	for name, html := range map[string]string{"page": page, "jobs": jobsList} {
		for _, want := range []string{`data-assistant-job="own-shop"`, `data-assistant-job="own-blog"`, `href="` + base + `/projects/shop/editor"`} {
			if !strings.Contains(html, want) {
				t.Fatalf("the guest's %s lacks %s", name, want)
			}
		}
	}
	for name, html := range map[string]string{"page": page, "triggers": triggersList, "form": formAsked} {
		if !strings.Contains(html, `data-assistant-trigger="`+own.ID+`"`) {
			t.Fatalf("the guest's %s lacks its assistant's trigger", name)
		}
	}
	for name, target := range map[string]string{
		"another assistant's jobs":     base + "/assistants/jobs?assistant=" + sa.other,
		"another assistant's triggers": base + "/assistants/triggers?assistant=" + sa.other,
		"everybody's triggers":         base + "/assistants/triggers",
		"everybody's jobs":             base + "/assistants/jobs?assistant=all",
	} {
		sa.notFound(t, name, target)
	}
}

// A guest's bubbles leave the cockpit only into the share: an address under
// the link stays, one the share opens is led there, every other address of
// the cockpit keeps its words without the link, and a link elsewhere stays,
// the backslash one a browser reads as another host among them.
func TestAGuestsLinksLeaveTheCockpitOnlyIntoTheShare(t *testing.T) {
	reach := func(href string) (string, bool) {
		if href == "/coders/c1" {
			return "/share/t/coders/c1", true
		}
		return "", false
	}
	got := string(guestAnchors(template.HTML(`<p><a href="/share/t/assistants/a/media/x.png">pic</a> <a href="/coders/c1">coder</a> <a href="/coders/c2">other</a> <a href="/settings"><code>settings</code></a> <a href="//elsewhere.example">far</a> <a href="https://example.com" title="t">web</a> <a href="/\evil">trick</a></p>`), "/share/t", reach))
	want := `<p><a href="/share/t/assistants/a/media/x.png">pic</a> <a href="/share/t/coders/c1">coder</a> other <code>settings</code> <a href="//elsewhere.example">far</a> <a href="https://example.com" title="t">web</a> <a href="/\evil">trick</a></p>`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := string(guestAnchors(`<a href="/share/t/x">a</a> <a href="/coders/c1">b</a>`, "", nil)); got != "a b" {
		t.Fatalf("a live frame keeps %s", got)
	}
}

// A guest's stream carries the answer being written, since when its turn
// runs, the name of a tool at work and the signals that the thread moved,
// never the picks, and the live answer holds no link into the cockpit.
func TestTheGuestFrameCarriesTheAnswerAndNothingElse(t *testing.T) {
	models := assistant.ModelPicks{Chat: "secret-model"}
	for _, ev := range []assistant.StreamEvent{
		{Kind: assistant.FrameModels, Models: &models},
		{Kind: "something-new"},
	} {
		if _, ok := guestFrame(assistant.Instance{}, ev); ok {
			t.Fatalf("%s reached the guest", ev.Kind)
		}
	}
	frame, ok := guestFrame(assistant.Instance{}, assistant.StreamEvent{Kind: assistant.FrameStart, MessageID: "m", RunID: "r", Model: "secret-model", HTML: `<a href="/settings">go</a>`, Text: "x", Since: 42})
	if !ok || frame.HTML != "go" || frame.Model != "" || frame.RunID != "" || frame.Text != "x" || frame.MessageID != "m" || frame.Since != 42 {
		t.Fatalf("the start frame reads %+v", frame)
	}
	tool, ok := guestFrame(assistant.Instance{}, assistant.StreamEvent{Kind: assistant.FrameTool, MessageID: "m", RunID: "r", Text: "Bash", Model: "secret-model"})
	if !ok || tool != (assistant.StreamEvent{Kind: assistant.FrameTool, MessageID: "m", Text: "Bash"}) {
		t.Fatalf("the tool frame reads %+v", tool)
	}
}

// A coder is named in a chat by its id or by the short id the cockpit prints,
// and a short id two coders share names neither.
func TestACoderIsLinkedByItsIdOrShortID(t *testing.T) {
	l := coderLinker{hrefs: map[string]string{
		"ece050d8-c7a1-4c35-b955-776923076819": "/share/t/coders/ece050d8-c7a1-4c35-b955-776923076819",
		"ece050d8":                             "/share/t/coders/ece050d8-c7a1-4c35-b955-776923076819",
	}}
	got := l.linkedText("`share-assistants` (ece050d8) and ece050d8-c7a1-4c35-b955-776923076819. Not ece050d81 <b>")
	want := "`share-assistants` (<a href=\"/share/t/coders/ece050d8-c7a1-4c35-b955-776923076819\">ece050d8</a>) and " +
		"<a href=\"/share/t/coders/ece050d8-c7a1-4c35-b955-776923076819\">ece050d8-c7a1-4c35-b955-776923076819</a>. Not ece050d81 &lt;b&gt;"
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if got := (coderLinker{hrefs: map[string]string{}}).linkedText("a <b>"); got != "a &lt;b&gt;" {
		t.Fatalf("an empty linker reads %q", got)
	}
}
