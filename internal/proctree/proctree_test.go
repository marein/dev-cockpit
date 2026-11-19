package proctree

import (
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The process on the other end of a unix socket is the one that connected,
// as the kernel recorded it.
func TestPeerPIDNamesTheProcessThatConnected(t *testing.T) {
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("this platform does not say who connected")
	}
	dir, err := os.MkdirTemp("", "dcpt")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	ln, err := net.Listen("unix", filepath.Join(dir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, _ := ln.Accept()
		accepted <- c
	}()
	c, err := net.Dial("unix", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	server := <-accepted
	if server == nil {
		t.Fatal("nothing was accepted")
	}
	defer server.Close()
	if pid := PeerPID(server); pid != os.Getpid() {
		t.Fatalf("the peer reads %d, want %d", pid, os.Getpid())
	}
}

// The ancestors of a process start at its parent and climb to the root of
// the table, each once.
func TestAncestorsClimbFromTheParent(t *testing.T) {
	up := Capture().Ancestors(os.Getpid())
	if len(up) == 0 || up[0] != os.Getppid() {
		t.Fatalf("the ancestors of %d read %v, want %d first", os.Getpid(), up, os.Getppid())
	}
	seen := map[int]bool{os.Getpid(): true}
	for _, pid := range up {
		if pid <= 0 || seen[pid] {
			t.Fatalf("the ancestors read %v", up)
		}
		seen[pid] = true
	}
}
