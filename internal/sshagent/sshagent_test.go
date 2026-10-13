package sshagent

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

type settings struct {
	mu sync.Mutex
	m  map[string]string
}

func (s *settings) get(k string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[k]
}

func (s *settings) set(k, v string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[k] = v
}

func writeKey(t *testing.T, dir, name, passphrase string) ssh.PublicKey {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var block *pem.Block
	if passphrase == "" {
		block, err = ssh.MarshalPrivateKey(priv, "me@test")
	} else {
		block, err = ssh.MarshalPrivateKeyWithPassphrase(priv, "me@test", []byte(passphrase))
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name+".pub"), ssh.MarshalAuthorizedKey(sshPub), 0o644); err != nil {
		t.Fatal(err)
	}
	return sshPub
}

type fixture struct {
	s        *Service
	settings *settings
	state    string
	ssh      string
	asked    atomic.Int32
	mu       sync.Mutex
	answers  []string
	requests []Request
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	state, err := os.MkdirTemp("", "dcag")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(state) })
	f := &fixture{settings: &settings{m: map[string]string{}}, state: state, ssh: t.TempDir()}
	f.s = New(state, f.ssh, "", f.settings.get)
	f.s.SetAsker(f.ask)
	return f
}

func (f *fixture) ask(_ context.Context, r Request) (string, bool) {
	f.asked.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r)
	if len(f.answers) == 0 {
		return "", false
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a, true
}

func (f *fixture) answer(a ...string) {
	f.mu.Lock()
	f.answers = append(f.answers, a...)
	f.mu.Unlock()
}

func (f *fixture) lastRequest(t *testing.T) Request {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("nothing was asked")
	}
	return f.requests[len(f.requests)-1]
}

func dial(t *testing.T, path string) agent.ExtendedAgent {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return agent.NewClient(c)
}

func sockOf(t *testing.T, env map[string]string) string {
	t.Helper()
	path := env["SSH_AUTH_SOCK"]
	if path == "" {
		t.Fatal("the session got no socket")
	}
	return path
}

func signs(t *testing.T, a agent.ExtendedAgent, pub ssh.PublicKey) bool {
	t.Helper()
	data := []byte("challenge")
	sig, err := a.Sign(pub, data)
	if err != nil {
		return false
	}
	if err := pub.Verify(data, sig); err != nil {
		t.Fatalf("the signature does not verify: %v", err)
	}
	return true
}

func TestDefaultsAndParsing(t *testing.T) {
	empty := func(string) string { return "" }
	if PolicyOf(empty, KindUI) != Allow || PolicyOf(empty, KindShell) != Ask || PolicyOf(empty, KindCoder) != Ask {
		t.Fatal("the default policies are wrong")
	}
	if TTLOf(empty) != time.Hour {
		t.Fatal("the default time is not one hour")
	}
	bogus := func(string) string { return "sometimes" }
	if PolicyOf(bogus, KindShell) != Ask {
		t.Fatal("a stored value nobody knows did not fall back to the default")
	}
	for _, v := range []string{"0", "-5", "x", "", "100000"} {
		if _, ok := ParseTTL(v); ok {
			t.Fatalf("%q was taken as a time", v)
		}
	}
	if ttl, ok := ParseTTL(" 90 "); !ok || ttl != 90*time.Minute {
		t.Fatal("90 minutes were not read")
	}
}

