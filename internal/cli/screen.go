package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/marein/dev-cockpit/internal/terminal"
	"github.com/marein/dev-cockpit/internal/terminalstate"
	"github.com/marein/dev-cockpit/internal/tmux"
	"github.com/spf13/cobra"
)

// defaultOutputLines is how much of a terminal the assistant reads by default:
// enough to see what a coder said last, small enough to stay a fraction of a
// turn's context.
const defaultOutputLines = 120

func newOutputCommand(opts *inspectOptions) *cobra.Command {
	lines := defaultOutputLines
	cmd := &cobra.Command{
		Use:   "terminal-screen <terminal>",
		Short: "Show what a coder or shell has on screen",
		Long: "Show the last lines of a terminal the cockpit runs: what a coder said, " +
			"what it is waiting for, what a command printed. The terminal is an id from " +
			"`status`. Reads only, and it never resizes the terminal.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runOutput(cmd.OutOrStdout(), *opts, args[0], lines)
		},
	}
	cmd.Flags().IntVar(&lines, "lines", lines, "how many lines to show")
	return cmd
}

func runOutput(out io.Writer, opts inspectOptions, rawTarget string, lines int) error {
	target, err := terminal.ValidateIdentifier(rawTarget)
	if err != nil {
		return err
	}
	text, err := tmux.New().CapturePane(target, lines)
	if err != nil {
		if text, err = captureMoved(opts, target, lines, err); err != nil {
			return err
		}
	}
	if strings.TrimSpace(text) == "" {
		fmt.Fprintf(out, "Terminal %s has nothing on screen.\n", target)
		return nil
	}
	fmt.Fprintln(out, text)
	return nil
}

// captureMoved reads a terminal tmux knows under another name than its id: a
// coder whose session took its record's id after its start keeps the pane it
// started on (coder.LateSessionRecords), and the coders the server holds
// resolve the id to that pane the way its routes do. Else it says why the
// screen could not be read, and what can be done about it. What tmux says is
// that it found no session, which leaves the caller guessing whether the
// terminal is gone for good, so the id is classified against the same coders
// and shells and the answer names `coder-resume` where it is the way back. A
// cockpit this process cannot even read falls back to what tmux said, which
// is all there is then.
func captureMoved(opts inspectOptions, target string, lines int, capture error) (string, error) {
	picture, err := openTerminals(opts)
	if err != nil {
		return "", fmt.Errorf("no terminal %q is running: %s", target, firstLine(capture.Error()))
	}
	if pane, ok := picture.pane(target); ok && pane != target {
		text, err := tmux.New().CapturePane(pane, lines)
		if err == nil {
			return text, nil
		}
		capture = err
	}
	return "", errors.New(screenErrorMessage(target,
		terminalstate.Classify(target, picture.coderLookups(), picture.shells), capture))
}

// pane is the tmux session a running coder stands on, found the way the
// server's routes find it.
func (t terminals) pane(id string) (string, bool) {
	for _, manager := range t.coders {
		if r, err := manager.ResolveRunning(id); err == nil {
			return r.TmuxSession, true
		}
	}
	return "", false
}

// screenErrorMessage words one classified id for this command. There is no
// branch for a shell that is gone: a shell leaves nothing behind, so an id no
// shell runs under is indistinguishable from an id nobody ever used, and this
// command takes any id. The shell's own sentence lives on the shell route,
// which knows what it was asked about.
func screenErrorMessage(target string, state terminalstate.State, capture error) string {
	switch state {
	case terminalstate.Running:
		return fmt.Sprintf("terminal %q is running, but reading its screen failed: %s",
			target, firstLine(capture.Error()))
	case terminalstate.Resumable:
		return fmt.Sprintf("no terminal %q is running, its coder session is stopped: `coder-resume %s` brings it back",
			target, target)
	default:
		return fmt.Sprintf("no terminal %q is running, and no session with that id can be resumed", target)
	}
}
