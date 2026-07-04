package notify

import "path/filepath"

// StorePath returns the shared notification list file. Like the recent
// projects store it lives directly in the state dir; separate lists come
// from separate state dirs.
func StorePath(stateDir string) string {
	return filepath.Join(stateDir, "notifications.json")
}

// InboxDir returns the directory event files for one coder are dropped into,
// the ingestion seam kept next to the store but clearly separate from it.
// Claude Code hooks (injected via --settings when a coder starts) write
// there; for other coders it is the generic seam, used by the e2e suite.
func InboxDir(stateDir, coderID string) string {
	return filepath.Join(stateDir, "notification-inbox", coderID)
}
