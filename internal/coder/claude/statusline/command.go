package statusline

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/marein/dev-cockpit/internal/clirun"
)

// commandTimeout is the ceiling of a command of the user's, the same as git's
// and the usage call's: the sources run side by side, so a command within it
// never makes the line later than a stalled repository already can.
const commandTimeout = 5 * time.Second

// commandOutputCap is how much of the output is kept. The first line is all
// the line shows, and no terminal row is this wide.
const commandOutputCap = 4096

// commandWaitDelay bounds the wait for a pipe something still holds once the
// command is gone. The whole process group is killed at the timeout, so this
// only covers a child that left the group on purpose, and it is bounded the
// way a worktree's post script is.
const commandWaitDelay = 2 * time.Second

// runCommand runs one command line of the user's and answers the first line it
// printed. It is split into argv and started without a shell, so nothing in
// the line is expanded or run as a second command. It gets claude's payload on
// stdin, the way claude hands it to a status line command of its own, and
// runs in the coder's folder. A command that fails, or does not end within
// the timeout, answers nothing and its entry leaves the line.
func runCommand(ctx context.Context, line string, stdin []byte, dir string) string {
	argv, err := clirun.SplitCommand(line)
	if err != nil || len(argv) == 0 {
		return ""
	}
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(stdin)
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		cmd.Dir = dir
	}
	out := &cappedOutput{limit: commandOutputCap}
	cmd.Stdout = out
	cmd.WaitDelay = commandWaitDelay
	clirun.KillsWholeGroup(cmd)
	if cmd.Run() != nil {
		return ""
	}
	first, _, _ := strings.Cut(out.buf.String(), "\n")
	return strings.TrimSuffix(first, "\r")
}

// cappedOutput keeps the first bytes of what a command writes and takes the
// rest without keeping it, so a command that writes on never blocks on a full
// pipe.
type cappedOutput struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedOutput) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(len(p), room)])
	}
	return len(p), nil
}
