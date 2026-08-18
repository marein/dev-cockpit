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

	"github.com/marein/dev-cockpit/internal/clirun"
)

// PostScriptTimeout bounds a worktree's post script. The create request waits
// for it.
const PostScriptTimeout = 15 * time.Second

// postScriptOutputCap keeps the end of what a script writes, which is where a
// failure says why.
const postScriptOutputCap = 64 << 10

// postScriptWaitDelay bounds the wait for pipes a child of the script still
// holds once the script itself is gone.
const postScriptWaitDelay = 2 * time.Second

// PostScriptFile is the name of the worktree post script in the state
// directory, one script for every project.
const PostScriptFile = "worktree-post-script"

// PostScriptEnv names the variables a post script gets on top of the
// cockpit's environment, in the order of PostScriptVars.
var PostScriptEnv = []string{"DC_SOURCE_PROJECT", "DC_SOURCE_DIR", "DC_WORKTREE_DIR", "DC_WORKTREE_PROJECT", "DC_BRANCH"}

// PostScriptVars are the values of PostScriptEnv for one new worktree.
type PostScriptVars struct {
	Project, SourceDir, WorktreeDir, WorktreeProject, Branch string
}

func (v PostScriptVars) env() []string {
	values := []string{v.Project, v.SourceDir, v.WorktreeDir, v.WorktreeProject, v.Branch}
	env := make([]string, len(PostScriptEnv))
	for i, name := range PostScriptEnv {
		env[i] = name + "=" + values[i]
	}
	// TODO(v2.0.0): drop DC_PROJECT, released scripts still read it, new ones read DC_SOURCE_PROJECT.
	return append(env, "DC_PROJECT="+v.Project)
}

// PostScriptSummary is one line naming where a post script runs, its time
// bound and its variables.
func PostScriptSummary() string {
	return fmt.Sprintf("Runs in the new worktree, max %s, env: %s.", PostScriptTimeout, strings.Join(PostScriptEnv, " "))
}

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
// front, its first line picks the interpreter. vars join the cockpit's own
// environment. It answers nil when there is no script. A script that lost its
// executable bit, as a restore by hand may leave it, gets it back first.
func RunPostScript(ctx context.Context, path, dir string, vars PostScriptVars) *PostScriptRun {
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
	cmd.Env = append(os.Environ(), vars.env()...)
	out := &tailBuffer{max: postScriptOutputCap}
	cmd.Stdout = out
	cmd.Stderr = out
	cmd.WaitDelay = postScriptWaitDelay
	clirun.KillsWholeGroup(cmd)
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
