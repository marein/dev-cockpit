//go:build darwin

package hostinfo

import (
	"context"
	"os/exec"
	"time"
)

// macOS keeps these numbers behind mach calls that need cgo, and the released
// binaries carry none. Both of these tools ship with the system, print a
// documented format, and cost a few milliseconds; the cache decides how often
// they actually run.

const readTimeout = 2 * time.Second

func run(name string, args ...string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), readTimeout)
	defer cancel()
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return "", false
	}
	return string(out), true
}

func loadAverage() (float64, bool) {
	out, ok := run("/usr/sbin/sysctl", "-n", "vm.loadavg")
	if !ok {
		return 0, false
	}
	return parseSysctlLoadAvg(out)
}

func memory() (total, available uint64, ok bool) {
	sizeOut, ok := run("/usr/sbin/sysctl", "-n", "hw.memsize")
	if !ok {
		return 0, 0, false
	}
	total, ok = parseSysctlUint(sizeOut)
	if !ok {
		return 0, 0, false
	}
	statOut, ok := run("/usr/bin/vm_stat")
	if !ok {
		return 0, 0, false
	}
	available, ok = parseVMStat(statOut)
	if !ok {
		return 0, 0, false
	}
	return total, available, true
}
