package opencode

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestServeStarter is not a test of its own: run as a child of the test
// below, it boots a server that dies with it and prints its port.
func TestServeStarter(t *testing.T) {
	if os.Getenv("OPENCODE_SERVE_STARTER") == "" {
		t.Skip("only runs as the starter of TestTheServerDiesWithItsStarter")
	}
	s, err := startServe(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(strings.TrimPrefix(s.base, "http://127.0.0.1:"))
	time.Sleep(time.Minute)
}

// A starter that ends without its deferred stop, a helper killed outright or
// a cockpit restarting while it clears what a compaction left, leaves no
// server and no open port behind. The real opencode serves where one is
// installed, the test binary stands in elsewhere.
func TestTheServerDiesWithItsStarter(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		dir := t.TempDir()
		script := fmt.Sprintf("#!/bin/sh\nOPENCODE_FAKE_SERVE=1 exec %q \"$@\"\n", os.Args[0])
		if err := os.WriteFile(filepath.Join(dir, "opencode"), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	for _, end := range []syscall.Signal{syscall.SIGKILL, syscall.SIGTERM} {
		t.Run(end.String(), func(t *testing.T) {
			starter := exec.Command(os.Args[0], "-test.run=^TestServeStarter$")
			starter.Env = append(os.Environ(), "OPENCODE_SERVE_STARTER=1")
			out, err := starter.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := starter.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() { _ = starter.Process.Kill(); _ = starter.Wait() }()
			line, _ := bufio.NewReader(out).ReadString('\n')
			port := strings.TrimSpace(line)
			if _, err := strconv.Atoi(port); err != nil {
				t.Fatalf("the starter printed %q", line)
			}
			servers := serversOn(port)
			if len(servers) == 0 {
				t.Fatal("no server runs on the port")
			}
			_ = starter.Process.Signal(end)
			_ = starter.Wait()
			deadline := time.Now().Add(5 * time.Second)
			for listening(port) || len(serversOn(port)) > 0 {
				if time.Now().After(deadline) {
					for _, pid := range servers {
						_ = syscall.Kill(pid, syscall.SIGKILL)
					}
					t.Fatalf("the server on %s outlived its starter", port)
				}
				time.Sleep(20 * time.Millisecond)
			}
		})
	}
}

func listening(port string) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, time.Second)
	if err == nil {
		conn.Close()
	}
	return err == nil
}

// serversOn are the living processes started as a server on the port.
func serversOn(port string) []int {
	entries, _ := os.ReadDir("/proc")
	var pids []int
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue
		}
		argv, _ := os.ReadFile("/proc/" + e.Name() + "/cmdline")
		stat, _ := os.ReadFile("/proc/" + e.Name() + "/stat")
		fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if len(fields) > 0 && fields[0] != "Z" && strings.Contains(string(argv), "serve\x00--port\x00"+port+"\x00") {
			pids = append(pids, pid)
		}
	}
	return pids
}
