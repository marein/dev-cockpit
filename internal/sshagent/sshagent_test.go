package sshagent

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marein/dev-cockpit/internal/askpass"
	"golang.org/x/crypto/ssh"
	xagent "golang.org/x/crypto/ssh/agent"
)

const holdArg = "hold-the-socket"

// The test binary stands in for the askpass helper the agent's processes
// call, and for a second caller that holds a socket open until its input
// ends.
func init() {
	if len(os.Args) < 2 {
		return
	}
	switch os.Args[1] {
	case "askpass":
		if askpass.Helper(os.Args[2:], os.Stdout) {
			os.Exit(0)
		}
		os.Exit(1)
	case holdArg:
		c, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
		if err != nil {
			os.Exit(1)
		}
		defer c.Close()
		os.Stdout.WriteString("held\n")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		os.Exit(0)
	}
}

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

type reply struct {
	passphrase string
	ok         bool
}

type fixture struct {
	state, sshDir string
	set           *settings
	pub           ssh.PublicKey
	plain         ssh.PublicKey
	replies       chan reply
	mu            sync.Mutex
	asked         []Request
	ended         int
}

func requireOpenSSH(t *testing.T) {
	t.Helper()
	for _, name := range []string{"ssh-agent", "ssh-add", "ssh-keygen", "cat"} {
		if _, err := exec.LookPath(name); err != nil {
			t.Skipf("%s is not installed", name)
		}
	}
}

// writeKey makes a key with ssh-keygen and answers its public key.
func writeKey(t *testing.T, dir, name, passphrase string) ssh.PublicKey {
	t.Helper()
	path := filepath.Join(dir, name)
	typ := strings.TrimPrefix(name, "id_")
	out, err := exec.Command("ssh-keygen", "-q", "-t", typ, "-N", passphrase, "-C", "me@test", "-f", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ssh-keygen: %v %s", err, out)
	}
	data, err := os.ReadFile(path + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		t.Fatal(err)
	}
	return pub
}

// newFixture is a started agent over a passphrase protected ed25519 key and
// a plain ecdsa key. Its state directory is short, the sockets have to fit.
func newFixture(t *testing.T) (*fixture, *Service) {
	t.Helper()
	requireOpenSSH(t)
	state, err := os.MkdirTemp("", "dcsa")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(state) })
	f := &fixture{state: state, sshDir: t.TempDir(), set: &settings{m: map[string]string{}}, replies: make(chan reply, 8)}
	f.pub = writeKey(t, f.sshDir, "id_ed25519", "secret")
	f.plain = writeKey(t, f.sshDir, "id_ecdsa", "")
	return f, f.start(t)
}

func (f *fixture) start(t *testing.T) *Service {
	t.Helper()
	b := askpass.New(f.state)
	ln, err := askpass.Listen(f.state)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { _ = http.Serve(ln, b.Handler()) }()
	script, err := askpass.WriteScript(f.state)
	if err != nil {
		t.Fatal(err)
	}
	s := New(f.state, f.sshDir, "", f.set.get)
	s.SetAsker(f.ask)
	s.SetPrompter(func(answer func(string) ([]byte, bool)) ([]string, func()) {
		env, end := b.Serve(answer)
		return append(env, "SSH_ASKPASS="+script, "SSH_ASKPASS_REQUIRE=force"), end
	})
	s.Start()
	t.Cleanup(s.stop)
	return s
}

// stop is what the end of the process does to the agents, at once.
func (s *Service) stop() {
	s.mu.Lock()
	listeners := s.listeners
	s.listeners = map[Kind]net.Listener{}
	s.mu.Unlock()
	for _, ln := range listeners {
		_ = ln.Close()
	}
	for _, k := range Kinds {
		s.mu.Lock()
		a := s.kinds[k].agent
		s.mu.Unlock()
		if a != nil {
			s.retire(k, a)
		}
	}
}

