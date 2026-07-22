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
	Workdir   string // @dc_shell_dir option; the shell's start directory, if any
	Coder     string // @dc_coder option; non-empty marks a coder pane and names its coder
	CoderName string // @dc_coder_name option; the coder's display name at launch
	CoderDir  string // @dc_coder_dir option; the coder's start directory
	TabPos    string // @dc_tab_pos option; the session's tab strip position, raw
	TabGroup  string // @dc_tab_group option; non-empty puts the session into a split view group
	TabGPos   string // @dc_tab_gpos option; the session's position inside its group, raw
	TabGName  string // @dc_tab_gname option; the group's display name, duplicated on every member
}

// TabPosition parses the pane's tab strip position; 0 when unset or invalid,
// which sorts the session after every positioned one.
func (p Pane) TabPosition() int {
	v, err := strconv.Atoi(strings.TrimSpace(p.TabPos))
	if err != nil || v < 1 {
		return 0
	}
	return v
}

// TabGroupPosition parses the pane's position inside its split view group; 0
// when unset or invalid, which sorts the member after every positioned one.
func (p Pane) TabGroupPosition() int {
	v, err := strconv.Atoi(strings.TrimSpace(p.TabGPos))
	if err != nil || v < 1 {
		return 0
	}
	return v
}

// StartTime parses the pane's raw session_created stamp; zero when invalid.
func (p Pane) StartTime() time.Time {
	v, err := strconv.ParseInt(strings.TrimSpace(p.StartedAt), 10, 64)
	if err != nil {
		return time.Time{}
	}
	return time.Unix(v, 0).UTC()
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

// SetPaneStyle sets a pane's default colors (style like "bg=#111827,fg=#f9fafb").
// tmux answers a pane program's OSC 11 background query from this style, so it
// is how a session signals light or dark to TUIs while only the control mode
// client is attached, which never answers such queries itself.
func (c *Client) SetPaneStyle(name, style string) error {
	return clirun.Check("tmux", "select-pane", "-t", Target(name), "-P", style)
}

// PaneForeground describes a session's foreground process state. AltScreen
// separates a full screen TUI (claude's interactive mode) from plain command
// runs like `claude -p` that share the process name.
type PaneForeground struct {
	Command   string
	AltScreen bool
}

// PaneForegrounds returns every session's foreground process (first pane
// only), from a single list-panes call.
func (c *Client) PaneForegrounds() map[string]PaneForeground {
	r := clirun.Run("tmux", "list-panes", "-a", "-F",
		"#{session_name}\t#{window_index}\t#{pane_index}\t#{alternate_on}\t#{pane_current_command}")
	if r.Err != nil {
		return nil
	}
	foregrounds := map[string]PaneForeground{}
	for _, line := range strings.Split(r.Stdout, "\n") {
		parts := strings.Split(line, "\t")
		if len(parts) != 5 || parts[1] != "0" || parts[2] != "0" {
			continue
		}
		foregrounds[parts[0]] = PaneForeground{Command: parts[4], AltScreen: parts[3] == "1"}
	}
	return foregrounds
}

// PaneForeground returns one session's foreground process state.
func (c *Client) PaneForeground(name string) (PaneForeground, error) {
	fg, ok := c.PaneForegrounds()[name]
	if !ok {
		return PaneForeground{}, errors.New("Failed to read the pane state.")
	}
	return fg, nil
}

// PaneTheme describes one pane's theme application for ApplyPaneThemes.
type PaneTheme struct {
	Name   string
	Style  string
	Report []byte
}

// ApplyPaneThemes styles panes and injects the optional reports in one tmux
// invocation, so a theme change costs a single process spawn regardless of
// session count. tmux aborts the command chain on the first error (e.g. a
// session that just died), remaining panes keep their old style then; the
// caller is expected to re-apply on the next occasion.
func (c *Client) ApplyPaneThemes(themes []PaneTheme) error {
	if len(themes) == 0 {
		return nil
	}
	args := make([]string, 0, len(themes)*16)
	for _, t := range themes {
		if len(args) > 0 {
			args = append(args, ";")
		}
		args = append(args, "select-pane", "-t", Target(t.Name), "-P", t.Style)
		if len(t.Report) > 0 {
			args = append(args, ";", "send-keys", "-t", Target(t.Name), "-H")
			for _, b := range t.Report {
				args = append(args, fmt.Sprintf("%02x", b))
			}
		}
	}
	return clirun.Check("tmux", args...)
}

// tabPosOption is the tmux user option that holds a session's tab strip
// position. The order lives in tmux on purpose: it dies with the session, so
// there is no state file to prune, and every device sees the same strip.
const tabPosOption = "@dc_tab_pos"

// SetTabPositions writes the tab strip order onto the sessions, first name is
// position 1. All assignments ride in one tmux invocation, so a reorder costs
// a single process spawn regardless of session count.
func (c *Client) SetTabPositions(names []string) error {
	if len(names) == 0 {
		return nil
	}
	args := make([]string, 0, len(names)*6)
	for i, name := range names {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args, "set-option", "-t", name, tabPosOption, strconv.Itoa(i+1))
	}
	return clirun.Check("tmux", args...)
}

