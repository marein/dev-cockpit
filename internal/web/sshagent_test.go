package web

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/git"
	"github.com/marein/dev-cockpit/internal/notify"
	"github.com/marein/dev-cockpit/internal/project"
	"github.com/marein/dev-cockpit/internal/settings"
	"github.com/marein/dev-cockpit/internal/sshagent"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type agentFixture struct {
	s      *Server
	broker *askpass.Broker
	agent  *sshagent.Service
	router *gin.Engine
	pub    ssh.PublicKey
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	for _, name := range []string{"ssh-agent", "ssh-add", "ssh-keygen", "cat"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not installed", name)
		}
	}
	gin.SetMode(gin.TestMode)
	stateDir, err := os.MkdirTemp("", "dcwa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	sshDir := t.TempDir()
	key := filepath.Join(sshDir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "secret", "-C", "me@test", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	data, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		t.Fatal(err)
	}
	store := settings.New(filepath.Join(stateDir, "settings.json"))
	broker := askpass.New(stateDir)
	listener, err := askpass.Listen(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() { _ = http.Serve(listener, broker.Handler()) }()
	script, err := askpass.WriteScript(stateDir)
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{
		notifier: notify.NewService(filepath.Join(stateDir, "notifications.json"), nil),
		bus:      eventbus.New(),
		settings: store,
	}
	s.approvals = s.newApprovals(stateDir)
	s.SetAskpass(broker, script)
	a := sshagent.New(stateDir, sshDir, "", store.Get)
	a.Start()
	s.SetSSHAgent(a)
	r := gin.New()
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.POST("/git/prompt", s.handleGitPromptAnswer)
	r.POST(sshAgentPath, s.handleSettingsSSHAgentSave)
	return &agentFixture{s: s, broker: broker, agent: a, router: r, pub: pub}
}

// gitContext is a request the way a route hands it on: from a browser, or
// over the local socket.
func gitContext(local bool) *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	if local {
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	}
	c.Request = req
	return c
}

// agentSocket answers the agent socket a git call carries, empty for none.
func agentSocket(prompt *git.Prompt) string {
	for _, e := range prompt.Env {
		if v, ok := strings.CutPrefix(e, "SSH_AUTH_SOCK="); ok {
			return v
		}
	}
	return ""
}

// sign starts one signature through a kind's socket and answers its outcome.
func (f *agentFixture) sign(t *testing.T, kind sshagent.Kind) <-chan error {
	t.Helper()
	c, err := net.Dial("unix", f.agent.Socket(kind))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	signed := make(chan error, 1)
	go func() {
		_, err := agent.NewClient(c).Sign(f.pub, []byte("challenge"))
		signed <- err
	}()
	return signed
}

func (f *agentFixture) post(t *testing.T, path, contentType, body string, local bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	if local {
		req = req.WithContext(context.WithValue(req.Context(), localCallKey, true))
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *agentFixture) waitQuestion(t *testing.T) askpass.Question {
	t.Helper()
	var q askpass.Question
	waitFor(t, "the question stands", func() bool {
		list := f.broker.Questions()
		if len(list) == 1 {
			q = list[0]
			return true
		}
		return false
	})
	return q
}

// A shell's question stands under its own key and names the kind of caller,
// the key and the process connected to the socket. It is news like a proxied
// git question, nobody in the app started it. The shell cannot answer it over
// the local socket, the browser can, and the passphrase reaches ssh-add,
// which opens the key in the agents.
func TestAQuestionNamesItsKindAndOnlyABrowserAnswersIt(t *testing.T) {
	f := newAgentFixture(t)
	signed := f.sign(t, sshagent.KindShell)
	q := f.waitQuestion(t)
	id, ok := askpass.SSHKeyID(q.Key)
	if !ok || q.Kind != askpass.KindSSHKey || !q.External || !q.Unlock || q.Action != "Shell" || len(q.Callers) != 1 || q.Callers[0].Command == "" {
		t.Fatalf("the question reads %+v", q)
	}
	rows := map[string]string{}
	for _, d := range q.Details {
		rows[d.Label] = d.Value
	}
	if rows["Caller"] != "Shell" || !strings.HasSuffix(rows["Key"], "id_ed25519") || rows["Fingerprint"] != ssh.FingerprintSHA256(f.pub) {
		t.Fatalf("the details read %v", rows)
	}
	waitFor(t, "the question is news", func() bool {
		return f.s.notifier.UnreadTargets()[notify.SSHKeyTarget(id)]
	})
	body := `{"key":"` + q.Key + `","id":"` + q.ID + `","answer":"secret"}`
	if rec := f.post(t, "/git/prompt", "application/json", body, true); rec.Code != http.StatusForbidden {
		t.Fatalf("a local answer got %d", rec.Code)
	}
	if rec := f.post(t, "/git/prompt", "application/json", body, false); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("the browser's answer got %d: %s", rec.Code, rec.Body.String())
	}
	if err := <-signed; err != nil {
		t.Fatalf("the signature failed: %v", err)
	}
	if !f.agent.Status()[0].Open {
		t.Fatal("the answer did not open the key")
	}
	waitFor(t, "the news is read once the question is gone", func() bool {
		return !f.s.notifier.UnreadTargets()[notify.SSHKeyTarget(id)]
	})
}

