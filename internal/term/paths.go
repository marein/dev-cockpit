package term

import (
	"os"
	"path/filepath"
)

// RuntimeDir returns the per-provider directory that holds one socket and pid
// file per live session. It is created if missing.
func RuntimeDir(provider string) (string, error) {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = filepath.Join(os.TempDir(), "dev-cockpit-"+sanitizeFile(currentUser()))
	} else {
		base = filepath.Join(base, "dev-cockpit")
	}
	dir := filepath.Join(base, sanitizeFile(provider))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func socketPath(dir, key string) string { return filepath.Join(dir, key+".sock") }
func pidPath(dir, key string) string    { return filepath.Join(dir, key+".pid") }

func currentUser() string {
	if u := os.Getenv("USER"); u != "" {
		return u
	}
	return "user"
}

func sanitizeFile(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			out = append(out, r)
		default:
			out = append(out, '_')
		}
	}
	if len(out) == 0 {
		return "default"
	}
	return string(out)
}
