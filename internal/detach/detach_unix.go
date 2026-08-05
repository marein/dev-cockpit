//go:build unix

package detach

import (
	"os"
	"os/exec"
	"syscall"
)

// detach takes the hold process out of this server's session: its own session
// and, with it, its own process group. A run must survive the server going
// away, so it may not be in the group a shutdown or a terminal hangup reaches,
// and the group is what a kill ends, because a program spawns helpers.
func detach(cmd *exec.Cmd) error {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	return nil
}

// takeLock creates the lock file of a run and takes the exclusive lock the hold
// process inherits.
func takeLock(path string) (*os.File, error) {
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

// lockHeld reports whether something still holds the lock of a run. Holding it
// takes a descriptor inherited from the start of that run, so a recycled
// process number cannot fool this, and a process that ended released it even if
// nobody collected the process itself yet.
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

// Alive reports whether a run this server did not start is still going. The
// lock is the truth. The wait is only hygiene: after a self update the runs of
// the previous image are this image's children, and a child nobody collects
// would sit around as a zombie for as long as this server lives. Not our child
// answers with an error, which is the normal case after a full restart and
// costs nothing.
func Alive(pid int, lock string) bool {
	if pid > 0 {
		var status syscall.WaitStatus
		_, _ = syscall.Wait4(pid, &status, syscall.WNOHANG, nil)
	}
	return lockHeld(lock)
}

// killOwnGroup ends the process group this process leads, itself included, and
// then never returns. The hold process is a session leader (Start detaches it
// with Setsid before the program runs), so its group holds exactly the run:
// the program and whatever the program spawned, every one of them holding the
// inherited lock. The program deliberately stays in this group rather than
// getting one of its own, because Kill reaches a run through the hold's group
// and a program outside it would survive a cancel. A process that does not
// lead its group did not come through Start, a test driving Hold directly, and
// leaves the kill to its caller.
func killOwnGroup() {
	if syscall.Getpgrp() != os.Getpid() {
		return
	}
	_ = syscall.Kill(-os.Getpid(), syscall.SIGKILL)
}

// Kill ends the whole process group of a run. The lock is checked first: a run
// that already ended must not be signalled, its process number may belong to
// somebody else by now.
func Kill(pid int, lock string) {
	if pid <= 0 || !lockHeld(lock) {
		return
	}
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil {
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
}
