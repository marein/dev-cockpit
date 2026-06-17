//go:build !linux

package term

import (
	"errors"
	"os"
)

// errNoPTY marks platforms where the session agent cannot run. The agent only
// ever runs on the Linux host; non-linux builds exist so the rest of the binary
// (serve, attach) still compiles for macOS.
var errNoPTY = errors.New("session agent is only supported on linux")

func openPTY() (master, slave *os.File, err error) { return nil, nil, errNoPTY }

func setWinsize(f *os.File, cols, rows int) error { return errNoPTY }

func getWinsize(f *os.File) (cols, rows int, err error) { return 0, 0, errNoPTY }
