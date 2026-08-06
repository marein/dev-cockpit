package hostinfo

import (
	"strconv"
	"strings"
)

// The parsers live apart from the platform files on purpose: they are pure
// string in, numbers out, so the macOS readings can be tested on a Linux
// machine, which is the only kind this project is developed on.

// parseProcLoadAvg takes the first field of /proc/loadavg
// ("0.52 0.58 0.59 1/834 12345").
func parseProcLoadAvg(s string) (float64, bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseFloat(fields[0], 64)
	if err != nil || value < 0 {
		return 0, false
	}
	return value, true
}

// parseProcStat reads the summary cpu line of /proc/stat ("cpu  10132153
// 290696 3084719 46828483 16683 0 25195 0 175628 0"): user, nice, system,
// idle, iowait, irq, softirq, steal, then the guest fields, all in ticks
// since boot. It answers the idle and the total ticks of the sample, the busy
// share is the delta between two of them. Waiting for a disk is not work, so
// iowait counts as idle, and the guest fields are dropped because the kernel
// already counts guest time into user.
func parseProcStat(s string) (idle, total uint64, ok bool) {
	for _, line := range strings.Split(s, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 5 || fields[0] != "cpu" {
			continue
		}
		values := fields[1:]
		if len(values) > 8 {
			values = values[:8]
		}
		for i, field := range values {
			value, err := strconv.ParseUint(field, 10, 64)
			if err != nil {
				return 0, 0, false
			}
			total += value
			if i == 3 || i == 4 {
				idle += value
			}
		}
		return idle, total, true
	}
	return 0, 0, false
}

// busyPercent turns the movement between two /proc/stat samples into the busy
// share of the cores over that window.
func busyPercent(idleDelta, totalDelta uint64) int {
	if totalDelta == 0 {
		return 0
	}
	if idleDelta > totalDelta {
		idleDelta = totalDelta
	}
	return percent(totalDelta-idleDelta, totalDelta)
}

// parseSysctlLoadAvg takes the first average out of what
// `sysctl -n vm.loadavg` prints on macOS: "{ 1.85 2.03 2.11 }".
func parseSysctlLoadAvg(s string) (float64, bool) {
	trimmed := strings.Trim(strings.TrimSpace(s), "{}")
	return parseProcLoadAvg(trimmed)
}

// parseMeminfo reads the total and the available memory out of /proc/meminfo,
// both in bytes. Available is the kernel's own estimate of what a new process
// could get, which counts the reclaimable page cache as free; MemFree alone
// would report a healthy machine as full.
func parseMeminfo(s string) (total, available uint64, ok bool) {
	for _, line := range strings.Split(s, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		switch strings.TrimSpace(key) {
		case "MemTotal":
			total, _ = parseKiBValue(value)
		case "MemAvailable":
			available, _ = parseKiBValue(value)
		}
	}
	return total, available, total > 0 && available > 0
}

// parseKiBValue reads "16314516 kB" as bytes.
func parseKiBValue(s string) (uint64, bool) {
	fields := strings.Fields(s)
	if len(fields) == 0 {
		return 0, false
	}
	value, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0, false
	}
	if len(fields) > 1 && strings.EqualFold(fields[1], "kB") {
		return value * 1024, true
	}
	return value, true
}

// parseVMStat reads what macOS can spare out of `vm_stat`. Available is the
// counterpart of Linux's MemAvailable: the pages that are free plus the ones
// the system would hand over without swapping, which is what Activity Monitor
// treats as not in use. Wired, active and compressed pages are the ones really
// taken.
func parseVMStat(s string) (available uint64, ok bool) {
	pageSize := uint64(4096)
	if _, rest, found := strings.Cut(s, "page size of "); found {
		if fields := strings.Fields(rest); len(fields) > 0 {
			if value, err := strconv.ParseUint(fields[0], 10, 64); err == nil && value > 0 {
				pageSize = value
			}
		}
	}
	pages := map[string]uint64{}
	for _, line := range strings.Split(s, "\n") {
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		digits := strings.TrimSuffix(strings.TrimSpace(value), ".")
		count, err := strconv.ParseUint(digits, 10, 64)
		if err != nil {
			continue
		}
		pages[strings.TrimSpace(key)] = count
	}
	free, hasFree := pages["Pages free"]
	if !hasFree {
		return 0, false
	}
	available = free
	for _, key := range []string{"Pages inactive", "Pages speculative", "Pages purgeable"} {
		available += pages[key]
	}
	return available * pageSize, true
}

// parseSysctlUint reads a single number, the way `sysctl -n hw.memsize` prints it.
func parseSysctlUint(s string) (uint64, bool) {
	value, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, false
	}
	return value, true
}
