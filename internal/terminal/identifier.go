package terminal

import (
	"errors"
	"regexp"
	"strings"

	"github.com/local/dev-cockpit/internal/filesystem"
)

var identifierPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

// SanitizeName converts a user-supplied display name into a tmux-safe slug.
func SanitizeName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("ID is required.")
	}
	out := filesystem.ToDirectoryName(name)
	if out == "" {
		return "", errors.New("ID must include at least one letter or number.")
	}
	return out, nil
}

// ValidateIdentifier ensures raw matches the strict tmux-safe alphabet.
func ValidateIdentifier(raw string) (string, error) {
	id := strings.TrimSpace(raw)
	if !identifierPattern.MatchString(id) {
		return "", errors.New("Invalid ID.")
	}
	return id, nil
}
