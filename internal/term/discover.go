package term

import (
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
)

// Pane is one live session as discovered from the runtime directory. The fields
// mirror what the old tmux list-panes provided, so the correlation logic in the
// session package is unchanged: Name is the session key, PID is the agent (the
// root of the program's process tree), StartedAt is unix epoch seconds.
type Pane struct {
	Name      string
	PID       string
	StartedAt string
}

// Discover lists live sessions and, as a side effect, removes the socket/pid
// files left behind by agents that died without cleaning up.
func (c *Client) Discover() ([]Pane, error) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var panes []Pane
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sock") {
			continue
		}
		key := strings.TrimSuffix(e.Name(), ".sock")
		pid, ok := c.readPID(key)
		if !ok || !processAlive(pid) {
			c.removeSession(key)
			continue
		}
		started := ""
		if info, err := os.Stat(socketPath(c.dir, key)); err == nil {
			started = strconv.FormatInt(info.ModTime().Unix(), 10)
		}
		panes = append(panes, Pane{Name: key, PID: strconv.Itoa(pid), StartedAt: started})
	}
	sort.Slice(panes, func(i, j int) bool { return panes[i].Name < panes[j].Name })
	return panes, nil
}

// Reap removes stale session files. It is Discover with the result discarded.
func (c *Client) Reap() { _, _ = c.Discover() }

func (c *Client) removeSession(key string) {
	_ = os.Remove(socketPath(c.dir, key))
	_ = os.Remove(pidPath(c.dir, key))
	_ = os.Remove(filepath.Join(c.dir, key+".log"))
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(pid, 0) == nil
}
