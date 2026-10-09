package pluginhost

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
)

type assistantMemory struct {
	files     fs.FS
	overwrite bool
}

type settingValues struct {
	values    map[string]string
	overwrite bool
}

type SettingStore interface {
	Lookup(key string) (string, bool)
	Set(key, value string)
}

// ApplySettings writes every plugin's settings into store, in plugin and call
// order, keys sorted within one call. Without overwrite an existing key is kept.
func ApplySettings(serves []*Serve, store SettingStore) {
	for _, s := range serves {
		for _, d := range s.settings {
			keys := make([]string, 0, len(d.values))
			for key := range d.values {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				if _, ok := store.Lookup(key); ok && !d.overwrite {
					log.Printf("plugin %s: settings key %s kept", s.id, key)
					continue
				}
				store.Set(key, d.values[key])
				log.Printf("plugin %s: settings key %s written", s.id, key)
			}
		}
	}
}

// ApplyAssistantMemory writes every plugin's memory files into memoryDir, in
// plugin and call order. A file isMemoryFile refuses or tooLong flags is
// skipped and logged. Without overwrite an existing file is kept.
func ApplyAssistantMemory(serves []*Serve, memoryDir string, isMemoryFile func(name string) bool, tooLong func(content []byte) bool) {
	for _, s := range serves {
		for _, d := range s.memory {
			err := fs.WalkDir(d.files, ".", func(name string, entry fs.DirEntry, err error) error {
				if err != nil {
					log.Printf("plugin %s: assistant memory %s: %v", s.id, name, err)
					return nil
				}
				if entry.IsDir() {
					return nil
				}
				if !isMemoryFile(name) {
					log.Printf("plugin %s: assistant memory %s skipped, the memory reads only top level <slug>.md files", s.id, name)
					return nil
				}
				content, err := fs.ReadFile(d.files, name)
				if err != nil {
					log.Printf("plugin %s: assistant memory %s: %v", s.id, name, err)
					return nil
				}
				if tooLong(content) {
					log.Printf("plugin %s: assistant memory %s skipped, longer than the memory takes", s.id, name)
					return nil
				}
				s.apply("assistant memory "+name, filepath.Join(memoryDir, name), content, d.overwrite)
				return nil
			})
			if err != nil {
				log.Printf("plugin %s: assistant memory: %v", s.id, err)
			}
		}
	}
}

// apply writes one default and logs the outcome.
func (s *Serve) apply(label, path string, content []byte, overwrite bool) {
	written, err := writeDefault(path, content, overwrite)
	switch {
	case err != nil:
		log.Printf("plugin %s: %s: %v", s.id, label, err)
	case written:
		log.Printf("plugin %s: %s written to %s", s.id, label, path)
	default:
		log.Printf("plugin %s: %s kept at %s", s.id, label, path)
	}
}

// tempPattern names the temporary file of writeDefault, never a memory file name.
const tempPattern = ".plugin-default-*.tmp"

// writeDefault writes content to a temporary file next to path and publishes
// it, without overwrite by a link that fails on an existing file, with
// overwrite by a rename. It reports whether it wrote.
func writeDefault(path string, content []byte, overwrite bool) (bool, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), tempPattern)
	if err != nil {
		return false, err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return false, err
	}
	if err := tmp.Close(); err != nil {
		return false, err
	}
	if overwrite {
		if err := os.Rename(tmp.Name(), path); err != nil {
			return false, err
		}
		return true, nil
	}
	if err := os.Link(tmp.Name(), path); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
