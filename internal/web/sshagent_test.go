package web

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ginsessions "github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
	"github.com/marein/dev-cockpit/internal/askpass"
	"github.com/marein/dev-cockpit/internal/eventbus"
	"github.com/marein/dev-cockpit/internal/notify"
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
	gin.SetMode(gin.TestMode)
	stateDir, err := os.MkdirTemp("", "dcwa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(stateDir) })
	sshDir := t.TempDir()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	block, err := ssh.MarshalPrivateKeyWithPassphrase(priv, "me@test", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_ed25519"), pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	store := settings.New(filepath.Join(stateDir, "settings.json"))
	broker := askpass.New(stateDir)
	s := &Server{
		notifier: notify.NewService(filepath.Join(stateDir, "notifications.json"), nil),
		bus:      eventbus.New(),
		settings: store,
	}
	s.approvals = s.newApprovals(stateDir)
	s.SetAskpass(broker, "/nowhere/helper")
	a := sshagent.New(stateDir, sshDir, "", store.Get)
	a.SetResolver(func(string) (sshagent.Caller, bool) {
		return sshagent.Caller{Name: "build", Project: "shop"}, true
	})
	s.SetSSHAgent(a)
	r := gin.New()
	r.Use(ginsessions.Sessions("session", cookie.NewStore([]byte("test-key"))))
	r.POST("/git/prompt", s.handleGitPromptAnswer)
	r.POST(sshAgentPath, s.handleSettingsSSHAgentSave)
	return &agentFixture{s: s, broker: broker, agent: a, router: r, pub: signer.PublicKey()}
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

// A shell's question stands under its own key and names the shell, the
// project and the key. It is news like a proxied git question, nobody in the
// app started it. The shell cannot answer it over the local socket, the
// browser can, and the answer opens the key.
func TestATerminalsQuestionIsNewsAndOnlyABrowserAnswersIt(t *testing.T) {
	f := newAgentFixture(t)
	env, _ := f.agent.OpenSession(sshagent.KindShell)
	c, err := net.Dial("unix", env["SSH_AUTH_SOCK"])
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	signed := make(chan error, 1)
	go func() {
		_, err := agent.NewClient(c).Sign(f.pub, []byte("challenge"))
		signed <- err
	}()
	q := f.waitQuestion(t)
	id, ok := askpass.SSHKeyID(q.Key)
	if !ok || q.Kind != askpass.KindSSHKey || !q.External || !q.Unlock || q.Action != "Shell: build" {
		t.Fatalf("the question reads %+v", q)
	}
	rows := map[string]string{}
	for _, d := range q.Details {
		rows[d.Label] = d.Value
	}
	if rows["Shell"] != "build" || rows["Project"] != "shop" || !strings.HasSuffix(rows["Key"], "id_ed25519") || rows["Fingerprint"] != ssh.FingerprintSHA256(f.pub) {
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

// A git action of the UI carries a socket of its own in its environment, and
// the agent's question stands on the action itself, under its project, so the
// action's dialog and deadline cover it. The action's end closes the socket.
func TestAGitActionAsksOnItsOwnAction(t *testing.T) {
	f := newAgentFixture(t)
	action, prompt, open := f.s.promptAction("shop", "push")
	if !open || action == nil {
		t.Fatal("the bridge did not open")
	}
	sock := ""
	for _, e := range prompt.Env {
		if v, ok := strings.CutPrefix(e, "SSH_AUTH_SOCK="); ok {
			sock = v
		}
	}
	if sock == "" {
		t.Fatal("the action carries no agent socket")
	}
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	go func() { _, _ = agent.NewClient(c).Sign(f.pub, []byte("challenge")) }()
	q := f.waitQuestion(t)
	if q.Key != "shop" || q.Kind != askpass.KindSSHKey || q.External || q.Action != "Git action: push" {
		t.Fatalf("the question reads %+v", q)
	}
	select {
	case <-action.Asked():
	default:
		t.Fatal("the action's deadline did not hear the question")
	}
	action.End()
	waitFor(t, "the action's socket closes with it", func() bool {
		_, err := net.Dial("unix", sock)
		return err != nil
	})
}

// The proxy signs as the session whose socket the caller names, and a socket
// that is no session's of this agent leaves the call without the agent.
func TestTheProxySignsAsItsSession(t *testing.T) {
	f := newAgentFixture(t)
	env, _ := f.agent.OpenSession(sshagent.KindCoder)
	caller := f.s.proxyCaller(env["SSH_AUTH_SOCK"])
	if caller == nil || caller.Kind != sshagent.KindCoder || caller.Name != "build" {
		t.Fatalf("the proxy reads its caller as %+v", caller)
	}
	for _, sock := range []string{"", "/tmp/ssh-XXXX/agent.1", filepath.Join(filepath.Dir(env["SSH_AUTH_SOCK"]), "q")} {
		if f.s.proxyCaller(sock) != nil {
			t.Fatalf("%q was read as a session", sock)
		}
	}
}

// The policies decide who signs without the person, so the local socket may
// not change them and a value nobody knows is refused; forgetting is not
// refused.
func TestTheSettingsRefuseLocalAndUnknownValues(t *testing.T) {
	f := newAgentFixture(t)
	form := url.Values{"form": {"policy"}, "policy_ui": {"allow"}, "policy_shell": {"allow"}, "policy_coder": {"deny"}, "ttl_minutes": {"30"}}
	const formType = "application/x-www-form-urlencoded"
	if rec := f.post(t, sshAgentPath, formType, form.Encode(), true); rec.Code != http.StatusForbidden {
		t.Fatalf("a local save got %d", rec.Code)
	}
	if sshagent.PolicyOf(f.s.settings.Get, sshagent.KindShell) != sshagent.Ask {
		t.Fatal("a local save moved a policy")
	}
	bad := url.Values{"form": {"policy"}, "policy_ui": {"allow"}, "policy_shell": {"always"}, "policy_coder": {"deny"}, "ttl_minutes": {"30"}}
	f.post(t, sshAgentPath, formType, bad.Encode(), false)
	tooLong := url.Values{"form": {"policy"}, "policy_ui": {"allow"}, "policy_shell": {"allow"}, "policy_coder": {"deny"}, "ttl_minutes": {"999999"}}
	f.post(t, sshAgentPath, formType, tooLong.Encode(), false)
	if sshagent.PolicyOf(f.s.settings.Get, sshagent.KindCoder) != sshagent.Ask {
		t.Fatal("a refused save stored a policy")
	}
	if rec := f.post(t, sshAgentPath, formType, form.Encode(), false); rec.Code != http.StatusFound && rec.Code != http.StatusSeeOther {
		t.Fatalf("the save got %d", rec.Code)
	}
	if sshagent.PolicyOf(f.s.settings.Get, sshagent.KindShell) != sshagent.Allow ||
		sshagent.PolicyOf(f.s.settings.Get, sshagent.KindCoder) != sshagent.Deny ||
		sshagent.TTLOf(f.s.settings.Get).Minutes() != 30 {
		t.Fatal("the save did not store what was posted")
	}
	forget := url.Values{"form": {"forget"}, "fingerprint": {ssh.FingerprintSHA256(f.pub)}}
	if rec := f.post(t, sshAgentPath, formType, forget.Encode(), true); rec.Code == http.StatusForbidden {
		t.Fatal("a local forget was refused")
	}
}
