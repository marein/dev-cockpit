package render

import (
	"fmt"
	"html/template"

	"github.com/local/dev-cockpit/internal/hostinfo"
)

// The host status is colored in two places, here for the first paint and in
// host-status.js for every reading after it. Both read the same thresholds out
// of internal/hostinfo.

// HostBarClass colors one bar by its own reading.
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

// HostRingClass colors a ring gauge. Unlike a bare number a ring is always
// visible, so the quiet state is an explicit green rather than no class.
func HostRingClass(percent int) string {
	switch {
	case percent >= hostinfo.Crit:
		return "text-red"
	case percent >= hostinfo.Warn:
		return "text-yellow"
	default:
		return "text-green"
	}
}

// HostLevelClass colors the status icon by the worst of the readings.
func HostLevelClass(level string) string {
	switch level {
	case "crit":
		return "text-red"
	case "warn":
		return "text-yellow"
	default:
		return ""
	}
}

// HostBarStyle writes the width of a bar. It is built here rather than
// interpolated in the template so the value never lands in a CSS context the
// template escaper has to guess at.
func HostBarStyle(percent int) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`style="width: %d%%"`, hostinfo.Bar(percent)))
}

// HostBarHeight is HostBarStyle for the standing mini bars of the float card.
func HostBarHeight(percent int) template.HTMLAttr {
	return template.HTMLAttr(fmt.Sprintf(`style="height: %d%%"`, hostinfo.Bar(percent)))
}
