package project

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/git"
)

// PostScriptTimeout bounds a worktree's post script. It is the default budget
// of a compose up, the other configured command that installs and builds.
const PostScriptTimeout = 10 * time.Minute

// postScriptOutputCap keeps the end of what a script writes, which is where a
// failure says why.
const postScriptOutputCap = 64 << 10

// postScriptWaitDelay bounds the wait for pipes a child of the script still
// holds once the script itself is gone.
const postScriptWaitDelay = 2 * time.Second

// PostScriptFile is the name of the worktree post script in the state
// directory, one script for every project.
const PostScriptFile = "worktree-post-script"

// ErrNoInterpreter refuses a script the kernel could not run directly.
var ErrNoInterpreter = errors.New("the first line has to name the interpreter, like #!/bin/bash")

// PostScriptRun is the outcome of one post script: stdout and stderr
// interleaved as written, and why it failed, empty when it exited 0.
type PostScriptRun struct {
	Output string
	Err    string
}

// ReadPostScript answers the script at path, empty when there is none.
func ReadPostScript(path string) string {
	data, _ := os.ReadFile(path)
	return string(data)
}

// NormalizePostScript answers content the way SavePostScript stores it, with
// LF line endings and a final newline, empty for content that removes the
// script, or why it is refused.
func NormalizePostScript(content string) (string, error) {
	content = strings.ReplaceAll(strings.ReplaceAll(content, "\r\n", "\n"), "\r", "\n")
	if strings.TrimSpace(content) == "" {
		return "", nil
	}
	if !strings.HasPrefix(content, "#!") {
		return "", ErrNoInterpreter
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	return content, nil
}

// SavePostScript stores content as the script at path, normalized and
// executable by the owner alone. Empty content removes the script.
func SavePostScript(path, content string) error {
	content, err := NormalizePostScript(content)
	if err != nil {
		return err
	}
	if content == "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), PostScriptFile+".tmp*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.WriteString(content)
	if err == nil {
		err = tmp.Chmod(0o700)
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// RunPostScript executes the script at path inside dir, without a shell in
// front, its first line picks the interpreter. env joins the cockpit's own
// environment. It answers nil when there is no script. A script that lost its
// executable bit, as a restore by hand may leave it, gets it back first.
func RunPostScript(ctx context.Context, path, dir string, env []string) *PostScriptRun {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	run := &PostScriptRun{}
	if err == nil && info.Mode().Perm()&0o100 == 0 {
		err = os.Chmod(path, 0o700)
	}
	if err != nil {
		run.Err = err.Error()
		return run
	}
	ctx, cancel := context.WithTimeout(ctx, PostScriptTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	out := &tailBuffer{max: postScriptOutputCap}
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = postScriptWaitDelay
	git.KillsWholeGroup(cmd)
	err = cmd.Run()
	run.Output = out.String()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		run.Err = fmt.Sprintf("stopped after %s", PostScriptTimeout)
	case err != nil && !errors.Is(err, exec.ErrWaitDelay):
		run.Err = err.Error()
	}
	return run
}

// tailBuffer keeps the last max bytes written to it.
type tailBuffer struct {
	max  int
	buf  []byte
	lost bool
}

func (t *tailBuffer) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if over := len(t.buf) - t.max; over > 0 {
		t.buf = append(t.buf[:0], t.buf[over:]...)
		t.lost = true
	}
	return len(p), nil
}

func (t *tailBuffer) String() string {
	if t.lost {
		return "…" + string(t.buf)
	}
	return string(t.buf)
}
