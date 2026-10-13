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
