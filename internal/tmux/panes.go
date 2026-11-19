package tmux

import (
	"context"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

const (
	paneSessionsWait = 2 * time.Second
	maxPaneSessions  = 1 << 20
)

// PaneSessions answers the process of every pane, in every window of every
// session, with the session it belongs to. It answers nil where no server
// runs or tmux did not answer in time.
func (c *Client) PaneSessions() map[int]string {
	ctx, cancel := context.WithTimeout(context.Background(), paneSessionsWait)
	defer cancel()
	cmd := exec.CommandContext(ctx, "tmux", "list-panes", "-a", "-F", "#{pane_pid}\t#{session_name}")
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return nil
	}
	data, _ := io.ReadAll(io.LimitReader(out, maxPaneSessions))
	_, _ = io.Copy(io.Discard, out)
	if cmd.Wait() != nil {
		return nil
	}
	return parsePaneSessions(string(data))
}

func parsePaneSessions(out string) map[int]string {
	sessions := map[int]string{}
	for _, line := range strings.Split(out, "\n") {
		pid, name, ok := strings.Cut(line, "\t")
		if !ok || name == "" {
			continue
		}
		if n, err := strconv.Atoi(pid); err == nil && n > 0 {
			sessions[n] = name
		}
	}
	return sessions
}