// The UI's question names it and stays off the news, somebody pressed a
// button for it. An assistant's names the assistants.
func TestTheUIsQuestionStaysOffTheNews(t *testing.T) {
	f := newAgentFixture(t)
	f.sign(t, sshagent.KindUI)
	q := f.waitQuestion(t)
	if q.Action != "Git action" || q.External || questionTarget(q) != "" {
		t.Fatalf("the UI's question reads %+v", q)
	}
	if a := f.broker.Find(q.Key); a != nil {
		a.End()
	}
	waitFor(t, "the UI's question ends", func() bool { return len(f.broker.Questions()) == 0 })
	f.sign(t, sshagent.KindAssistant)
	if q := f.waitQuestion(t); q.Action != "Assistant" || !q.External {
		t.Fatalf("the assistant's question reads %+v", q)
	}
}

// A git action of the UI carries the UI's socket for a browser, and none for
// the same route over the local socket. The proxy carries the socket the
// caller's environment names, for a local call, and none for a browser or a
// path that is no socket of the agent.
func TestAGitCallGetsTheSocketOfItsCallersKind(t *testing.T) {
	f := newAgentFixture(t)
	action, prompt, open := f.s.promptAction(gitContext(false), "shop", "push")
	if !open || action == nil {
		t.Fatal("the bridge did not open")
	}
	action.End()
	if got := agentSocket(prompt); got != f.agent.Socket(sshagent.KindUI) {
		t.Fatalf("the browser's git action carries %q", got)
	}
	action, prompt, open = f.s.promptAction(gitContext(true), "shop", "push")
	if !open || action == nil {
		t.Fatal("the bridge did not open")
	}
	action.End()
	if got := agentSocket(prompt); got != "" {
		t.Fatalf("a local call of a git action carries %q", got)
	}
	coder := f.agent.Socket(sshagent.KindCoder)
	if got := f.s.proxyKind(gitContext(true), coder); got != sshagent.KindCoder {
		t.Fatalf("a coder's proxied call reads as %q", got)
	}
	for name, kind := range map[string]sshagent.Kind{
		"a browser's":       f.s.proxyKind(gitContext(false), coder),
		"a foreign agent's": f.s.proxyKind(gitContext(true), "/run/user/1000/agent.sock"),
		"one without":       f.s.proxyKind(gitContext(true), ""),
	} {
		if kind != "" {
			t.Fatalf("%s proxied call reads as %q", name, kind)
		}
	}
	action, prompt, open = f.s.promptActionCommand("shop", "push", "git push", "/tmp", sshagent.KindCoder)
	if !open || action == nil {
		t.Fatal("the bridge did not open")
	}
	action.End()
	if got := agentSocket(prompt); got != coder {
		t.Fatalf("the coder's proxied call carries %q", got)
	}
}

