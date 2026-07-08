// Package statefile reads and writes the small JSON state files in the
// dev-cockpit state directory. Reads go through the file on every call and
// writes are atomic via a temp file rename, so several processes sharing a
// state dir stay consistent and a fresh process picks up the latest values.
// A file that exists but does not parse is quarantined as <path>.broken and
// treated as absent, so a later save can never silently overwrite state that
// is still recoverable from the broken file.
package statefile

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"
)

// Load unmarshals path into v. A missing or empty file leaves v untouched,
// a corrupt file is quarantined and logged.
func Load(path string, v any) {
	data, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			log.Printf("statefile: read %s: %v", path, err)
		}
		return
	}
	if len(data) == 0 {
		return
	}
	if err := json.Unmarshal(data, v); err != nil {
		broken := path + ".broken"
		if renameErr := os.Rename(path, broken); renameErr != nil {
			log.Printf("statefile: %s is corrupt (%v) and could not be quarantined: %v", path, err, renameErr)
			return
		}
		log.Printf("statefile: %s is corrupt (%v), quarantined as %s", path, err, broken)
	}
}

// Save writes v to path atomically with the given file mode.
func Save(path string, mode os.FileMode, v any) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Printf("statefile: create dir for %s: %v", path, err)
		return
	}
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("statefile: marshal %s: %v", path, err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		log.Printf("statefile: write %s: %v", path, err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		log.Printf("statefile: replace %s: %v", path, err)
	}
}

// NewID returns a short random identifier for state entries.
func NewID() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000000000")))
	}
	return hex.EncodeToString(raw[:])
}
