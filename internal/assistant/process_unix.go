//go:build unix

package assistant

import (
	"os"
	"os/exec"
	"syscall"
)

// detach takes the turn process out of this server's session: its own session
// and, with it, its own process group. A turn must survive the server going
// away, so it may not be in the group a shutdown or a terminal hangup reaches,
// and the group is what a cancel kills, because a coder CLI spawns helpers.
func detach(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}

// lockTurn creates the lock file of a turn and takes the exclusive lock the
// host inherits.
func lockTurn(path string) (*os.File, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// lockHeld reports whether something still holds the lock of a turn. Holding
// it takes a descriptor inherited from the start of that turn, so a recycled
// process number cannot fool this, and a process that ended released it even
// if nobody collected the process itself yet.
func lockHeld(path string) bool {
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return true
	}
	return false
}

// processAlive reports whether a turn this server did not start is still
// writing. The lock is the truth. The wait is only hygiene: after a self
// update the turns of the previous image are this image's children, and a
// child nobody collects would sit around as a zombie for as long as this
// server lives. Not our child answers with an error, which is the normal case
// after a full restart and costs nothing.
func processAlive(pid int, lock string) bool {
	if pid > 0 {
		var status syscall.WaitStatus
		_, _ = syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	}
	return lockHeld(lock)
}

// killProcess ends the whole process group of a turn. The lock is checked
// first: a turn that already ended must not be signalled, its process number
// may belong to somebody else by now.
func killProcess(pid int, lock string) {
	if pid <= 0 || !lockHeld(lock) {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