// The quiet fetch runs without the agent, the way git ran before it: the ssh
// git starts for it, here a program the repository's own config names, finds
// no socket in its environment. A browser's request and a local one alike.
func TestTheQuietFetchRunsWithoutTheAgent(t *testing.T) {
	f := newAgentFixture(t)
	f.s.gitWrites = newGitWrites()
	t.Setenv("SSH_AUTH_SOCK", "")
	dir, tmp := t.TempDir(), t.TempDir()
	out, planted := filepath.Join(tmp, "out"), filepath.Join(tmp, "ssh")
	if err := os.WriteFile(planted, []byte("#!/bin/sh\nprintf 'sock=%s' \"$SSH_AUTH_SOCK\" > '"+out+"'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	worktreeGit(t, dir, "init", "-q")
	worktreeGit(t, dir, "remote", "add", "origin", "ssh://e2e.invalid/x")
	worktreeGit(t, dir, "config", "core.sshCommand", planted)
	p := project.Project{Name: "shop", Path: dir}
	for name, c := range map[string]*gin.Context{
		"a browser's":    gitContext(false),
		"a local call's": gitContext(true),
	} {
		_ = os.Remove(out)
		_ = os.Remove(filepath.Join(dir, ".git", "FETCH_HEAD"))
		f.s.quietFetch(c, p)
		got, err := os.ReadFile(out)
		if err != nil {
			t.Fatalf("%s quiet fetch never ran the planted program: %v", name, err)
		}
		if string(got) != "sock=" {
			t.Fatalf("%s quiet fetch handed the planted program an agent: %s", name, got)
		}
	}
}

// The policies decide who signs without the person, so the local socket may
// not change them and a value nobody knows is refused; forgetting is not
// refused.
func TestTheSettingsRefuseLocalAndUnknownValues(t *testing.T) {
	f := newAgentFixture(t)
	form := url.Values{"form": {"policy"}, "policy_ui": {"allow"}, "policy_shell": {"allow"}, "policy_coder": {"deny"}, "policy_assistant": {"allow"}, "ttl_minutes": {"30"}}
	const formType = "application/x-www-form-urlencoded"
	if rec := f.post(t, sshAgentPath, formType, form.Encode(), true); rec.Code != http.StatusForbidden {
		t.Fatalf("a local save got %d", rec.Code)
	}
	if sshagent.PolicyOf(f.s.settings.Get, sshagent.KindShell) != sshagent.Ask {
		t.Fatal("a local save moved a policy")
	}
	bad := url.Values{"form": {"policy"}, "policy_ui": {"allow"}, "policy_shell": {"always"}, "policy_coder": {"deny"}, "policy_assistant": {"allow"}, "ttl_minutes": {"30"}}
	f.post(t, sshAgentPath, formType, bad.Encode(), false)
	missing := url.Values{"form": {"policy"}, "policy_ui": {"allow"}, "policy_shell": {"allow"}, "policy_coder": {"deny"}, "ttl_minutes": {"30"}}
	f.post(t, sshAgentPath, formType, missing.Encode(), false)
	tooLong := url.Values{"form": {"policy"}, "policy_ui": {"allow"}, "policy_shell": {"allow"}, "policy_coder": {"deny"}, "policy_assistant": {"allow"}, "ttl_minutes": {"999999"}}
	f.post(t, sshAgentPath, formType, tooLong.Encode(), false)
	if sshagent.PolicyOf(f.s.settings.Get, sshagent.KindCoder) != sshagent.Ask {
		t.Fatal("a refused save stored a policy")
	}
	if rec := f.post(t, sshAgentPath, formType, form.Encode(), false); rec.Code != http.StatusFound && rec.Code != http.StatusSeeOther {
		t.Fatalf("the save got %d", rec.Code)
	}
	if sshagent.PolicyOf(f.s.settings.Get, sshagent.KindShell) != sshagent.Allow ||
		sshagent.PolicyOf(f.s.settings.Get, sshagent.KindCoder) != sshagent.Deny ||
		sshagent.PolicyOf(f.s.settings.Get, sshagent.KindAssistant) != sshagent.Allow ||
		sshagent.TTLOf(f.s.settings.Get).Minutes() != 30 {
		t.Fatal("the save did not store what was posted")
	}
	forget := url.Values{"form": {"forget"}, "fingerprint": {ssh.FingerprintSHA256(f.pub)}}
	if rec := f.post(t, sshAgentPath, formType, forget.Encode(), true); rec.Code == http.StatusForbidden {
		t.Fatal("a local forget was refused")
	}
}
