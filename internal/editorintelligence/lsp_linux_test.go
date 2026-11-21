package editorintelligence

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestLSPStarter is not a test of its own: run as a child of the test below,
// it asks the fake server through the service and prints the server's pid.
func TestLSPStarter(t *testing.T) {
	if os.Getenv("LSP_DEATH_STARTER") == "" {
		t.Skip("only runs as the starter of TestALanguageServerDiesWithItsStarter")
	}
	installFakeLSP(t, "deaf", "gopls")
	s := newTestService(t)
	res, err := s.Definition(context.Background(), goRequest(t))
	if err != nil || !res.Available || len(res.Locations) != 1 {
		t.Fatalf("the server did not answer: %+v %v", res, err)
	}
	s.mu.Lock()
	for _, mc := range s.conns {
		fmt.Println(mc.conn.cmd.Process.Pid)
	}
	s.mu.Unlock()
	time.Sleep(time.Minute)
}

// A language server answers its starter over stdio and dies with it however
// it ends, even one that keeps running when its stdin closes: the starter
// reaches no shutdown when it is killed outright.
func TestALanguageServerDiesWithItsStarter(t *testing.T) {
	starter := exec.Command(os.Args[0], "-test.run=^TestLSPStarter$")
	starter.Env = append(os.Environ(), "LSP_DEATH_STARTER=1")
	out, err := starter.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := starter.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = starter.Process.Kill(); _ = starter.Wait() }()
	line, _ := bufio.NewReader(out).ReadString('\n')
	server, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		t.Fatalf("the starter printed %q", line)
	}
	if !running(server) {
		t.Fatal("the server is not running")
	}
	_ = starter.Process.Kill()
	_ = starter.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for running(server) {
		if time.Now().After(deadline) {
			_ = syscall.Kill(server, syscall.SIGKILL)
			t.Fatal("the language server outlived its starter")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// running reads the process state: a server reparented to init is gone once
// it is a zombie, whoever reaps it.
func running(pid int) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
	return len(fields) > 0 && fields[0] != "Z" && fields[0] != "X"
}
