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
var RequiredTools = []string{"tmux", "stdbuf"}

// Pane is one tmux session entry as reported by `tmux list-panes`.
type Pane struct {
	Name      string
	PID       string
	StartedAt string // unix epoch seconds, raw
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

// HasSession returns true when tmux knows the named session.
func (c *Client) HasSession(name string) bool {
	return clirun.Run("tmux", "has-session", "-t", name).Err == nil
}

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

// StartPipe appends live pane output to logPath.
func (c *Client) StartPipe(name, logPath string) error {
	return clirun.Check("tmux", "pipe-pane", "-t", Target(name),
		"stdbuf -o0 cat >> "+clirun.ShellQuote(logPath))
}

// StopPipe detaches any active pipe-pane logger.
func (c *Client) StopPipe(name string) error {
	return clirun.Check("tmux", "pipe-pane", "-t", Target(name))
}

// CapturePane returns the current pane contents plus bounded history.
func (c *Client) CapturePane(name string) ([]byte, error) {
	r := clirun.Run("tmux", "capture-pane", "-p", "-e", "-t", Target(name), "-S", "0", "-E", "-")
	if r.Err != nil {
		if r.ExitCode != 0 {
			msg := strings.TrimSpace(r.Stderr)
			if msg == "" {
				msg = "Failed to capture terminal."
			}
			return nil, errors.New(msg)
		}
		return nil, r.Err
	}
	out := stripSnapshotCursor(stripOSC([]byte(r.Stdout)))
	cursor := clirun.Run("tmux", "display-message", "-p", "-t", Target(name), "#{cursor_x} #{cursor_y} #{cursor_flag}")
	if cursor.Err != nil {
		return out, nil
	}
	fields := strings.Fields(cursor.Stdout)
	if len(fields) < 2 {
		return out, nil
	}
	x, xErr := strconv.Atoi(fields[0])
	y, yErr := strconv.Atoi(fields[1])
	if xErr != nil || yErr != nil {
		return out, nil
	}
	// cursor_flag reports whether the pane's program keeps the hardware cursor
	// visible (DECTCEM). Programs that rely on it (e.g. the Copilot CLI) draw no
	// cursor of their own, so move xterm's cursor to tmux's position and let xterm
	// render it. Programs that draw their own cursor (e.g. Claude Code, with the
	// hardware cursor disabled) instead get a redrawn reverse-video block here,
	// while xterm's own cursor stays disabled.
	if len(fields) >= 3 && fields[2] == "1" {
		return append(out, []byte(fmt.Sprintf("\x1b[%d;%dH\x1b[?25h", y+1, x+1))...), nil
	}
	return append(out, []byte(fmt.Sprintf("\x1b[%d;%dH\x1b[97m\x1b[49m\x1b[7m \x1b[0m\x1b[?25l", y+1, x+1))...), nil
}

// PaneSize returns the current pane dimensions.
func (c *Client) PaneSize(name string) (Size, error) {
	r := clirun.Run("tmux", "display-message", "-p", "-t", Target(name), "#{pane_width} #{pane_height}")
	if r.Err != nil {
		if r.ExitCode != 0 {
			msg := strings.TrimSpace(r.Stderr)
			if msg == "" {
				msg = "Failed to read terminal size."
			}
			return Size{}, errors.New(msg)
		}
		return Size{}, r.Err
	}
	fields := strings.Fields(r.Stdout)
	if len(fields) != 2 {
		return Size{}, errors.New("Failed to parse terminal size.")
	}
	cols, err := strconv.Atoi(fields[0])
	if err != nil {
		return Size{}, err
	}
	rows, err := strconv.Atoi(fields[1])
	if err != nil {
		return Size{}, err
	}
	return Size{Cols: cols, Rows: rows}, nil
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

// Resize sets the terminal dimensions of a session.
func (c *Client) Resize(name string, cols, rows int) error {
	return clirun.Check("tmux", "resize-window", "-t", name,
		"-x", strconv.Itoa(cols), "-y", strconv.Itoa(rows))
}

// ListPanes returns the unique first-pane entries for every session.
func (c *Client) ListPanes() ([]Pane, error) {
	r := clirun.Run("tmux", "list-panes", "-a", "-F",
		"#{session_name}\t#{pane_pid}\t#{session_created}\t#{window_index}\t#{pane_index}")
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
		if len(parts) != 5 {
			continue
		}
		name, pid, created, win, pane := parts[0], parts[1], parts[2], parts[3], parts[4]
		if win != "0" || pane != "0" || seen[name] {
			continue
		}
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		seen[name] = true
		panes = append(panes, Pane{Name: name, PID: pid, StartedAt: created})
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
