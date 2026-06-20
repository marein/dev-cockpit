package filesystem

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// IsUnder reports whether path is root itself or inside root.
func IsUnder(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func HomeDir() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return h, nil
}

func ExpandHome(p string) (string, error) {
	h, err := HomeDir()
	if err != nil {
		return "", err
	}
	if len(p) > 0 && p[0] == '~' {
		return filepath.Join(h, p[1:]), nil
	}
	return p, nil
}

// HumanSize formats a byte count for display; negative counts are "unknown".
func HumanSize(n int64) string {
	if n < 0 {
		return "unknown"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for x := n / unit; x >= unit; x /= unit {
		div *= unit
		exp++
	}
	val := float64(n) / float64(div)
	suffix := "KMGTPE"[exp]
	if val >= 10 {
		return fmt.Sprintf("%.0f%c", val, suffix)
	}
	return fmt.Sprintf("%.1f%c", val, suffix)
}

// ToDirectoryName normalizes user input to a lowercase alnum-dash slug.
// Invalid characters collapse to '-', and invalid/empty inputs become "".
func ToDirectoryName(raw string) string {
	var b strings.Builder
	prevDash, hasAlnum := false, false
	for _, r := range strings.ToLower(strings.TrimSpace(raw)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevDash = false
			hasAlnum = true
		case r == '-':
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			if !prevDash {
				b.WriteByte('-')
				prevDash = true
			}
		}
	}
	out := strings.Trim(b.String(), "-")
	if !hasAlnum {
		return ""
	}
	return out
}
