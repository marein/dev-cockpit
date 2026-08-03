// Package hostinfo reads what the machine the cockpit runs on is up to: how
// busy it is, how much memory is in use, and how full the disk under the
// projects is. Three percentages, cheap enough to read while a browser is
// connected, and nothing to configure.
//
// Everything here stays free of cgo, because the released binaries are built
// that way. That rules out the mach calls a true CPU busy percentage would need
// on macOS, so the busy number is the load average against the core count. It
// means the same thing on both systems, it is already averaged over a minute so
// the reading does not flicker, and it may pass 100 percent when more work is
// queued than the machine can run at once. That is information, not a bug.
package hostinfo

import (
	"fmt"
	"math"
	"runtime"
	"sync"
	"time"
)

// Warn and Crit are the thresholds a surface colors on: quiet below Warn,
// yellow from Warn, red from Crit.
const (
	Warn = 80
	Crit = 95
)

// Stats is one reading. A metric the platform could not answer keeps its
// Has flag false and its percentage at -1, so a surface can leave that row out
// instead of showing a zero that would read as "idle".
type Stats struct {
	HasCPU  bool `json:"hasCpu"`
	HasMem  bool `json:"hasMem"`
	HasDisk bool `json:"hasDisk"`

	CPUPercent  int `json:"cpu"`
	MemPercent  int `json:"mem"`
	DiskPercent int `json:"disk"`

	CPULabel  string `json:"cpuLabel"`
	MemLabel  string `json:"memLabel"`
	DiskLabel string `json:"diskLabel"`

	// Level is "", "warn" or "crit", taken from the worst of the readings.
	Level string `json:"level"`
}

// Any reports whether the reading carries a single usable number.
func (s Stats) Any() bool { return s.HasCPU || s.HasMem || s.HasDisk }

// Worst is the highest of the readings, the one the color follows.
func (s Stats) Worst() int {
	worst := 0
	for _, m := range []struct {
		ok bool
		v  int
	}{{s.HasCPU, s.CPUPercent}, {s.HasMem, s.MemPercent}, {s.HasDisk, s.DiskPercent}} {
		if m.ok && m.v > worst {
			worst = m.v
		}
	}
	return worst
}

// Bar caps a percentage at 100 for the width of a progress bar. The number
// next to it keeps the real value.
func Bar(percent int) int {
	if percent < 0 {
		return 0
	}
	if percent > 100 {
		return 100
	}
	return percent
}

// Cache reads at most once per interval, so every connected browser shares one
// reading instead of each heartbeat costing its own.
type Cache struct {
	path string
	ttl  time.Duration

	mu    sync.Mutex
	at    time.Time
	stats Stats
}

// NewCache reads the disk of the filesystem path sits on.
func NewCache(path string, ttl time.Duration) *Cache {
	if ttl <= 0 {
		ttl = 10 * time.Second
	}
	return &Cache{path: path, ttl: ttl}
}

// Stats returns the current reading, refreshing it when the cached one aged out.
func (c *Cache) Stats() Stats {
	if c == nil {
		return Stats{CPUPercent: -1, MemPercent: -1, DiskPercent: -1}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < c.ttl {
		return c.stats
	}
	c.stats = read(c.path)
	c.at = time.Now()
	return c.stats
}

// read takes one reading. Every metric stands on its own: a platform that
// cannot answer one of them still reports the other two.
func read(path string) Stats {
	s := Stats{CPUPercent: -1, MemPercent: -1, DiskPercent: -1}
	cores := runtime.NumCPU()
	if load, ok := loadAverage(); ok && cores > 0 {
		s.HasCPU = true
		s.CPUPercent = int(math.Round(load / float64(cores) * 100))
		s.CPULabel = fmt.Sprintf("Load %.2f on %d cores", load, cores)
		if cores == 1 {
			s.CPULabel = fmt.Sprintf("Load %.2f on 1 core", load)
		}
	}
	if total, available, ok := memory(); ok && total > 0 {
		if available > total {
			available = total
		}
		s.HasMem = true
		s.MemPercent = percent(total-available, total)
		s.MemLabel = fmt.Sprintf("%s of %s used", humanBytes(total-available), humanBytes(total))
	}
	if total, free, ok := disk(path); ok && total > 0 {
		if free > total {
			free = total
		}
		s.HasDisk = true
		s.DiskPercent = percent(total-free, total)
		s.DiskLabel = fmt.Sprintf("%s of %s free", humanBytes(free), humanBytes(total))
	}
	switch worst := s.Worst(); {
	case !s.Any():
	case worst >= Crit:
		s.Level = "crit"
	case worst >= Warn:
		s.Level = "warn"
	}
	return s
}

func percent(part, whole uint64) int {
	if whole == 0 {
		return 0
	}
	return int(math.Round(float64(part) / float64(whole) * 100))
}

// humanBytes writes a size the way the machines themselves count it, in steps
// of 1024, with one decimal while the number is small enough for it to matter.
func humanBytes(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	value := float64(b)
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	i := -1
	for value >= unit && i < len(units)-1 {
		value /= unit
		i++
	}
	if value < 10 {
		return fmt.Sprintf("%.1f %s", value, units[i])
	}
	return fmt.Sprintf("%.0f %s", value, units[i])
}
