//go:build linux

package sshagent

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/marein/dev-cockpit/internal/proctree"
	"golang.org/x/sys/unix"
)

const parentProbeEnv = "DC_SSH_AGENT_TEST_PARENT"

// The test binary stands in as a child of the started agent before TestMain
// runs: it reports whether it is dumpable itself, whether it still reads the
// parent's command line, which every process list shows, and whether it reads
// the parent's environment, which Yama leaves to every process of the same
// user, so only the dumpable flag refuses it.
func init() {
	if os.Getenv(parentProbeEnv) == "" {
		return
	}
	parent := os.Getppid()
	dumpable, _ := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0)
	environ := "read"
	if _, err := os.ReadFile(fmt.Sprintf("/proc/%d/environ", parent)); err != nil {
		environ = "refused"
	}
	fmt.Printf("dumpable=%d cmdline=%t environ=%s\n", dumpable, len(proctree.Capture().Cmdline(parent)) > 0, environ)
	os.Exit(0)
}

// A started agent leaves no core and no memory the user's other processes may
// read, while the children it starts are dumpable again after their exec and
// still read its command line. The same child reads the environment of the
// process before, and only a process allowed to trace every other one, root
// outside a container, still reads it after.
func TestStartClosesTheProcessToTheUsersOtherProcesses(t *testing.T) {
	if err := unix.Prctl(unix.PR_SET_DUMPABLE, 1, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	before := probeParent(t)
	if before != "dumpable=1 cmdline=true environ=read" {
		t.Fatalf("before the start the child reads %q", before)
	}
	_, _ = newFixture(t)
	if v, err := unix.PrctlRetInt(unix.PR_GET_DUMPABLE, 0, 0, 0, 0); err != nil || v != 0 {
		t.Fatalf("the started agent's process reads dumpable %d: %v", v, err)
	}
	want := "dumpable=1 cmdline=true environ=refused"
	if tracesEveryone(t) {
		want = before
	}
	if got := probeParent(t); got != want {
		t.Fatalf("the child reads %q, want %q", got, want)
	}
}

func probeParent(t *testing.T) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), parentProbeEnv+"=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the child failed: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// tracesEveryone answers whether this process holds CAP_SYS_PTRACE, which
// passes over every dumpable flag.
func tracesEveryone(t *testing.T) bool {
	t.Helper()
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		if hex, ok := strings.CutPrefix(line, "CapEff:"); ok {
			caps, err := strconv.ParseUint(strings.TrimSpace(hex), 16, 64)
			if err != nil {
				t.Fatal(err)
			}
			return caps&(1<<unix.CAP_SYS_PTRACE) != 0
		}
	}
	t.Fatal("no CapEff in /proc/self/status")
	return false
}
