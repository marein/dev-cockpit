//go:build linux

package term

import (
	"bytes"
	"testing"
	"time"
)

// TestAgentRoundtrip runs a real agent over a PTY and unix socket, sends input,
// and checks it streams back — exercising openPTY, the framing protocol, input
// handling, output fan-out, and discovery end to end.
func TestAgentRoundtrip(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	go func() {
		_ = RunAgent(AgentConfig{Provider: "test", Key: "k1", Workdir: "/tmp", Command: "exec cat"})
	}()

	c, err := NewClient("test")
	if err != nil {
		t.Fatal(err)
	}

	var stream *Stream
	deadline := time.Now().Add(5 * time.Second)
	for {
		stream, err = c.OpenStream("k1", 80, 24)
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("agent never accepted connections: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	defer stream.Close()

	if err := c.Send("k1", []InFrame{TextFrame("hello\n")}); err != nil {
		t.Fatal(err)
	}

	var got bytes.Buffer
	timeout := time.After(5 * time.Second)
	for !bytes.Contains(got.Bytes(), []byte("hello")) {
		select {
		case chunk, ok := <-stream.Output():
			if !ok {
				t.Fatalf("stream closed; got %q", got.String())
			}
			got.Write(chunk)
		case <-timeout:
			t.Fatalf("timed out; got %q", got.String())
		}
	}

	panes, err := c.Discover()
	if err != nil {
		t.Fatal(err)
	}
	if len(panes) != 1 || panes[0].Name != "k1" {
		t.Fatalf("discover = %+v, want one pane named k1", panes)
	}

	if err := c.Kill("k1"); err != nil {
		t.Fatalf("kill: %v", err)
	}
}

// TestAgentStopSIGTERMIgnorer verifies Kill terminates a program that ignores
// SIGTERM (an interactive shell), which the agent must achieve via SIGHUP.
func TestAgentStopSIGTERMIgnorer(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	go func() {
		_ = RunAgent(AgentConfig{Provider: "test", Key: "k2", Workdir: "/tmp", Command: "exec bash --norc -i"})
	}()
	c, err := NewClient("test")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if s, err := c.OpenStream("k2", 80, 24); err == nil {
			s.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("agent never came up")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := c.Kill("k2"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	deadline = time.Now().Add(8 * time.Second)
	for {
		panes, _ := c.Discover()
		if len(panes) == 0 {
			return // agent exited and cleaned up
		}
		if time.Now().After(deadline) {
			t.Fatalf("session still present after Kill: %+v", panes)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