func (f *fixture) ask(ctx context.Context, r Request) ([]byte, bool) {
	f.mu.Lock()
	f.asked = append(f.asked, r)
	f.mu.Unlock()
	select {
	case got := <-f.replies:
		return []byte(got.passphrase), got.ok
	case <-ctx.Done():
		f.mu.Lock()
		f.ended++
		f.mu.Unlock()
		return nil, false
	}
}

func (f *fixture) answer(passphrases ...string) {
	for _, p := range passphrases {
		f.replies <- reply{passphrase: p, ok: true}
	}
}

func (f *fixture) deny() { f.replies <- reply{} }

func (f *fixture) requests() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.asked)
}

func (f *fixture) dial(t *testing.T, s *Service, k Kind) xagent.ExtendedAgent {
	t.Helper()
	return dialAt(t, s.Socket(k))
}

func dialAt(t *testing.T, path string) xagent.ExtendedAgent {
	t.Helper()
	c, err := net.Dial("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return xagent.NewClient(c)
}

func signs(a xagent.ExtendedAgent, pub ssh.PublicKey) error {
	sig, err := a.Sign(pub, []byte("challenge"))
	if err != nil {
		return err
	}
	return pub.Verify([]byte("challenge"), sig)
}

// holding lists what a kind's ssh-agent holds, asked at its private socket.
func holding(t *testing.T, s *Service, k Kind) []*xagent.Key {
	t.Helper()
	s.mu.Lock()
	a := s.kinds[k].agent
	s.mu.Unlock()
	if a == nil {
		return nil
	}
	keys, err := dialAt(t, a.path).List()
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func TestDefaultsAndParsing(t *testing.T) {
	empty := func(string) string { return "" }
	for _, k := range Kinds {
		want := Ask
		if k == KindUI {
			want = Allow
		}
		if got := PolicyOf(empty, k); got != want {
			t.Fatalf("the default of %s is %s, want %s", k, got, want)
		}
	}
	if got := PolicyOf(func(string) string { return "deny" }, KindShell); got != Deny {
		t.Fatalf("a stored deny reads as %s", got)
	}
	if TTLOf(empty) != DefaultTTL {
		t.Fatal("the default time is not an hour")
	}
	for _, v := range []string{"0", "-1", "x", "10081", ""} {
		if _, ok := ParseTTL(v); ok {
			t.Fatalf("%q was taken as a time", v)
		}
	}
	if ttl, ok := ParseTTL(" 30 "); !ok || ttl != 30*time.Minute {
		t.Fatalf("30 reads as %v", ttl)
	}
	if _, ok := ParsePolicy("always"); ok {
		t.Fatal("an unknown policy was taken")
	}
}

// The page lists both keys from what ssh-keygen says, a socket offers only
// the one with a passphrase, and a kind under Off sees none.
func TestOnlyProtectedKeysAreOffered(t *testing.T) {
	f, s := newFixture(t)
	status := s.Status()
	if len(status) != 2 {
		t.Fatalf("the status lists %d keys", len(status))
	}
	byPath := map[string]KeyStatus{}
	for _, k := range status {
		byPath[filepath.Base(k.Path)] = k
	}
	if k := byPath["id_ed25519"]; !k.Protected || k.Open || k.Fingerprint != ssh.FingerprintSHA256(f.pub) || k.Comment != "me@test" || k.Type != "ED25519" {
		t.Fatalf("the protected key reads %+v", k)
	}
	if k := byPath["id_ecdsa"]; k.Protected || k.Fingerprint != ssh.FingerprintSHA256(f.plain) {
		t.Fatalf("the plain key reads %+v", k)
	}
	keys, err := f.dial(t, s, KindShell).List()
	if err != nil || len(keys) != 1 || string(keys[0].Blob) != string(f.pub.Marshal()) {
		t.Fatalf("the shells' socket offers %v, %v", keys, err)
	}
	f.set.set(PolicySettingKey(KindCoder), string(Deny))
	if keys, err := f.dial(t, s, KindCoder).List(); err != nil || len(keys) != 0 {
		t.Fatalf("the coders' socket under off offers %v, %v", keys, err)
	}
	if len(f.requests()) != 0 {
		t.Fatal("listing the keys asked")
	}
}

// A shell's first signature asks for the passphrase, again after a wrong
// one, and the right one puts the key into every agent that may use it: the
// shell's own with confirmation, without asking a second time for this
// signature, the UI's without. After that the shell asks for a yes on every
// use, the UI signs without a question.
func TestAShellAsksAndOpensTheKey(t *testing.T) {
	f, s := newFixture(t)
	f.answer("wrong", "secret")
	if err := signs(f.dial(t, s, KindShell), f.pub); err != nil {
		t.Fatalf("the signature failed: %v", err)
	}
	asked := f.requests()
	if len(asked) != 2 {
		t.Fatalf("the signature asked %d times: %+v", len(asked), asked)
	}
	for i, r := range asked {
		if r.Kind != KindShell || !r.Unlock || r.Retry != (i == 1) || filepath.Base(r.Key.Path) != "id_ed25519" {
			t.Fatalf("question %d reads %+v", i, r)
		}
		if len(r.Callers) != 1 || !slices.Contains(r.Callers[0].Command, os.Args[0]) {
			t.Fatalf("question %d names the callers %+v", i, r.Callers)
		}
	}
	for _, k := range []Kind{KindUI, KindShell, KindCoder, KindAssistant} {
		if keys := holding(t, s, k); len(keys) != 1 {
			t.Fatalf("the %s agent holds %d keys", k, len(keys))
		}
	}
	st := s.Status()
	for _, k := range st {
		if k.Protected && (!k.Open || k.Left < 59*time.Minute) {
			t.Fatalf("the opened key reads %+v", k)
		}
	}
	f.answer("")
	if err := signs(f.dial(t, s, KindShell), f.pub); err != nil {
		t.Fatalf("an allowed signature failed: %v", err)
	}
	if r := f.requests()[2]; r.Unlock || r.Kind != KindShell || filepath.Base(r.Key.Path) != "id_ed25519" {
		t.Fatalf("the open key's question reads %+v", r)
	}
	f.deny()
	if err := signs(f.dial(t, s, KindShell), f.pub); err == nil {
		t.Fatal("a denied signature went through")
	}
	if err := signs(f.dial(t, s, KindUI), f.pub); err != nil {
		t.Fatalf("the UI did not sign: %v", err)
	}
	if n := len(f.requests()); n != 4 {
		t.Fatalf("the UI's signature under allow asked, %d questions", n)
	}
}

// Each kind signs under its own policy: off offers no key and refuses, with
// no question, also once the key is open; a kind that allows signs with the
// open key unasked. A kind switched on again needs no passphrase.
func TestEachKindSignsUnderItsOwnPolicy(t *testing.T) {
	f, s := newFixture(t)
	f.set.set(PolicySettingKey(KindCoder), string(Deny))
	f.set.set(PolicySettingKey(KindShell), string(Allow))
	if err := signs(f.dial(t, s, KindCoder), f.pub); err == nil {
		t.Fatal("the coders' socket signed under off")
	}
	if len(f.requests()) != 0 {
		t.Fatal("a refused socket asked")
	}
	f.answer("secret")
	if err := signs(f.dial(t, s, KindShell), f.pub); err != nil {
		t.Fatalf("the shell did not sign: %v", err)
	}
	if err := signs(f.dial(t, s, KindShell), f.pub); err != nil {
		t.Fatalf("the shell did not sign again: %v", err)
	}
	if err := signs(f.dial(t, s, KindCoder), f.pub); err == nil {
		t.Fatal("the coders' socket signed under off with the key open")
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("an open key under allow or off asked, %d questions", n)
	}
	f.set.set(PolicySettingKey(KindCoder), string(Ask))
	s.Apply()
	f.answer("")
	if err := signs(f.dial(t, s, KindCoder), f.pub); err != nil {
		t.Fatalf("the coders switched to ask did not sign: %v", err)
	}
	if r := f.requests(); len(r) != 2 || r[1].Unlock || r[1].Kind != KindCoder {
		t.Fatalf("the coders switched to ask asked %+v", r)
	}
}

// A key that went into an agent with confirmation serves a kind switched to
// allow, the cockpit confirms it. One that went in without leaves its agent
// at once when the kind stops allowing, the others keep theirs, and its next
// signature opens the key again.
func TestLeavingAllowLocksThatKind(t *testing.T) {
	f, s := newFixture(t)
	f.answer("secret")
	if err := signs(f.dial(t, s, KindUI), f.pub); err != nil {
		t.Fatal(err)
	}
	f.set.set(PolicySettingKey(KindShell), string(Allow))
	s.Apply()
	if err := signs(f.dial(t, s, KindShell), f.pub); err != nil {
		t.Fatalf("the shells switched to allow did not sign: %v", err)
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("the shells switched to allow asked, %d questions", n)
	}
	f.set.set(PolicySettingKey(KindUI), string(Ask))
	s.Apply()
	if keys := holding(t, s, KindUI); len(keys) != 0 {
		t.Fatalf("the UI's agent kept %d keys", len(keys))
	}
	if keys := holding(t, s, KindShell); len(keys) != 1 {
		t.Fatal("the shells' agent lost its key")
	}
	f.answer("secret")
	if err := signs(f.dial(t, s, KindUI), f.pub); err != nil {
		t.Fatal(err)
	}
	if r := f.requests(); len(r) != 2 || !r[1].Unlock || r[1].Kind != KindUI {
		t.Fatalf("the UI's signature after the change asked %+v", r)
	}
}

// Forget takes the key out of every agent, so does the end of its time, also
// a time set shorter since.
func TestForgetAndTheTimeEndTheKey(t *testing.T) {
	f, s := newFixture(t)
	fp := ssh.FingerprintSHA256(f.pub)
	f.answer("secret")
	if err := signs(f.dial(t, s, KindUI), f.pub); err != nil {
		t.Fatal(err)
	}
	if !s.Forget(fp) {
		t.Fatal("an open key was not forgotten")
	}
	if s.Forget(fp) {
		t.Fatal("a closed key was forgotten")
	}
	for _, k := range Kinds {
		if keys := holding(t, s, k); len(keys) != 0 {
			t.Fatalf("the %s agent kept the forgotten key", k)
		}
	}
	f.answer("secret")
	if err := signs(f.dial(t, s, KindUI), f.pub); err != nil {
		t.Fatal(err)
	}
	if r := f.requests(); len(r) != 2 || !r[1].Unlock {
		t.Fatalf("the forgotten key did not ask again: %+v", r)
	}
	f.set.set(TTLSettingKey, "1")
	later := time.Now().Add(2 * time.Minute)
	s.now = func() time.Time { return later }
	s.Apply()
	for _, k := range Kinds {
		if keys := holding(t, s, k); len(keys) != 0 {
			t.Fatalf("the %s agent kept a key past its time", k)
		}
	}
	for _, k := range s.Status() {
		if k.Open {
			t.Fatal("a key past its time reads open")
		}
	}
}

// A restart keeps the sockets of the kinds where they were, and locks every
// key: the agents of the process before forget their keys at the start of
// the next one and end on their own once that process is gone.
func TestARestartKeepsTheSocketsAndLocksTheKeys(t *testing.T) {
	f, s := newFixture(t)
	f.answer("secret")
	if err := signs(f.dial(t, s, KindShell), f.pub); err != nil {
		t.Fatal(err)
	}
	var old []string
	s.mu.Lock()
	for _, kd := range s.kinds {
		if kd.agent != nil {
			old = append(old, kd.agent.path)
			_ = kd.agent.stdin.Close()
			kd.agent = nil
		}
	}
	listeners := s.listeners
	s.listeners = map[Kind]net.Listener{}
	s.mu.Unlock()
	for _, ln := range listeners {
		_ = ln.Close()
	}
	shell := s.Socket(KindShell)
	next := f.start(t)
	if next.Socket(KindShell) != shell {
		t.Fatalf("the shells' socket moved to %s", next.Socket(KindShell))
	}
	for _, path := range old {
		if c, err := net.Dial("unix", path); err == nil {
			keys, _ := xagent.NewClient(c).List()
			_ = c.Close()
			if len(keys) != 0 {
				t.Fatalf("an agent of the process before still holds %d keys", len(keys))
			}
		}
	}
	for _, k := range next.Status() {
		if k.Open {
			t.Fatal("a key reads open after the restart")
		}
	}
	f.answer("secret")
	if err := signs(dialAt(t, shell), f.pub); err != nil {
		t.Fatalf("the shells' socket does not sign after the restart: %v", err)
	}
	if r := f.requests(); len(r) != 2 || !r[1].Unlock {
		t.Fatalf("the restart did not lock the key: %+v", r)
	}
	deadline := time.Now().Add(20 * time.Second)
	for _, path := range old {
		for {
			if _, err := os.Stat(path); os.IsNotExist(err) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("the agent at %s outlived its process", path)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
}

// Several callers on one kind's socket: the question names every one of
// them, the agent cannot tell which of them signs.
func TestTheQuestionNamesEveryCallerOfTheKind(t *testing.T) {
	f, s := newFixture(t)
	f.answer("secret")
	if err := signs(f.dial(t, s, KindShell), f.pub); err != nil {
		t.Fatal(err)
	}
	hold := exec.Command(os.Args[0], holdArg)
	hold.Env = append(os.Environ(), "SSH_AUTH_SOCK="+s.Socket(KindShell))
	stdin, err := hold.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := hold.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := hold.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = stdin.Close()
		_ = hold.Wait()
	})
	if line, err := bufio.NewReader(stdout).ReadString('\n'); err != nil || line != "held\n" {
		t.Fatalf("the second caller reads %q, %v", line, err)
	}
	f.answer("")
	if err := signs(f.dial(t, s, KindShell), f.pub); err != nil {
		t.Fatal(err)
	}
	r := f.requests()[1]
	if len(r.Callers) != 2 {
		t.Fatalf("the question names %d callers: %+v", len(r.Callers), r.Callers)
	}
	if !slices.Contains(r.Callers[1].Command, holdArg) || slices.Contains(r.Callers[0].Command, holdArg) {
		t.Fatalf("the callers read %+v", r.Callers)
	}
}

// The keys are the cockpit's to add, remove and lock: a caller is refused.
// A signature that reaches an agent around the relay is refused at its
// confirmation, nobody is asked.
func TestTheRelayKeepsTheAgentsToTheCockpit(t *testing.T) {
	f, s := newFixture(t)
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	a := f.dial(t, s, KindShell)
	if err := a.Add(xagent.AddedKey{PrivateKey: priv}); err == nil {
		t.Fatal("a caller added a key")
	}
	if err := a.RemoveAll(); err == nil {
		t.Fatal("a caller removed the keys")
	}
	if err := a.Lock([]byte("x")); err == nil {
		t.Fatal("a caller locked the agent")
	}
	f.answer("secret")
	if err := signs(a, f.pub); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	private := s.kinds[KindShell].agent.path
	s.mu.Unlock()
	if err := signs(dialAt(t, private), f.pub); err == nil {
		t.Fatal("a signature around the relay went through")
	}
	if n := len(f.requests()); n != 1 {
		t.Fatalf("a signature around the relay asked, %d questions", n)
	}
}

// A caller that hangs up while its question stands takes the question down.
func TestAHungUpCallerEndsItsQuestion(t *testing.T) {
	f, s := newFixture(t)
	c, err := net.Dial("unix", s.Socket(KindShell))
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = xagent.NewClient(c).Sign(f.pub, []byte("challenge")) }()
	waitFor(t, "the question stands", func() bool { return len(f.requests()) == 1 })
	_ = c.Close()
	waitFor(t, "the question ends", func() bool {
		f.mu.Lock()
		defer f.mu.Unlock()
		return f.ended == 1
	})
}

// A question nobody answers ends with its window and the signature fails.
func TestAQuestionNobodyAnswersEndsWithItsWindow(t *testing.T) {
	f, s := newFixture(t)
	askWait = 300 * time.Millisecond
	t.Cleanup(func() { askWait = 2 * time.Minute })
	if err := signs(f.dial(t, s, KindShell), f.pub); err == nil {
		t.Fatal("a question nobody answered signed")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ended != 1 {
		t.Fatalf("%d questions ended with the window", f.ended)
	}
}

// A host agent found at the start wins, except one of the cockpit's own
// sockets, which a cockpit started from its own terminal inherits.
func TestAnExternalAgentWins(t *testing.T) {
	requireOpenSSH(t)
	state := t.TempDir()
	host := New(state, t.TempDir(), "/run/user/1000/agent.sock", func(string) string { return "" })
	if host.Active() || host.Socket(KindShell) != "" || host.External() != "/run/user/1000/agent.sock" {
		t.Fatal("the host's agent did not win")
	}
	own := New(state, t.TempDir(), filepath.Join(socketDir(state), "shell"), func(string) string { return "" })
	if !own.Active() || own.External() != "" {
		t.Fatal("the cockpit's own socket counted as a host agent")
	}
}

// Without OpenSSH's tools the cockpit runs no agent and hands out no socket.
func TestMissingToolsTurnTheAgentOff(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s := New(t.TempDir(), t.TempDir(), "", func(string) string { return "" })
	s.Start()
	if s.Active() || s.Socket(KindShell) != "" {
		t.Fatal("an agent without ssh-agent is active")
	}
}

// The directory is the user's alone and the sockets too. A link in the
// directory's place is not followed, the cockpit then runs no agent.
func TestSocketsAreTheUsersAlone(t *testing.T) {
	_, s := newFixture(t)
	for _, dir := range []string{s.dir, filepath.Join(s.dir, agentsDirName)} {
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s reads %v, %v", dir, info.Mode(), err)
		}
	}
	for _, k := range Kinds {
		info, err := os.Stat(s.Socket(k))
		if err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("the %s socket reads %v, %v", k, info.Mode(), err)
		}
	}
	state, err := os.MkdirTemp("", "dcsl")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(state) })
	if err := os.Symlink(t.TempDir(), filepath.Join(state, socketDirName)); err != nil {
		t.Fatal(err)
	}
	linked := New(state, t.TempDir(), "", func(string) string { return "" })
	linked.Start()
	if linked.Active() || linked.Socket(KindShell) != "" {
		t.Fatal("the agent took a link as its directory")
	}
}

