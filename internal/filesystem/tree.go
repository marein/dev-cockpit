package filesystem

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// MaxEditableBytes caps how large a file may be to be read into the editor.
const MaxEditableBytes = 2 << 20 // 2 MiB

// Entry is one item (file or directory) inside a project tree. RelPath is the
// slash-separated path relative to the tree root and is what the client sends
// back to identify the item.
type Entry struct {
	Name     string `json:"name"`
	RelPath  string `json:"path"`
	IsDir    bool   `json:"isDir"`
	Size     int64  `json:"size"`
	SizeText string `json:"sizeText"`
	ModTime  string `json:"modTime"`
}

// ResolveUnder cleans a slash-separated, client-supplied relative path and joins
// it onto root, refusing anything that would escape root (lexically or via
// symlinks). An empty rel resolves to root itself.
func ResolveUnder(root, rel string) (string, error) {
	cleaned := path.Clean("/" + strings.ReplaceAll(strings.TrimSpace(rel), "\\", "/"))
	target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))
	if !IsUnder(target, root) {
		return "", errors.New("Path escapes the project directory.")
	}
	if err := ensureNoSymlinkEscape(target, root); err != nil {
		return "", err
	}
	return target, nil
}

// ensureNoSymlinkEscape resolves symlinks on target (or its parent when target
// does not exist yet) and verifies the real path is still under root.
func ensureNoSymlinkEscape(target, root string) error {
	check := target
	if _, err := os.Lstat(target); err != nil {
		check = filepath.Dir(target)
	}
	resolved, err := filepath.EvalSymlinks(check)
	if err != nil {
		return errors.New("Path could not be resolved.")
	}
	if !IsUnder(resolved, root) {
		return errors.New("Path escapes the project directory.")
	}
	return nil
}

// relTo returns the slash-separated path of full relative to root.
func relTo(root, full string) string {
	rel, err := filepath.Rel(root, full)
	if err != nil {
		return ""
	}
	if rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// ListDir returns the directories and regular files directly inside root/rel,
// directories first, then files, each ordered case-insensitively by name.
func ListDir(root, rel string) ([]Entry, error) {
	dir, err := ResolveUnder(root, rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return nil, errors.New("Not a directory.")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]Entry, 0, len(entries))
	for _, e := range entries {
		fi, err := e.Info()
		if err != nil {
			continue
		}
		if !fi.IsDir() && !fi.Mode().IsRegular() {
			continue
		}
		full := filepath.Join(dir, fi.Name())
		out = append(out, entryFromInfo(fi, relTo(root, full)))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// ErrTooLarge and ErrBinary mark files the browser editor cannot edit; callers
// can offer a viewer or a download instead of a plain error.
var (
	ErrTooLarge = errors.New("File is too large to edit in the browser.")
	ErrBinary   = errors.New("Binary files cannot be edited.")
)

// CheckEditableText rejects content the browser editor cannot handle: anything
// over MaxEditableBytes and binary data (containing a NUL byte).
func CheckEditableText(data []byte) error {
	if len(data) > MaxEditableBytes {
		return ErrTooLarge
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return ErrBinary
	}
	return nil
}

// ReadFileText reads a regular file for editing. It rejects directories, files
// over MaxEditableBytes, and binary content.
func ReadFileText(root, rel string) (string, error) {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", errors.New("Only regular files can be edited.")
	}
	if info.Size() > MaxEditableBytes {
		return "", ErrTooLarge
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", err
	}
	if err := CheckEditableText(data); err != nil {
		return "", err
	}
	return string(data), nil
}

// ResolveExistingFile resolves rel to an existing regular file under root and
// returns its absolute path along with its file info.
func ResolveExistingFile(root, rel string) (string, os.FileInfo, error) {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return "", nil, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", nil, err
	}
	if !info.Mode().IsRegular() {
		return "", nil, errors.New("Only regular files can be served.")
	}
	return target, info, nil
}

// MaxListedFiles caps the recursive file list used by the quick open palette.
const MaxListedFiles = 5000

// skippedDirs are directories the recursive file listing never descends into.
var skippedDirs = map[string]bool{".git": true, "node_modules": true, ".worktrees": true}

// ListFilesRecursive returns the relative paths of every regular file under
// root, skipping VCS and dependency directories. Symlinked directories are not
// followed. The walk stops after MaxListedFiles entries and reports truncation.
func ListFilesRecursive(root string) ([]string, bool, error) {
	out := []string{}
	truncated := false
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != root && skippedDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(out) >= MaxListedFiles {
			truncated = true
			return filepath.SkipAll
		}
		out = append(out, relTo(root, p))
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Strings(out)
	return out, truncated, nil
}

// RenameEntry renames the file or directory at root/rel to newName inside the
// same parent directory. newName must be a bare name without path separators.
func RenameEntry(root, rel, newName string) (Entry, error) {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return Entry{}, err
	}
	if filepath.Clean(target) == filepath.Clean(root) {
		return Entry{}, errors.New("Refusing to rename the project root.")
	}
	if _, err := os.Lstat(target); err != nil {
		return Entry{}, errors.New("File or folder not found.")
	}
	name := strings.TrimSpace(newName)
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\") || strings.ContainsRune(name, 0) {
		return Entry{}, errors.New("Please enter a plain name without slashes.")
	}
	dest := filepath.Join(filepath.Dir(target), name)
	if !IsUnder(dest, root) {
		return Entry{}, errors.New("Path escapes the project directory.")
	}
	if dest != target {
		if _, err := os.Lstat(dest); err == nil {
			return Entry{}, errors.New("A file or folder with that name already exists.")
		}
	}
	if err := os.Rename(target, dest); err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(dest)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(info, relTo(root, dest)), nil
}

