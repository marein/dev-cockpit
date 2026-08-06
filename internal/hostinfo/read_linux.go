//go:build linux

package hostinfo

import (
	"fmt"
	"os"
	"runtime"
	"sync"
)

// cpuPrev is the previous /proc/stat sample. Its counters count ticks since
// boot, so one sample alone says nothing about now: the busy share is the
// delta between two readings, and the sample before has to survive between
// calls. Package state, guarded on its own because nothing ties every caller
// to one Cache.
var cpuPrev struct {
	sync.Mutex
	idle  uint64
	total uint64
	valid bool
}

// cpu is the real busy share of the cores, the movement between this
// /proc/stat sample and the one before. The first reading has no sample to
// diff against and answers the load average instead, like macOS always does;
// the label says which of the two the number is.
func cpu() (busy int, label string, ok bool) {
	data, err := os.ReadFile("/proc/stat")
	if err != nil {
		return loadCPU()
	}
	idle, total, parsed := parseProcStat(string(data))
	if !parsed {
		return loadCPU()
	}
	cpuPrev.Lock()
	prevIdle, prevTotal, valid := cpuPrev.idle, cpuPrev.total, cpuPrev.valid
	cpuPrev.idle, cpuPrev.total, cpuPrev.valid = idle, total, true
	cpuPrev.Unlock()
	if !valid || total <= prevTotal || idle < prevIdle {
		return loadCPU()
	}
	busy = busyPercent(idle-prevIdle, total-prevTotal)
	cores := runtime.NumCPU()
	label = fmt.Sprintf("Usage across %d cores", cores)
	if cores == 1 {
		label = "Usage on 1 core"
	}
	return busy, label, true
}

func loadAverage() (float64, bool) {
	data, err := os.ReadFile("/proc/loadavg")
	if err != nil {
		return 0, false
	}
	return parseProcLoadAvg(string(data))
}

func memory() (total, available uint64, ok bool) {
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0, false
	}
	return parseMeminfo(string(data))
}
