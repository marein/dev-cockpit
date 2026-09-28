package render

import (
	"fmt"
	"html/template"

	"github.com/marein/dev-cockpit/internal/hostinfo"
)

// The host status is colored in two places, here for the first paint and in
// host-status.js for every reading after it. Both read the same thresholds out
// of internal/hostinfo.

// HostBarClass colors one bar by its own reading: green while quiet, yellow
// from the warn threshold and red from the critical one, wherever a bar stands.
func HostBarClass(percent int) string {
	switch {
	case percent >= hostinfo.Crit:
		return "bg-red"
	case percent >= hostinfo.Warn:
		return "bg-yellow"
	default:
		return "bg-green"
	}
}

// HostTabClass colors the phone's Cockpit gauge by the worst reading: nothing
// of its own while every metric is quiet, so it keeps the tab's color and the
// active one, yellow from the warn threshold and red from the critical one.
func HostTabClass(s hostinfo.Stats) string {
	_, percent := hostWorst(s)
	switch {
	case percent >= hostinfo.Crit:
		return "text-red"
	case percent >= hostinfo.Warn:
		return "text-yellow"
	default:
		return ""
	}
}

// HostTabLabel names the state the gauge's color says, empty while quiet.
func HostTabLabel(s hostinfo.Stats) string {
	name, percent := hostWorst(s)
	switch {
	case percent >= hostinfo.Crit:
		return fmt.Sprintf("Server critical, %s %d%%", name, percent)
	case percent >= hostinfo.Warn:
		return fmt.Sprintf("Server busy, %s %d%%", name, percent)
	default:
		return ""
	}
}

// hostWorst answers the metric with the highest reading, the first of CPU,
// RAM and disk on a tie, and -1 when the host reports nothing.
func hostWorst(s hostinfo.Stats) (name string, percent int) {
	percent = -1
	for _, m := range []struct {
		has   bool
		name  string
		value int
	}{
		{s.HasCPU, "CPU", s.CPUPercent},
		{s.HasMem, "RAM", s.MemPercent},
		{s.HasDisk, "Disk", s.DiskPercent},
	} {
		if m.has && m.value > percent {
			name, percent = m.name, m.value
		}
	}
	return name, percent
}

// HostBarStyle writes the width of a bar. It is built here rather than
// interpolated in the template so the value never lands in a CSS context the
// template escaper has to guess at.
func HostBarStyle(percent int) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`style="width: %d%%"`, hostinfo.Bar(percent)))
}