// SaveUpload stores an uploaded file as dirRel/filename under root, writing to
// a temp file first and renaming into place. It refuses to overwrite.
func SaveUpload(root, dirRel, filename string, src io.Reader) (Entry, error) {
	name, err := CleanBaseName(filename)
	if err != nil {
		return Entry{}, err
	}
	rel := name
	if strings.TrimSpace(dirRel) != "" {
		rel = strings.TrimSuffix(dirRel, "/") + "/" + name
	}
	target, err := resolveForCreate(root, rel)
	if err != nil {
		return Entry{}, err
	}
	if _, err := os.Lstat(target); err == nil {
		return Entry{}, errors.New("A file or folder with that name already exists.")
	}
	dir := filepath.Dir(target)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Entry{}, errors.New("Target directory does not exist.")
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return Entry{}, err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return Entry{}, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return Entry{}, err
	}
	if err := tmp.Close(); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return Entry{}, err
	}
	cleanup = false
	info, err := os.Stat(target)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(info, relTo(root, target)), nil
}

// WriteFileText writes content to root/rel atomically, preserving the existing
// file mode when the target already exists. The parent directory must exist.
func WriteFileText(root, rel string, content []byte) (Entry, error) {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return Entry{}, err
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return Entry{}, errors.New("Cannot write to a directory.")
	}
	dir := filepath.Dir(target)
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return Entry{}, errors.New("Target directory does not exist.")
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(target); err == nil {
		mode = info.Mode().Perm()
	}
	tmp, err := os.CreateTemp(dir, ".edit-*")
	if err != nil {
		return Entry{}, err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, bytes.NewReader(content)); err != nil {
		_ = tmp.Close()
		return Entry{}, err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return Entry{}, err
	}
	if err := tmp.Close(); err != nil {
		return Entry{}, err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return Entry{}, err
	}
	cleanup = false
	info, err := os.Stat(target)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(info, relTo(root, target)), nil
}

// DeleteEntry removes a file or directory (recursively) under root. It refuses
// to delete the root itself.
func DeleteEntry(root, rel string) (Entry, error) {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return Entry{}, err
	}
	if filepath.Clean(target) == filepath.Clean(root) {
		return Entry{}, errors.New("Refusing to delete the project root.")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return Entry{}, err
	}
	entry := entryFromInfo(info, relTo(root, target))
	if err := os.RemoveAll(target); err != nil {
		return Entry{}, err
	}
	return entry, nil
}

// resolveForCreate is like ResolveUnder but tolerates a not-yet-existing target
// (and missing intermediate directories): it verifies the lexical path stays
// under root and that the nearest existing ancestor does not symlink out.
func resolveForCreate(root, rel string) (string, error) {
	slashed := strings.ReplaceAll(strings.TrimSpace(rel), "\\", "/")
	for _, seg := range strings.Split(slashed, "/") {
		if seg == ".." {
			return "", errors.New("Path must not contain \"..\".")
		}
	}
	cleaned := path.Clean("/" + slashed)
	target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))
	if target == filepath.Clean(root) || !IsUnder(target, root) {
		return "", errors.New("Path escapes the project directory.")
	}
	for anc := target; IsUnder(anc, root); anc = filepath.Dir(anc) {
		if _, err := os.Lstat(anc); err != nil {
			continue
		}
		resolved, err := filepath.EvalSymlinks(anc)
		if err != nil {
			return "", errors.New("Path could not be resolved.")
		}
		if !IsUnder(resolved, root) {
			return "", errors.New("Path escapes the project directory.")
		}
		break
	}
	return target, nil
}

// CreateFile creates an empty regular file at root/rel, creating any missing
// parent directories. It fails if the path already exists.
func CreateFile(root, rel string) (Entry, error) {
	target, err := resolveForCreate(root, rel)
	if err != nil {
		return Entry{}, err
	}
	if _, err := os.Lstat(target); err == nil {
		return Entry{}, errors.New("A file or folder with that name already exists.")
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return Entry{}, err
	}
	f, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return Entry{}, err
	}
	_ = f.Close()
	info, err := os.Stat(target)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(info, relTo(root, target)), nil
}

// CreateDir creates a directory at root/rel (and any missing parents). It fails
// if the path already exists.
func CreateDir(root, rel string) (Entry, error) {
	target, err := resolveForCreate(root, rel)
	if err != nil {
		return Entry{}, err
	}
	if _, err := os.Lstat(target); err == nil {
		return Entry{}, errors.New("A file or folder with that name already exists.")
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(target)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(info, relTo(root, target)), nil
}

func entryFromInfo(info os.FileInfo, rel string) Entry {
	e := Entry{
		Name:    info.Name(),
		RelPath: rel,
		IsDir:   info.IsDir(),
		ModTime: info.ModTime().UTC().Format("2006-01-02 15:04:05 UTC"),
	}
	if !info.IsDir() {
		e.Size = info.Size()
		e.SizeText = HumanSize(info.Size())
	}
	return e
}