// SetTabPosition writes one session's tab strip position, used by the startup
// terminal restore to re-apply a recorded order onto a recreated session.
func (c *Client) SetTabPosition(name string, pos int) error {
	return clirun.Check("tmux", "set-option", "-t", name, tabPosOption, strconv.Itoa(pos))
}

// Split view group membership lives in tmux like the tab order does: every
// member session carries the group id, its position inside the group and the
// group's display name as user options. Membership dies with the session, is
// cross-device by construction and needs no state file. The name is duplicated
// on every member with last-write-wins semantics.
const (
	tabGroupOption     = "@dc_tab_group"
	tabGroupPosOption  = "@dc_tab_gpos"
	tabGroupNameOption = "@dc_tab_gname"
)

// SetTabGroup writes a split view group onto its member sessions, first name
// is group position 1. The optional display name rides along when non-empty.
// All assignments go into one tmux invocation.
func (c *Client) SetTabGroup(names []string, group, groupName string) error {
	if len(names) == 0 {
		return nil
	}
	args := make([]string, 0, len(names)*16)
	for i, name := range names {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args,
			"set-option", "-t", name, tabGroupOption, group, ";",
			"set-option", "-t", name, tabGroupPosOption, strconv.Itoa(i+1))
		if groupName != "" {
			args = append(args, ";", "set-option", "-t", name, tabGroupNameOption, groupName)
		}
	}
	return clirun.Check("tmux", args...)
}

// SetTabGroupEntry re-applies one session's recorded group membership, used by
// the startup terminal restore on resumed and recreated sessions.
func (c *Client) SetTabGroupEntry(name, group string, pos int, groupName string) error {
	if group == "" || pos < 1 {
		return nil
	}
	args := []string{
		"set-option", "-t", name, tabGroupOption, group, ";",
		"set-option", "-t", name, tabGroupPosOption, strconv.Itoa(pos),
	}
	if groupName != "" {
		args = append(args, ";", "set-option", "-t", name, tabGroupNameOption, groupName)
	}
	return clirun.Check("tmux", args...)
}

// ClearTabGroup removes the split view group options from the sessions, in one
// tmux invocation.
func (c *Client) ClearTabGroup(names []string) error {
	if len(names) == 0 {
		return nil
	}
	args := make([]string, 0, len(names)*16)
	for i, name := range names {
		if i > 0 {
			args = append(args, ";")
		}
		args = append(args,
			"set-option", "-u", "-t", name, tabGroupOption, ";",
			"set-option", "-u", "-t", name, tabGroupPosOption, ";",
			"set-option", "-u", "-t", name, tabGroupNameOption)
	}
	return clirun.Check("tmux", args...)
}

// SetTabGroupName writes the group display name onto every member, empty
// removes it (the strip falls back to the joined member names).
func (c *Client) SetTabGroupName(names []string, groupName string) error {
	if len(names) == 0 {
		return nil
	}
	args := make([]string, 0, len(names)*8)
	for i, name := range names {
		if i > 0 {
			args = append(args, ";")
		}
		if groupName == "" {
			args = append(args, "set-option", "-u", "-t", name, tabGroupNameOption)
		} else {
			args = append(args, "set-option", "-t", name, tabGroupNameOption, groupName)
		}
	}
	return clirun.Check("tmux", args...)
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
		"#{session_name}\t#{pane_pid}\t#{session_created}\t#{window_index}\t#{pane_index}\t#{@dc_shell_name}\t#{@dc_shell_dir}\t#{@dc_coder}\t#{@dc_coder_name}\t#{@dc_coder_dir}\t#{@dc_tab_pos}\t#{@dc_tab_group}\t#{@dc_tab_gpos}\t#{@dc_tab_gname}")
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
		if len(parts) != 14 {
			continue
		}
		name, pid, created, win, pane, shellName, workdir := parts[0], parts[1], parts[2], parts[3], parts[4], parts[5], parts[6]
		coder, coderName, coderDir, tabPos := parts[7], parts[8], parts[9], parts[10]
		tabGroup, tabGPos, tabGName := parts[11], parts[12], parts[13]
		if win != "0" || pane != "0" || seen[name] {
			continue
		}
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		seen[name] = true
		panes = append(panes, Pane{
			Name: name, PID: pid, StartedAt: created,
			ShellName: shellName, Workdir: workdir,
			Coder: coder, CoderName: coderName, CoderDir: coderDir,
			TabPos: tabPos, TabGroup: tabGroup, TabGPos: tabGPos, TabGName: tabGName,
		})
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
