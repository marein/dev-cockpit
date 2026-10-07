package clirun

import (
	"strings"
	"testing"
	"time"
)

func TestRunBoundedEndsAHangingCommandAndRefusesTooMuchOutput(t *testing.T) {
	if r := RunBounded(time.Second, 10, "sh", "-c", "printf 0123456789"); r.Err != nil || r.Stdout != "0123456789" {
		t.Fatalf("a command within its bounds: %q, %v", r.Stdout, r.Err)
	}
	if r := RunBounded(time.Second, 4, "sh", "-c", "printf 0123456789"); r.Err == nil || !strings.Contains(r.Err.Error(), "more than 4 bytes") || r.Stdout != "0123" {
		t.Fatalf("too much output: %q, %v", r.Stdout, r.Err)
	}
	start := time.Now()
	if r := RunBounded(100*time.Millisecond, 10, "sleep", "5"); r.Err == nil || !strings.Contains(r.Err.Error(), "did not answer within 100ms") {
		t.Fatalf("a hanging command: %v", r.Err)
	}
	if took := time.Since(start); took > 2*time.Second {
		t.Fatalf("the hanging command was ended after %s", took)
	}
}
