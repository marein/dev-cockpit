package coder

// TODO(v2.0.0): delete this file together with internal/proctree. It only
// attributes coder panes created before panes carried the @dc_coder tmux
// options; those sessions are identified by the DEV_COCKPIT_PROVIDER variable
// in their process environment. Once no pre-option sessions can be running
// anymore, every pane is either option-tagged or foreign and the whole
// process-tree scan goes.

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/local/dev-cockpit/internal/proctree"
	"github.com/local/dev-cockpit/internal/terminal"
	"github.com/local/dev-cockpit/internal/tmux"
)

// legacyEnvVar is the variable older binaries injected into every coder pane.
// It is no longer written, only read to attribute those panes.
const legacyEnvVar = "DEV_COCKPIT_PROVIDER"

func ownsProcess(env map[string]string, coderID string) bool {
	return env[legacyEnvVar] == coderID
}

// runningName extracts the display name from the coder process cmdline
// (--name flag), the only name source for legacy panes without a store record.
func runningName(cmdline []string) string {
	return flagValue(cmdline, "--name")
}

func flagValue(cmdline []string, name string) string {
	prefix := name + "="
	for i, arg := range cmdline {
		switch {
		case arg == name && i+1 < len(cmdline):
			return strings.TrimSpace(cmdline[i+1])
		case strings.HasPrefix(arg, prefix):
			return strings.TrimSpace(strings.TrimPrefix(arg, prefix))
		}
	}
	return ""
}

// legacyScanner attributes option-less panes via the process tree. The capture
// is lazy so snapshots pay for it only while such panes still exist.
type legacyScanner struct {
	prov         Coder
	resumable    []Session
	tree         *proctree.Tree
	byID         map[string]Session
	byLegacyName map[string][]Session
	ready        bool
}

func (l *legacyScanner) init() {
	if l.ready {
		return
	}
	l.ready = true
	// Capture the process table once for the whole scan so platforms without
	// /proc read it in two ps calls instead of reforking ps per descendant PID.
	l.tree = proctree.Capture()
	l.byID = map[string]Session{}
	l.byLegacyName = map[string][]Session{}
	for _, r := range l.resumable {
		l.byID[r.SessionID] = r
		n, err := terminal.SanitizeName(r.Name)
		if err != nil {
			continue
		}
		l.byLegacyName[n] = append(l.byLegacyName[n], r)
	}
}

// match reports whether the pane belongs to this coder. The returned id is
// non-empty when a stored session matched, so the caller can exclude it from
// the inactive list.
func (l *legacyScanner) match(p tmux.Pane) (Running, string, bool) {
	l.init()
	var descendants []int
	if rootPID, err := strconv.Atoi(p.PID); err == nil {
		descendants = l.tree.Descendants(rootPID)
	}
	match := findMatchingResumable(descendants, p.Name, l.byID, l.byLegacyName, l.prov, l.tree)
	if match == nil {
		if info, ok := paneCoderInfo(descendants, l.prov, l.tree); ok {
			return Running{
				Identifier:  p.Name,
				TmuxSession: p.Name,
				PID:         p.PID,
				Name:        DisplayName(info.name, p.Name),
				StartedAt:   p.StartTime(),
				CWD:         info.cwd,
			}, "", true
		}
		return Running{}, "", false
	}
	return Running{
		Identifier:  match.SessionID,
		TmuxSession: p.Name,
		PID:         p.PID,
		Name:        DisplayName(match.Name, match.SessionID),
		StartedAt:   p.StartTime(),
		CWD:         match.CWD,
	}, match.SessionID, true
}

func findMatchingResumable(descendants []int, paneName string, byID map[string]Session, byLegacyName map[string][]Session, prov Coder, tree *proctree.Tree) *Session {
	// Modern sessions name the tmux pane after the session ID, so an exact ID
	// hit plus a live coder process is already a unique match. Skip the
	// working-dir comparison here: it is redundant and would cost a per-PID
	// lsof on platforms without /proc.
	if direct, ok := byID[paneName]; ok {
		if hasCoderProcess(descendants, prov, tree) {
			return &direct
		}
	}
	// Legacy sessions were named after the (non-unique) sanitized display name,
	// so fall back to the working dir to disambiguate collisions.
	candidates := byLegacyName[paneName]
	for i := range candidates {
		if matchesResumable(descendants, candidates[i], prov, tree) {
			return &candidates[i]
		}
	}
	return nil
}

func hasCoderProcess(descendants []int, prov Coder, tree *proctree.Tree) bool {
	for _, pid := range descendants {
		if ownsProcess(tree.Environ(pid), prov.ID()) {
			return true
		}
	}
	return false
}

type paneCoderProcess struct {
	cwd  string
	name string
}

func paneCoderInfo(descendants []int, prov Coder, tree *proctree.Tree) (paneCoderProcess, bool) {
	for _, pid := range descendants {
		if ownsProcess(tree.Environ(pid), prov.ID()) {
			return paneCoderProcess{
				cwd:  tree.CWD(pid),
				name: runningName(tree.Cmdline(pid)),
			}, true
		}
	}
	return paneCoderProcess{}, false
}

func matchesResumable(descendants []int, candidate Session, prov Coder, tree *proctree.Tree) bool {
	target, err := filepath.EvalSymlinks(candidate.CWD)
	if err != nil {
		target = candidate.CWD
	}
	for _, pid := range descendants {
		if !ownsProcess(tree.Environ(pid), prov.ID()) {
			continue
		}
		if tree.CWD(pid) == target {
			return true
		}
	}
	return false
}