// The agent offers the protected keys only. A key without a passphrase stays
// ssh's own, so nothing about it changes, and a key the agent cannot read
// is simply not there.
func TestOnlyProtectedKeysAreOffered(t *testing.T) {
	f := newFixture(t)
	protected := writeKey(t, f.ssh, "id_ed25519", "secret")
	writeKey(t, f.ssh, "id_ecdsa", "")
	if err := os.WriteFile(filepath.Join(f.ssh, "id_rsa"), []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	keys := f.s.keys()
	if len(keys) != 2 {
		t.Fatalf("read %d keys, want the protected and the plain one", len(keys))
	}
	env, _ := f.s.OpenSession(KindShell)
	list, err := dial(t, sockOf(t, env)).List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || string(list[0].Marshal()) != string(protected.Marshal()) {
		t.Fatalf("offered %v, want the protected key alone", list)
	}
}

// A shell asks: the first signature asks for the passphrase, a wrong one asks
// again, the right one opens the key, and the next signature only asks
// whether the shell may sign. The request names the caller and the key.
func TestAShellAsksAndOpensTheKey(t *testing.T) {
	f := newFixture(t)
	pub := writeKey(t, f.ssh, "id_ed25519", "secret")
	f.s.SetResolver(func(tag string) (Caller, bool) {
		return Caller{Name: "build", Project: "shop"}, true
	})
	env, tag := f.s.OpenSession(KindShell)
	if !sessionTag.MatchString(tag) || kindOfTag(tag) != KindShell {
		t.Fatalf("the tag %q does not name a shell", tag)
	}
	a := dial(t, sockOf(t, env))
	f.answer("wrong", "secret")
	if !signs(t, a, pub) {
		t.Fatal("the signature was refused")
	}
	r := f.lastRequest(t)
	if !r.Unlock || !r.Retry || r.Caller.Kind != KindShell || r.Caller.Name != "build" || r.Caller.Project != "shop" || r.Key.Path != filepath.Join(f.ssh, "id_ed25519") {
		t.Fatalf("the request reads %+v", r)
	}
	if len(r.Command) == 0 {
		t.Fatal("the asking process was not read")
	}
	status := f.s.Status()
	if len(status) != 1 || !status[0].Open || time.Until(status[0].Expires) < 59*time.Minute {
		t.Fatalf("the key reads %+v after it was opened", status)
	}
	f.answer("")
	if !signs(t, a, pub) {
		t.Fatal("the allowed second signature was refused")
	}
	if r := f.lastRequest(t); r.Unlock {
		t.Fatal("an open key asked for the passphrase again")
	}
	if n := f.asked.Load(); n != 3 {
		t.Fatalf("asked %d times, want two passphrases and one consent", n)
	}
	if signs(t, a, pub) {
		t.Fatal("a declined signature went through")
	}
}

// Allow signs an open key without a word, and Deny offers nothing and signs
// nothing. Both are read on every request, a saved change applies at once.
func TestPoliciesApplyAtOnce(t *testing.T) {
	f := newFixture(t)
	pub := writeKey(t, f.ssh, "id_ed25519", "secret")
	env, _ := f.s.OpenSession(KindCoder)
	a := dial(t, sockOf(t, env))
	f.settings.set(PolicySettingKey(KindCoder), string(Allow))
	f.answer("secret")
	if !signs(t, a, pub) {
		t.Fatal("the first signature was refused")
	}
	if !signs(t, a, pub) || f.asked.Load() != 1 {
		t.Fatal("an allowed caller was asked for an open key")
	}
	f.settings.set(PolicySettingKey(KindCoder), string(Deny))
	if list, _ := a.List(); len(list) != 0 {
		t.Fatal("a refused caller was offered keys")
	}
	if signs(t, a, pub) || f.asked.Load() != 1 {
		t.Fatal("a refused caller signed or was asked")
	}
}

// The quiet socket never asks: it signs with an open key while the UI may sign
// without asking, and is refused at once otherwise.
func TestTheQuietSocketNeverAsks(t *testing.T) {
	f := newFixture(t)
	pub := writeKey(t, f.ssh, "id_ed25519", "secret")
	if err := f.s.Start(func() ([]string, error) { return nil, nil }); err != nil {
		t.Fatal(err)
	}
	quiet := dial(t, strings.TrimPrefix(f.s.QuietEnv()[0], "SSH_AUTH_SOCK="))
	if list, _ := quiet.List(); len(list) != 0 {
		t.Fatal("the quiet socket offered a locked key")
	}
	if signs(t, quiet, pub) || f.asked.Load() != 0 {
		t.Fatal("the quiet socket signed or asked with a locked key")
	}
	env, _ := f.s.OpenSession(KindShell)
	f.answer("secret")
	if !signs(t, dial(t, sockOf(t, env)), pub) {
		t.Fatal("the shell could not open the key")
	}
	if !signs(t, quiet, pub) {
		t.Fatal("the quiet socket did not sign with the open key")
	}
	f.settings.set(PolicySettingKey(KindUI), string(Ask))
	if signs(t, quiet, pub) || f.asked.Load() != 1 {
		t.Fatal("the quiet socket signed or asked while the UI has to ask")
	}
}

// An action's socket asks through the action and names the caller it was
// opened for; closing it is the end of the action.
func TestAnActionAsksThroughItsOwnAsker(t *testing.T) {
	f := newFixture(t)
	pub := writeKey(t, f.ssh, "id_ed25519", "secret")
	var got Request
	env, closeAction := f.s.OpenAction(Caller{Kind: KindUI, Name: "push", Project: "shop"}, func(_ context.Context, r Request) (string, bool) {
		got = r
		return "secret", true
	})
	path := strings.TrimPrefix(env[0], "SSH_AUTH_SOCK=")
	if !signs(t, dial(t, path), pub) {
		t.Fatal("the action could not sign")
	}
	if got.Caller.Name != "push" || got.Caller.Kind != KindUI || !got.Unlock || f.asked.Load() != 0 {
		t.Fatalf("the action asked %+v, or the session asker was used", got)
	}
	closeAction()
	if _, err := net.Dial("unix", path); err == nil {
		t.Fatal("the closed action socket still answers")
	}
}

// Forgetting and the end of the time both lock the key again, and so does a
// shorter time saved while it is open.
func TestForgetAndExpiry(t *testing.T) {
	f := newFixture(t)
	pub := writeKey(t, f.ssh, "id_ed25519", "secret")
	var moved atomic.Int32
	f.s.SetOnChange(func() { moved.Add(1) })
	now := time.Now()
	f.s.now = func() time.Time { return now }
	env, _ := f.s.OpenSession(KindShell)
	f.settings.set(PolicySettingKey(KindShell), string(Allow))
	a := dial(t, sockOf(t, env))
	f.answer("secret")
	if !signs(t, a, pub) {
		t.Fatal("the key did not open")
	}
	fp := ssh.FingerprintSHA256(pub)
	if !f.s.Forget(fp) || f.s.Forget(fp) {
		t.Fatal("forget did not answer whether the key was open")
	}
	if signs(t, a, pub) {
		t.Fatal("a forgotten key signed without its passphrase")
	}
	f.answer("secret")
	if !signs(t, a, pub) {
		t.Fatal("the key did not open again")
	}
	f.settings.set(TTLSettingKey, "5")
	now = now.Add(6 * time.Minute)
	f.s.expire()
	if f.s.Status()[0].Open {
		t.Fatal("the key is open past its time")
	}
	if moved.Load() < 4 {
		t.Fatalf("the change hook fired %d times, want open, forget, open, expire", moved.Load())
	}
}

// A restarted cockpit listens again on the sockets of the sessions that
// survived, under the same path, and closes the ones whose session is gone.
func TestReconcileListensAgainAfterARestart(t *testing.T) {
	f := newFixture(t)
	pub := writeKey(t, f.ssh, "id_ed25519", "secret")
	env, tag := f.s.OpenSession(KindShell)
	_, goneTag := f.s.OpenSession(KindCoder)
	path := sockOf(t, env)
	for _, l := range f.s.listeners {
		_ = l.ln.Close()
	}
	after := New(f.state, f.ssh, "", f.settings.get)
	after.SetAsker(f.ask)
	if err := after.Start(func() ([]string, error) { return []string{tag, goneTag, "not-a-tag"}, nil }); err != nil {
		t.Fatal(err)
	}
	f.answer("secret")
	if !signs(t, dial(t, path), pub) {
		t.Fatal("the surviving session's socket does not answer after the restart")
	}
	after.now = func() time.Time { return time.Now().Add(2 * reapGrace) }
	after.Reconcile([]string{tag})
	if _, ok := after.listeners[goneTag]; ok {
		t.Fatal("the socket of a gone session is still open")
	}
	if _, ok := after.SessionCaller(path); !ok {
		t.Fatal("the live session's socket is not known")
	}
	if _, ok := after.SessionCaller(filepath.Join(after.dir, goneTag)); ok {
		t.Fatal("a closed socket still names a caller")
	}
}

// A process that hangs up while its question stands takes the question
// along, instead of leaving it for somebody to answer into nothing.
func TestAHungUpCallerEndsItsQuestion(t *testing.T) {
	f := newFixture(t)
	pub := writeKey(t, f.ssh, "id_ed25519", "secret")
	ended := make(chan struct{})
	f.s.SetAsker(func(ctx context.Context, _ Request) (string, bool) {
		<-ctx.Done()
		close(ended)
		return "", false
	})
	env, _ := f.s.OpenSession(KindShell)
	c, err := net.Dial("unix", sockOf(t, env))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = agent.NewClient(c).Sign(pub, []byte("x")) }()
	time.Sleep(200 * time.Millisecond)
	_ = c.Close()
	select {
	case <-ended:
	case <-time.After(5 * time.Second):
		t.Fatal("the question outlived the process that asked it")
	}
}

// An agent found at start wins: nothing listens and no session gets a socket.
func TestAnExternalAgentWins(t *testing.T) {
	f := newFixture(t)
	s := New(f.state, f.ssh, "/run/user/1000/agent.sock", f.settings.get)
	if s.Active() {
		t.Fatal("the cockpit's agent runs next to the host's")
	}
	if env, tag := s.OpenSession(KindShell); env != nil || tag != "" {
		t.Fatal("a session got a socket of an agent that is off")
	}
	if env, _ := s.OpenAction(Caller{Kind: KindUI}, nil); env != nil || s.QuietEnv() != nil {
		t.Fatal("a git call got a socket of an agent that is off")
	}
}

func TestSocketsAreTheUsersAlone(t *testing.T) {
	f := newFixture(t)
	env, _ := f.s.OpenSession(KindShell)
	path := sockOf(t, env)
	dir, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	sock, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if dir.Mode().Perm() != 0o700 || sock.Mode().Perm() != 0o600 {
		t.Fatalf("the directory is %v and the socket %v", dir.Mode().Perm(), sock.Mode().Perm())
	}
}
