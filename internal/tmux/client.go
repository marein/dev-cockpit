// Package tmux is a thin domain client around the tmux CLI.
package tmux

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/local/dev-cockpit/internal/clirun"
)

// RequiredTools lists external binaries tmux relies on.
var RequiredTools = []string{"tmux"}

// Pane is one tmux session entry as reported by `tmux list-panes`.
type Pane struct {
	Name      string
	PID       string
	StartedAt string // unix epoch seconds, raw
	ShellName string // @dc_shell_name option; non-empty marks a shell session
}

// Size is a tmux pane dimension in cells.
type Size struct {
	Cols int
	Rows int
}

// Client wraps the tmux CLI.
type Client struct{}

// New returns a tmux Client.
func New() *Client { return &Client{} }

// Target returns the canonical pane target for a session.
func Target(name string) string { return name + ":0.0" }

// NewSession spawns a detached tmux session.
func (c *Client) NewSession(name, workdir, shellCmd string, env map[string]string) error {
	args := []string{"new-session", "-d", "-s", name, "-c", workdir}
	for _, kv := range sortedEnv(env) {
		args = append(args, "-e", kv)
	}
	args = append(args, "bash", "-lc", shellCmd)
	return clirun.Check("tmux", args...)
}

func sortedEnv(env map[string]string) []string {
	out := make([]string, 0, len(env))
	for k, v := range env {
		out = append(out, k+"="+v)
	}
	sort.Strings(out)
	return out
}

// SetHistoryLimit configures how much scrollback tmux keeps for snapshots.
func (c *Client) SetHistoryLimit(name string, historyLimit int) error {
	return clirun.Check("tmux", "set-option", "-t", name, "history-limit", strconv.Itoa(historyLimit))
}

// SetOption sets a tmux option (e.g. a user option "@name") on a session.
func (c *Client) SetOption(name, option, value string) error {
	return clirun.Check("tmux", "set-option", "-t", name, option, value)
}

// StopPipe detaches any inherited pipe-pane logger (migration cleanup).
func (c *Client) StopPipe(name string) error {
	return clirun.Check("tmux", "pipe-pane", "-t", Target(name))
}

// Rename changes the tmux session name.
func (c *Client) Rename(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	return clirun.Check("tmux", "rename-session", "-t", oldName, newName)
}

// Kill terminates a session.
func (c *Client) Kill(name string) error {
	return clirun.Check("tmux", "kill-session", "-t", name)
}

// SendKey sends one named key (e.g. "Enter", "Up").
func (c *Client) SendKey(name, key string) error {
	return clirun.Check("tmux", "send-keys", "-t", Target(name), key)
}

// SendLiteral sends literal text without key interpretation.
func (c *Client) SendLiteral(name, text string) error {
	if text == "" {
		return nil
	}
	return clirun.Check("tmux", "send-keys", "-t", Target(name), "-l", text)
}

// sendRawChunk bounds how many byte values go into one send-keys call so a long
// paste can't blow the argument list.
const sendRawChunk = 256

// SendRaw injects an exact byte sequence into a pane via send-keys -H (each byte
// as a hex key code). Unlike SendKey/SendLiteral it does no interpretation, so
// it carries whatever a browser terminal's onData emits verbatim: printable
// UTF-8 (including composed accents), control bytes, and escape sequences.
func (c *Client) SendRaw(name string, data []byte) error {
	for start := 0; start < len(data); start += sendRawChunk {
		end := start + sendRawChunk
		if end > len(data) {
			end = len(data)
		}
		args := make([]string, 0, 4+(end-start))
		args = append(args, "send-keys", "-t", Target(name), "-H")
		for _, b := range data[start:end] {
			args = append(args, fmt.Sprintf("%02x", b))
		}
		if err := clirun.Check("tmux", args...); err != nil {
			return err
		}
	}
	return nil
}

// PasteLiteral pastes literal text through a temporary tmux buffer.
func (c *Client) PasteLiteral(name, text string) error {
	if text == "" {
		return nil
	}
	buffer := fmt.Sprintf("dev-cockpit-%d", time.Now().UnixNano())
	if err := clirun.Check("tmux", "set-buffer", "-b", buffer, text); err != nil {
		return err
	}
	return clirun.Check("tmux", "paste-buffer", "-t", Target(name), "-b", buffer, "-d", "-p", "-r")
}

// ListPanes returns the unique first-pane entries for every session.
func (c *Client) ListPanes() ([]Pane, error) {
	r := clirun.Run("tmux", "list-panes", "-a", "-F",
		"#{session_name}\t#{pane_pid}\t#{session_created}\t#{window_index}\t#{pane_index}\t#{@dc_shell_name}")
	if r.Err != nil && r.ExitCode != 0 {
		if isNoServerError(r.Stderr) {
			return nil, nil
		}
		msg := strings.TrimSpace(r.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(r.Stdout)
		}
		if msg == "" {
			msg = "Failed to list active sessions."
		}
		return nil, errors.New(msg)
	}
	return parsePanes(r.Stdout), nil
}

func isNoServerError(stderr string) bool {
	s := strings.ToLower(stderr)
	if strings.Contains(s, "no server running") {
		return true
	}
	return strings.Contains(s, "error connecting to") && strings.Contains(s, "no such file or directory")
}

func parsePanes(out string) []Pane {
	seen := map[string]bool{}
	var panes []Pane
	for _, raw := range strings.Split(out, "\n") {
		parts := strings.Split(strings.TrimRight(raw, "\n"), "\t")
		if len(parts) != 6 {
			continue
		}
		name, pid, created, win, pane, shellName := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5]
		if win != "0" || pane != "0" || seen[name] {
			continue
		}
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		seen[name] = true
		panes = append(panes, Pane{Name: name, PID: pid, StartedAt: created, ShellName: shellName})
	}
	sort.Slice(panes, func(i, j int) bool {
		a, b := strings.ToLower(panes[i].Name), strings.ToLower(panes[j].Name)
		if a != b {
			return a < b
		}
		pi, _ := strconv.Atoi(panes[i].PID)
		pj, _ := strconv.Atoi(panes[j].PID)
		return pi < pj
	})
	return panes
}
