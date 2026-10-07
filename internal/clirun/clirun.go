// Package clirun executes external commands with structured error reporting.
package clirun

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Error captures a non-zero exit status from an external command.
type Error struct {
	Cmd    string
	Code   int
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return e.Stderr
	}
	return fmt.Sprintf("%s: exit %d", e.Cmd, e.Code)
}

func (e *Error) Unwrap() error { return e.Err }

// Result is the captured output of a command run.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Err      error
}

// Run executes a command and captures stdout/stderr.
func Run(name string, args ...string) Result {
	cmd := exec.Command(name, args...)
	var out, errBuf strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	err := cmd.Run()
	r := Result{Stdout: out.String(), Stderr: errBuf.String(), Err: err}
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	return r
}

// RunBounded is Run for a command that must neither hang nor flood the
// memory: it is killed after timeout, and output beyond maxOutput bytes on
// either stream fails it, since a cut answer would read as a whole one.
func RunBounded(timeout time.Duration, maxOutput int, name string, args ...string) Result {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	out, errOut := &cappedBuffer{max: maxOutput}, &cappedBuffer{max: maxOutput}
	cmd.Stdout, cmd.Stderr = out, errOut
	r := Result{Err: cmd.Run()}
	switch {
	case r.Err != nil && ctx.Err() != nil:
		r.Err = fmt.Errorf("%s did not answer within %s", name, timeout)
	case out.overflow || errOut.overflow:
		r.Err = fmt.Errorf("%s wrote more than %d bytes", name, maxOutput)
	}
	r.Stdout, r.Stderr = out.buf.String(), errOut.buf.String()
	if cmd.ProcessState != nil {
		r.ExitCode = cmd.ProcessState.ExitCode()
	}
	return r
}

// cappedBuffer keeps the first max bytes written to it and notes that more
// came, while it goes on taking writes, so the process never blocks on a full
// pipe.
type cappedBuffer struct {
	buf      bytes.Buffer
	max      int
	overflow bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	keep := p
	if room := c.max - c.buf.Len(); len(p) > room {
		keep = p[:max(room, 0)]
		c.overflow = true
	}
	c.buf.Write(keep)
	return len(p), nil
}

// Check runs a command and returns a typed Error on failure.
func Check(name string, args ...string) error {
	r := Run(name, args...)
	if r.Err == nil {
		return nil
	}
	if r.ExitCode != 0 {
		stderr := strings.TrimSpace(r.Stderr)
		if stderr == "" {
			stderr = fmt.Sprintf("command failed with exit code %d", r.ExitCode)
		}
		return &Error{Cmd: name, Code: r.ExitCode, Stderr: stderr, Err: r.Err}
	}
	return r.Err
}

// MissingTools returns the subset of the supplied names not on PATH.
func MissingTools(tools []string) []string {
	var missing []string
	for _, t := range tools {
		if _, err := exec.LookPath(t); err != nil {
			missing = append(missing, t)
		}
	}
	return missing
}

// ShellQuote wraps a string in POSIX-safe single quotes.
func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}
