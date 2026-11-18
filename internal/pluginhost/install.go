package pluginhost

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
)

// Bounds of a directory Install copies.
const (
	maxInstallFiles = 5000
	maxInstallBytes = 256 << 20
	maxInstallPath  = 4096
)

// InstallError is a refusal worded for the person, as opposed to a disk
// failure.
type InstallError struct{ Message string }

func (e *InstallError) Error() string { return e.Message }

func refuse(format string, args ...any) error {
	return &InstallError{Message: fmt.Sprintf(format, args...)}
}

// Install copies a plugin directory of the host into the plugins folder
// under its manifest id, switches it on and starts it. A plugin of that id
// installed from the same path is replaced, which updates it, one from
// another source only with replace, else a ReplaceError asks first. It
// answers the id and whether a plugin was replaced. .git is not copied.
func (h *Host) Install(source string, replace bool) (string, bool, error) {
	if source == "" {
		return "", false, refuse("Enter the plugin's directory.")
	}
	if len(source) > maxInstallPath || hasControl(source) || !filepath.IsAbs(source) {
		return "", false, refuse("Enter an absolute path to a directory on the host.")
	}
	source = filepath.Clean(source)
	info, err := os.Stat(source)
	if err != nil {
		return "", false, refuse("There is no directory at that path.")
	}
	if !info.IsDir() {
		return "", false, refuse("That path is not a directory.")
	}
	root, err := os.OpenRoot(source)
	if err != nil {
		return "", false, refuse("The directory could not be opened.")
	}
	m, err := readManifest(root)
	if err == nil {
		err = checkRun(root, m)
	}
	_ = root.Close()
	if err != nil {
		return "", false, refuse("%s", err.Error())
	}
	if h.compiled[m.ID] {
		return "", false, refuse("A built in plugin has the id %s.", m.ID)
	}

	h.ops.Lock()
	defer h.ops.Unlock()
	if err := os.MkdirAll(h.Dir(), 0o700); err != nil {
		return "", false, err
	}
	real, err := filepath.EvalSymlinks(source)
	if err != nil {
		return "", false, refuse("There is no directory at that path.")
	}
	folder, err := filepath.EvalSymlinks(h.Dir())
	if err != nil {
		return "", false, err
	}
	if within(real, folder) || within(folder, real) {
		return "", false, refuse("The directory is in the plugins folder already.")
	}
	h.mu.Lock()
	old := h.entries[m.ID]
	h.mu.Unlock()
	if rec := h.origins()[m.ID]; old != nil && (rec == nil || rec.Path != source) && !replace {
		return "", false, &ReplaceError{ID: m.ID, From: originLabel(rec)}
	}
	staging := filepath.Join(h.Dir(), ".install-"+randomHex(6))
	if err := copyTree(real, staging); err != nil {
		_ = os.RemoveAll(staging)
		return "", false, err
	}
	if old != nil {
		h.unload(old)
	}
	if err := h.removeDir(m.ID); err != nil {
		_ = os.RemoveAll(staging)
		return "", false, err
	}
	if err := os.Rename(staging, filepath.Join(h.Dir(), m.ID)); err != nil {
		_ = os.RemoveAll(staging)
		return "", false, err
	}
	if err := h.changeState(func(st *hostState) {
		st.Disabled = slices.DeleteFunc(st.Disabled, func(listed string) bool { return listed == m.ID })
		st.Origins[m.ID] = &origin{Path: source}
	}); err != nil {
		return "", false, err
	}
	h.keepVersions(m.ID)
	h.clearCheck(m.ID)
	h.reload(m.ID)
	h.changed()
	return m.ID, old != nil, nil
}

func within(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && (rel == "." || filepath.IsLocal(rel))
}

// copyTree copies a directory with its modes. Links stay links, sockets and
// devices are skipped, .git is left out.
func copyTree(from, to string) error {
	files, size := 0, int64(0)
	return filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return refuse("The directory could not be read.")
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, rel)
		if d.IsDir() && d.Name() == ".git" && rel != "." {
			return filepath.SkipDir
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm()|0o700)
		case info.Mode()&fs.ModeSymlink != 0:
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			return os.Symlink(link, target)
		case info.Mode().IsRegular():
			files++
			size += info.Size()
			if files > maxInstallFiles || size > maxInstallBytes {
				return refuse("The directory has more than %d files or 256 MiB.", maxInstallFiles)
			}
			return copyFile(path, target, info.Mode().Perm())
		default:
			return nil
		}
	})
}

func copyFile(from, to string, mode fs.FileMode) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// IsInstallError answers whether err is a refusal worded for the person.
func IsInstallError(err error) bool {
	var refusal *InstallError
	return errors.As(err, &refusal)
}
