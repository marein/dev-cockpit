package claude

import (
	"strings"

	"github.com/local/dev-cockpit/internal/terminal"
)

type controlMapper struct {
	base terminal.ControlMapper
}

func (c controlMapper) Map(raw string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "page-top":
		return "C-Home", true
	case "page-bottom":
		return "C-End", true
	default:
		return c.base.Map(raw)
	}
}