// A state path too long for a socket moves the sockets to a directory in the
// temp directory, a path everybody can work out. A link or a directory that
// stood open there before the cockpit leaves the agent off, one the cockpit
// made itself is taken.
func TestTheFallbackDirectoryIsTakenOnlyAsTheCockpitLeftIt(t *testing.T) {
	requireOpenSSH(t)
	state := filepath.Join(t.TempDir(), strings.Repeat("s", maxSocketPath))
	dir := socketDir(state)
	if !strings.HasPrefix(dir, os.TempDir()) {
		t.Fatalf("the long state path keeps its sockets at %s", dir)
	}
	_ = os.RemoveAll(dir)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	start := func() *Service {
		s := New(state, t.TempDir(), "", func(string) string { return "" })
		s.Start()
		t.Cleanup(s.stop)
		return s
	}
	if err := os.Symlink(t.TempDir(), dir); err != nil {
		t.Fatal(err)
	}
	if s := start(); s.Active() || s.Socket(KindShell) != "" {
		t.Fatal("the agent took a link at the fallback")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	if s := start(); s.Active() || s.Socket(KindShell) != "" {
		t.Fatal("the agent took a directory at the fallback that stood open")
	}
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if s := start(); !s.Active() || s.Socket(KindShell) != filepath.Join(dir, string(KindShell)) {
		t.Fatal("the agent did not make the fallback its own")
	}
	if s := start(); !s.Active() {
		t.Fatal("the agent did not take the fallback it left behind")
	}
}

func waitFor(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: timed out", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
