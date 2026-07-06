// DisplayName and ShortID format fallback labels for coder sessions.
package coder

import "strings"

// DisplayName returns name when present, otherwise a stable short fallback.
func DisplayName(name, sessionID string) string {
	name = strings.TrimSpace(name)
	if name != "" {
		return name
	}
	return "coder-" + ShortID(sessionID)
}

// ShortID returns a compact form of an identifier for display.
func ShortID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) > 8 {
		return id[:8]
	}
	return id
}
