package assistant

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// A turn must be recognizable across server restarts without trusting a
// process number. The server takes an exclusive flock on a file of the turn
// before anything starts and hands the locked descriptor down, and the turn
// process keeps it open for as long as it lives. The lock dies with the last
// process that inherited it, so whether the file can be locked is whether the
// turn still runs. No start times, no process table, the same code on every
// unix.

// RunTurn runs the provider named by args and waits it out. It is the
// process behind `dev-cockpit assistant run-turn`, and its one job is to
// hold the turn's lock, inherited as an open descriptor, while the provider
// runs: the provider cannot drop it by closing its own descriptors, because
// this process keeps its copy. Standard output and error are already the
// files of the turn, wired up by the server before this process started, and
// the provider's exit code is returned as this process's own.
func RunTurn(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "run-turn: nothing to run")
		return 1
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		return exit.ExitCode()
	}
	// A provider that could not be started at all says so into the turn's
	// error file, where stderrTail finds it for the server log.
	fmt.Fprintln(os.Stderr, err)
	return 1
}

// runTurnArgs is the argv prefix that reaches RunTurn in the real binary.
// The test binary has no command tree and recognizes the same tokens itself,
// so the server builds one argv and both binaries read it the same way.
var runTurnArgs = []string{"assistant", "run-turn"}

// selfExecutable is the binary a turn process is started from. A binary
// replaced underneath a running process, a self update between the swap and
// the re-exec, reads back with a " (deleted)" marker that exec would refuse.
func selfExecutable() (string, error) {
	path, err := os.Executable()
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(path, " (deleted)"), nil
}
