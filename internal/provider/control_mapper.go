package provider

import "strings"

// ControlMapper translates UI control IDs to key names (e.g. "Up", "C-c") which
// the session agent renders into the bytes a program reads from its terminal.
type ControlMapper interface {
	Map(raw string) (string, bool)
}

type defaultControlMapper struct{}

func DefaultControlMapper() ControlMapper { return defaultControlMapper{} }

var defaultControlKeys = map[string]string{
	"arrow-up":    "Up",
	"arrow-down":  "Down",
	"arrow-right": "Right",
	"arrow-left":  "Left",
	"escape":      "Escape",
	"backspace":   "BSpace",
	"page-up":     "PageUp",
	"page-top":    "Home",
	"page-bottom": "End",
	"page-down":   "PageDown",
	"enter":       "Enter",
	"space":       "Space",
	"tab":         "Tab",
	"shift-tab":   "BTab",
}

func (defaultControlMapper) Map(raw string) (string, bool) {
	return MapControlKey(raw, defaultControlKeys)
}

func MapControlKey(raw string, controlKeys map[string]string) (string, bool) {
	key := strings.ToLower(strings.TrimSpace(raw))
	if mapped, ok := controlKeys[key]; ok {
		return mapped, true
	}
	var prefixes []string
	hasMeta := false
	for {
		switch {
		case strings.HasPrefix(key, "ctrl-"):
			prefixes = append(prefixes, "C-")
			key = strings.TrimPrefix(key, "ctrl-")
		case strings.HasPrefix(key, "alt-"):
			if !hasMeta {
				prefixes = append(prefixes, "M-")
				hasMeta = true
			}
			key = strings.TrimPrefix(key, "alt-")
		case strings.HasPrefix(key, "meta-"):
			if !hasMeta {
				prefixes = append(prefixes, "M-")
				hasMeta = true
			}
			key = strings.TrimPrefix(key, "meta-")
		default:
			base, ok := controlBaseKey(key, controlKeys)
			if !ok || len(prefixes) == 0 {
				return "", false
			}
			return strings.Join(prefixes, "") + base, true
		}
	}
}

func controlBaseKey(key string, controlKeys map[string]string) (string, bool) {
	if mapped, ok := controlKeys[key]; ok {
		return mapped, true
	}
	if len(key) != 1 {
		return "", false
	}
	r := key[0]
	if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || strings.ContainsRune("`-=[]\\;',./", rune(r)) {
		return key, true
	}
	return "", false
}
