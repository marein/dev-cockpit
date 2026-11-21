package clirun

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestDiesWithParentStarter is not a test of its own: run as a child of the
// test below, it starts a sleep that dies with it and prints its pid.
func TestDiesWithParentStarter(t *testing.T) {
	if os.Getenv("CLIRUN_DEATH_STARTER") == "" {
		t.Skip("only runs as the starter of TestAChildDiesWithItsStarter")
	}
	cmd := exec.Command("sleep", "60")
	_ = DiesWithParent(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	fmt.Println(cmd.Process.Pid)
	time.Sleep(time.Minute)
}

// A starter killed outright reaches no deferred stop: the kernel ends what it
// started.
func TestAChildDiesWithItsStarter(t *testing.T) {
	starter := exec.Command(os.Args[0], "-test.run=^TestDiesWithParentStarter$")
	starter.Env = append(os.Environ(), "CLIRUN_DEATH_STARTER=1")
	out, err := starter.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := starter.Start(); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(out).ReadString('\n')
	if err != nil {
		t.Fatalf("the starter printed no pid: %v", err)
	}
	child, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("the starter printed %q", line)
	}
	if err := syscall.Kill(child, 0); err != nil {
		t.Fatalf("the child is not running: %v", err)
	}
	_ = starter.Process.Kill()
	_ = starter.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for alive(child) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(child, syscall.SIGKILL)
			t.Fatal("the child outlived its starter")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A child that ends on its own keeps its output and exit code, and once it
// was waited for, the release lets its guard go, where there is one.
func TestAWaitedChildKeepsItsOutputAndLetsItsGuardGo(t *testing.T) {
	cmd := exec.Command("sh", "-c", "read line; echo \"got $line\"; exit 7")
	cmd.Stdin = strings.NewReader("hello\n")
	release := DiesWithParent(cmd)
	guarded := cmd.SysProcAttr.Pgid != 0
	if !guarded && cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Fatalf("the child is neither guarded nor tied to its starter: %+v", cmd.SysProcAttr)
	}
	if guarded && len(guards()) != 1 {
		t.Fatalf("one guard should stand, found %v", guards())
	}
	out, err := cmd.Output()
	release()
	if string(out) != "got hello\n" || cmd.ProcessState.ExitCode() != 7 {
		t.Fatalf("the child answered %q with %v", out, err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for len(guards()) > 0 {
		if time.Now().After(deadline) {
			t.Fatalf("guards outlived their release: %v", guards())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// guards are the living guards this process started.
func guards() []int {
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || !alive(pid) {
			continue
		}
		stat, _ := os.ReadFile("/proc/" + e.Name() + "/stat")
		fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		argv, _ := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		args := strings.Split(string(argv), "\x00")
		if len(fields) > 1 && fields[1] == strconv.Itoa(os.Getpid()) && len(args) > 1 && args[1] == GuardCommand[0] {
			pids = append(pids, pid)
		}
	}
	return pids
}

// alive reads the process state: a child reparented to init is gone once it
// is a zombie, whoever reaps it.
func alive(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) > 0 && fields[0] != "Z" && fields[0] != "X"
}
