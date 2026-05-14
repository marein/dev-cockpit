package tmux

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// StreamLog manages the per-session pipe-pane log file.
type StreamLog struct {
	root string
}

// NewStreamLog returns a StreamLog backed by the given directory.
func NewStreamLog(root string) *StreamLog { return &StreamLog{root: root} }

// Path returns the log path for the named session, creating the root if needed.
func (s *StreamLog) Path(name string) (string, error) {
	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return "", err
	}
	return filepath.Join(s.root, name+".log"), nil
}

// Truncate empties the log before attaching pipe-pane for live streams.
func (s *StreamLog) Truncate(name string) (string, error) {
	p, err := s.Path(name)
	if err != nil {
		return "", err
	}
	return p, os.WriteFile(p, nil, 0o644)
}

// Remove deletes the log on close.
func (s *StreamLog) Remove(name string) {
	if p, err := s.Path(name); err == nil {
		_ = os.Remove(p)
	}
}

// Rename moves a session log to a different stable key.
func (s *StreamLog) Rename(oldName, newName string) error {
	if oldName == newName {
		return nil
	}
	oldPath, err := s.Path(oldName)
	if err != nil {
		return err
	}
	newPath, err := s.Path(newName)
	if err != nil {
		return err
	}
	if err := os.Rename(oldPath, newPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	return nil
}

// Delta returns the raw bytes written past offset along with the new total
// size. Callers stream the result through an OSCFilter so escape sequences
// split across reads are handled correctly.
func (s *StreamLog) Delta(path string, offset int64) ([]byte, int64, error) {
	fi, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	size := fi.Size()
	if size < offset {
		return nil, size, nil
	}
	if size == offset {
		return nil, offset, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}
	defer f.Close()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, 0, err
	}
	buf, err := io.ReadAll(f)
	if err != nil {
		return nil, 0, err
	}
	return buf, size, nil
}

func stripSnapshotCursor(src []byte) []byte {
	return bytes.ReplaceAll(src, []byte("\x1b[7m \x1b[0m"), []byte(" "))
}
