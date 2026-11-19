package sshagent

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
	xagent "golang.org/x/crypto/ssh/agent"
)

const (
	chainEnv = "DCSA_CHAIN"
	keyEnv   = "DCSA_KEY"
)

// The test binary stands in for git and ssh too: each process of a chain
// starts the next argument list of DCSA_CHAIN under the name that list gives,
// and the last one moves to / and signs with the key at DCSA_KEY through the
// socket in its environment.
func init() {
	raw, ok := os.LookupEnv(chainEnv)
	if !ok {
		return
	}
	var chain [][]string
	if json.Unmarshal([]byte(raw), &chain) != nil {
		os.Exit(2)
	}
	if len(chain) > 0 {
		self, err := os.Executable()
		if err != nil {
			os.Exit(2)
		}
		rest, _ := json.Marshal(chain[1:])
		next := &exec.Cmd{Path: self, Args: chain[0], Env: append(os.Environ(), chainEnv+"="+string(rest)), Stderr: os.Stderr}
		if next.Run() != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}
	if os.Chdir("/") != nil {
		os.Exit(2)
	}
	data, err := os.ReadFile(os.Getenv(keyEnv))
	if err != nil {
		os.Exit(2)
	}
	pub, _, _, _, err := ssh.ParseAuthorizedKey(data)
	if err != nil {
		os.Exit(2)
	}
	c, err := net.Dial("unix", os.Getenv("SSH_AUTH_SOCK"))
	if err != nil {
		os.Exit(2)
	}
	if signs(xagent.NewClient(c), pub) != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

// signFrom runs a chain of processes in dir whose last one signs through a
// kind's socket.
func signFrom(t *testing.T, f *fixture, s *Service, k Kind, dir string, chain ...[]string) error {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	rest, err := json.Marshal(chain[1:])
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, self)
	cmd.Args = chain[0]
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"SSH_AUTH_SOCK="+s.Socket(k),
		chainEnv+"="+string(rest),
		keyEnv+"="+filepath.Join(f.sshDir, "id_ed25519.pub"),
	)
	return cmd.Run()
}

func realDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

// A question names the topmost git process above the one that connected,
// past a program between them, with git's directory: a pull reads as the
// pull and not as the fetch it runs, the passphrase's question as the
// confirmation's.
func TestTheQuestionNamesTheGitCommandAboveTheCaller(t *testing.T) {
	f, s := newFixture(t)
	repo := realDir(t)
	f.answer("secret")
	pull := []string{"git", "pull", "origin", "main"}
	if err := signFrom(t, f, s, KindShell, repo, pull, []string{"git", "fetch", "origin"},
		[]string{"sh", "-c", `ssh "$@"`}, []string{"ssh", "git@example.com", "git-upload-pack 'x.git'"}); err != nil {
		t.Fatalf("the pull did not sign: %v", err)
	}
	f.answer("")
	push := []string{"git", "push", "origin", "main"}
	if err := signFrom(t, f, s, KindShell, repo, push, []string{"ssh", "git@example.com", "git-receive-pack 'x.git'"}); err != nil {
		t.Fatalf("the push did not sign: %v", err)
	}
	asked := f.requests()
	if len(asked) != 2 || !asked[0].Unlock || asked[1].Unlock {
		t.Fatalf("the signatures asked %+v", asked)
	}
	for i, want := range [][]string{pull, push} {
		got := asked[i].Callers
		if len(got) != 1 || !slices.Equal(got[0].Command, want) || got[0].Dir != repo || got[0].Transfer != "" {
			t.Fatalf("question %d names %+v, want %v in %s", i, got, want, repo)
		}
		if chain := got[0].Chain; len(chain) < 3 || chain[0] == os.Getpid() || !slices.Contains(chain, os.Getpid()) {
			t.Fatalf("question %d reads the chain %v, it does not climb from the signer to this test %d", i, chain, os.Getpid())
		}
	}
}

// Without git above it, an ssh line stays as it is with the transfer it
// carries, a line that carries none with none.
func TestAnSSHLineWithoutGitNamesItsTransfer(t *testing.T) {
	f, s := newFixture(t)
	f.answer("secret", "", "")
	for i, c := range []struct {
		argv []string
		want Transfer
	}{
		{[]string{"ssh", "-o", "SendEnv=GIT_PROTOCOL", "git@example.com", "git-receive-pack 'x.git'"}, TransferPush},
		{[]string{"/usr/bin/ssh", "-p", "2222", "git@example.com", "git-upload-pack '/srv/x.git'"}, TransferFetch},
		{[]string{"ssh", "git@example.com", "ls"}, ""},
	} {
		if err := signFrom(t, f, s, KindShell, realDir(t), c.argv); err != nil {
			t.Fatalf("%v did not sign: %v", c.argv, err)
		}
		got := f.requests()[i].Callers
		if len(got) != 1 || !slices.Equal(got[0].Command, c.argv) || got[0].Dir != "/" || got[0].Transfer != c.want {
			t.Fatalf("%v reads %+v", c.argv, got)
		}
	}
}

// The transfer is read off the remote command git hands ssh at the end of
// its line, also where ps hands the line over in pieces, and only off ssh.
func TestTransferOf(t *testing.T) {
	for _, c := range []struct {
		argv []string
		want Transfer
	}{
		{[]string{"ssh", "-o", "SendEnv=GIT_PROTOCOL", "git@github.com", "git-receive-pack 'me/x.git'"}, TransferPush},
		{[]string{"ssh", "git@github.com", "git-upload-pack 'me/x.git'"}, TransferFetch},
		{[]string{"ssh", "-o", "SendEnv=GIT_PROTOCOL", "git@github.com", "git-receive-pack", "'me/x.git'"}, TransferPush},
		{[]string{"ssh", "git@github.com", "git-upload-pack", "'/srv/my", "repo.git'"}, TransferFetch},
		{[]string{"/usr/local/bin/ssh", "git@github.com", "git-upload-pack '/srv/x.git'"}, TransferFetch},
		{[]string{"ssh", "git@github.com", "git-upload-archive 'x.git'"}, ""},
		{[]string{"ssh", "git@github.com", "git-receive-pack x.git"}, ""},
		{[]string{"ssh", "git@github.com", "git-receive-pack 'x.git'", "ls"}, ""},
		{[]string{"ssh", "git@github.com"}, ""},
		{[]string{"scp", "x", "git@github.com:git-receive-pack 'x.git'"}, ""},
		{[]string{"ssh-keygen", "-Y", "sign", "git-receive-pack 'x.git'"}, ""},
		{[]string{"ssh"}, ""},
		{nil, ""},
	} {
		if got := transferOf(c.argv); got != c.want {
			t.Errorf("%q reads %q, want %q", c.argv, got, c.want)
		}
	}
}
