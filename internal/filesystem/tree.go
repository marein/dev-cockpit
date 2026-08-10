package filesystem

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
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
	slashed := strings.ReplaceAll(strings.TrimSpace(rel), "\\", "/")
	// A path that walks out of the project is refused, and refused on purpose:
	// the clean below would otherwise bend "../../etc/passwd" into "etc/passwd"
	// inside the project and answer for whatever happens to be there. Until the
	// symlink check learned to walk up to an existing ancestor, this was said by
	// accident, because the parent of such a path is usually not on the disk.
	if escaped := path.Clean(slashed); escaped == ".." || strings.HasPrefix(escaped, "../") {
		return "", errors.New("Path escapes the project directory.")
	}
	cleaned := path.Clean("/" + slashed)
	target := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(cleaned, "/")))
	if !IsUnder(target, root) {
		return "", errors.New("Path escapes the project directory.")
	}
	if err := ensureNoSymlinkEscape(target, root); err != nil {
		return "", err
	}
	return target, nil
}

// ensureNoSymlinkEscape resolves symlinks on target and verifies the real path
// is still under root. A path that is not on the disk is checked at its nearest
// existing ancestor: that is the same question one level up, and it is the only
// one that can be asked at all. Walking up rather than stopping at the direct
// parent is what lets the git routes ask about a path the repository still has
// and the disk does not, a deleted file inside a deleted folder for instance,
// which they never touch on the disk anyway.
func ensureNoSymlinkEscape(target, root string) error {
	check := target
	for {
		if _, err := os.Lstat(check); err == nil {
			break
		}
		parent := filepath.Dir(check)
		if parent == check || !IsUnder(parent, root) {
			check = root
			break
		}
		check = parent
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

// ReadFileText reads a regular file for editing and answers the version token
// of exactly the bytes it returns, which is what the save carries back. The
// token is taken from those bytes and never from a second look at the disk: a
// write landing between the read and that look would hand the caller a token
// for content it never saw. It rejects directories, files over
// MaxEditableBytes, and binary content.
func ReadFileText(root, rel string) (content string, version string, err error) {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(target)
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", errors.New("Only regular files can be edited.")
	}
	if info.Size() > MaxEditableBytes {
		return "", "", ErrTooLarge
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return "", "", err
	}
	if err := CheckEditableText(data); err != nil {
		return "", "", err
	}
	return string(data), versionOf(data), nil
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

// MaxListedFiles caps the recursive file list ListFilesUnder returns.
const MaxListedFiles = 5000

// ListFilesUnder walks one directory inside root and answers the relative paths
// of every regular file in it. An empty rel walks the whole project.
//
// The excluded directories are skipped, symlinked directories are not followed,
// and the walk stops after MaxListedFiles files and reports truncation. The
// paths stay relative to root, which is what every client path is, so a caller
// that only cares about a subtree pays for that subtree and the cap counts
// there.
//
// The quick open palette does not use this: it answers from an index of the
// whole project instead, because a capped list cannot find what sits past the
// cap. See QuickOpenCache.
func ListFilesUnder(root, rel string, ex Exclusions) (files []string, truncated bool, err error) {
	dir, err := ResolveUnder(root, rel)
	if err != nil {
		return nil, false, err
	}
	files = []string{}
	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if d.IsDir() {
			if p != dir && ex.SkipDir(relTo(root, p), d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(files) >= MaxListedFiles {
			truncated = true
			return filepath.SkipAll
		}
		files = append(files, relTo(root, p))
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	sort.Strings(files)
	return files, truncated, nil
}

// ErrExists reports a name that is already taken. The editor answers it with a
// 409 so the browser can offer to overwrite instead of showing a dead end.
var ErrExists = errors.New("A file or folder with that name already exists.")

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
			return Entry{}, ErrExists
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

// MoveEntry moves the file or directory at root/rel into the directory
// root/dirRel, keeping its base name. An empty dirRel is the project root.
// Without overwrite a taken name is reported as ErrExists, with it the file
// there is replaced.
func MoveEntry(root, rel, dirRel string, overwrite bool) (Entry, error) {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return Entry{}, err
	}
	if filepath.Clean(target) == filepath.Clean(root) {
		return Entry{}, errors.New("Refusing to move the project root.")
	}
	info, err := os.Lstat(target)
	if err != nil {
		return Entry{}, errors.New("File or folder not found.")
	}
	dir, err := ResolveUnder(root, dirRel)
	if err != nil {
		return Entry{}, err
	}
	dirInfo, err := os.Stat(dir)
	if err != nil || !dirInfo.IsDir() {
		return Entry{}, errors.New("Target folder not found.")
	}
	dest := filepath.Join(dir, filepath.Base(target))
	if dest == target {
		return entryFromInfo(info, relTo(root, target)), nil
	}
	// A directory cannot swallow itself, os.Rename would happily create the loop.
	if info.IsDir() && IsUnder(dir, target) {
		return Entry{}, errors.New("Cannot move a folder into itself.")
	}
	if existing, err := os.Lstat(dest); err == nil {
		if !overwrite {
			return Entry{}, ErrExists
		}
		// Renaming onto a directory fails, and dropping a whole tree to make room
		// is not something a drag should do silently.
		if existing.IsDir() || info.IsDir() {
			return Entry{}, errors.New("A folder with that name is already there.")
		}
	}
	if err := os.Rename(target, dest); err != nil {
		return Entry{}, err
	}
	moved, err := os.Stat(dest)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(moved, relTo(root, dest)), nil
}

// WriteTarGz streams dir as a gzipped tar, with prefix as the single top level
// folder inside the archive. Only directories and regular files go in, anything
// else (symlinks, sockets) is skipped so the archive stays inside the project.
func WriteTarGz(w io.Writer, dir, prefix string) error {
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	walkErr := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if !d.IsDir() && !info.Mode().IsRegular() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		name := prefix
		if rel != "." {
			name = path.Join(prefix, filepath.ToSlash(rel))
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = name
		if d.IsDir() {
			header.Name += "/"
		}
		if err := tw.WriteHeader(header); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		_, err = io.Copy(tw, f)
		return err
	})
	if walkErr != nil {
		_ = tw.Close()
		_ = gz.Close()
		return walkErr
	}
	if err := tw.Close(); err != nil {
		_ = gz.Close()
		return err
	}
	return gz.Close()
}

// Extract limits keep a crafted archive from filling the disk.
const (
	maxExtractBytes = 1 << 30 // 1 GiB unpacked
	maxExtractFiles = 20000
)

// ArchiveExt reports whether the name looks like an archive the editor can
// unpack, and returns the name without that extension.
func ArchiveExt(name string) (string, bool) {
	lower := strings.ToLower(name)
	for _, ext := range []string{".tar.gz", ".tgz", ".tar", ".zip"} {
		if strings.HasSuffix(lower, ext) && len(lower) > len(ext) {
			return name[:len(name)-len(ext)], true
		}
	}
	return "", false
}

// ExtractArchive unpacks root/rel into a new folder next to it, named after the
// archive. The folder name is free by construction, so nothing is overwritten.
// Only directories and regular files are written; anything else in the archive
// (symlinks, devices, paths that would leave the folder) is skipped.
func ExtractArchive(root, rel string) (Entry, error) {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return Entry{}, err
	}
	info, err := os.Stat(target)
	if err != nil || !info.Mode().IsRegular() {
		return Entry{}, errors.New("File not found.")
	}
	stem, ok := ArchiveExt(filepath.Base(target))
	if !ok {
		return Entry{}, errors.New("That file is not a tar, tar.gz or zip archive.")
	}
	dest, err := freeName(filepath.Join(filepath.Dir(target), stem))
	if err != nil {
		return Entry{}, err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return Entry{}, err
	}
	if strings.HasSuffix(strings.ToLower(target), ".zip") {
		err = extractZip(target, dest)
	} else {
		err = extractTar(target, dest)
	}
	if err != nil {
		_ = os.RemoveAll(dest)
		return Entry{}, err
	}
	out, err := os.Stat(dest)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(out, relTo(root, dest)), nil
}

// freeName returns base, or base with a counter, whichever is not taken yet.
func freeName(base string) (string, error) {
	for i := 1; i < 100; i++ {
		candidate := base
		if i > 1 {
			candidate = fmt.Sprintf("%s %d", base, i)
		}
		if _, err := os.Lstat(candidate); err != nil {
			return candidate, nil
		}
	}
	return "", errors.New("Too many folders with that name already.")
}

// extractPath joins the entry name onto dest and refuses anything that would
// land outside it.
func extractPath(dest, name string) (string, bool) {
	clean := path.Clean("/" + strings.ReplaceAll(name, "\\", "/"))
	out := filepath.Join(dest, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	if out == dest || !IsUnder(out, dest) {
		return "", false
	}
	return out, true
}

func extractTar(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	var reader io.Reader = f
	if !strings.HasSuffix(strings.ToLower(archive), ".tar") {
		gz, err := gzip.NewReader(f)
		if err != nil {
			return errors.New("That file is not a readable tar.gz archive.")
		}
		defer gz.Close()
		reader = gz
	}
	tr := tar.NewReader(reader)
	var written int64
	var count int
	for {
		header, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return errors.New("That file is not a readable archive.")
		}
		out, ok := extractPath(dest, header.Name)
		if !ok {
			continue
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			count++
			written += header.Size
			if count > maxExtractFiles || written > maxExtractBytes {
				return errors.New("The archive is too large to unpack here.")
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := writeStream(out, tr, os.FileMode(header.Mode).Perm()); err != nil {
				return err
			}
		}
	}
}

func extractZip(archive, dest string) error {
	r, err := zip.OpenReader(archive)
	if err != nil {
		return errors.New("That file is not a readable zip archive.")
	}
	defer r.Close()
	var written uint64
	var count int
	for _, entry := range r.File {
		out, ok := extractPath(dest, entry.Name)
		if !ok {
			continue
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(out, 0o755); err != nil {
				return err
			}
			continue
		}
		if !entry.FileInfo().Mode().IsRegular() {
			continue
		}
		count++
		written += entry.UncompressedSize64
		if count > maxExtractFiles || written > maxExtractBytes {
			return errors.New("The archive is too large to unpack here.")
		}
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		rc, err := entry.Open()
		if err != nil {
			return err
		}
		err = writeStream(out, rc, entry.Mode().Perm())
		_ = rc.Close()
		if err != nil {
			return err
		}
	}
	return nil
}

// writeStream writes one extracted entry, capped so a lying header cannot make
// it run away.
func writeStream(out string, src io.Reader, mode os.FileMode) error {
	if mode == 0 {
		mode = 0o644
	}
	f, err := os.OpenFile(out, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(src, maxExtractBytes))
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	return err
}

// CopyEntry copies the file or directory at root/rel into the directory
// root/dirRel. Copying into its own folder duplicates the entry under a free
// "name copy" name; anywhere else a taken name is ErrExists unless overwrite
// replaces the file there.
func CopyEntry(root, rel, dirRel string, overwrite bool) (Entry, error) {
	source, err := ResolveUnder(root, rel)
	if err != nil {
		return Entry{}, err
	}
	if filepath.Clean(source) == filepath.Clean(root) {
		return Entry{}, errors.New("Refusing to copy the project root.")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return Entry{}, errors.New("File or folder not found.")
	}
	if !info.IsDir() && !info.Mode().IsRegular() {
		return Entry{}, errors.New("Only files and folders can be copied.")
	}
	dir, err := ResolveUnder(root, dirRel)
	if err != nil {
		return Entry{}, err
	}
	if dirInfo, err := os.Stat(dir); err != nil || !dirInfo.IsDir() {
		return Entry{}, errors.New("Target folder not found.")
	}
	// A directory cannot be copied into itself, the walk would never end.
	if info.IsDir() && IsUnder(dir, source) {
		return Entry{}, errors.New("Cannot copy a folder into itself.")
	}
	dest := filepath.Join(dir, filepath.Base(source))
	if dest == source {
		if dest, err = freeCopyName(source); err != nil {
			return Entry{}, err
		}
	} else if existing, err := os.Lstat(dest); err == nil {
		if !overwrite {
			return Entry{}, ErrExists
		}
		if existing.IsDir() || info.IsDir() {
			return Entry{}, errors.New("A folder with that name is already there.")
		}
		if err := os.Remove(dest); err != nil {
			return Entry{}, err
		}
	}
	if info.IsDir() {
		err = copyTree(source, dest)
	} else {
		err = copyFile(source, dest, info.Mode().Perm())
	}
	if err != nil {
		return Entry{}, err
	}
	copied, err := os.Stat(dest)
	if err != nil {
		return Entry{}, err
	}
	return entryFromInfo(copied, relTo(root, dest)), nil
}

// freeCopyName picks "name copy", then "name copy 2" and so on, keeping the
// extension where there is one.
func freeCopyName(source string) (string, error) {
	dir := filepath.Dir(source)
	base := filepath.Base(source)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	for i := 1; i < 100; i++ {
		suffix := " copy"
		if i > 1 {
			suffix = fmt.Sprintf(" copy %d", i)
		}
		candidate := filepath.Join(dir, stem+suffix+ext)
		if _, err := os.Lstat(candidate); err != nil {
			return candidate, nil
		}
	}
	return "", errors.New("Too many copies of that name already.")
}

// copyFile writes source to dest through a temp file in the same directory, so
// a failed copy never leaves a half written file behind.
func copyFile(source, dest string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".copy-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, in); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, dest); err != nil {
		return err
	}
	cleanup = false
	return nil
}

// copyTree copies a directory recursively. Anything that is not a regular file
// or a directory (symlinks, sockets) is skipped rather than followed, so a copy
// can never reach outside the project.
func copyTree(source, dest string) error {
	return filepath.WalkDir(source, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			return os.MkdirAll(target, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyFile(p, target, info.Mode().Perm())
		default:
			return nil
		}
	})
}

// SaveUpload stores an uploaded file as dirRel/filename under root, writing to
// a temp file first and renaming into place. A taken name is reported as
// ErrExists unless overwrite replaces the file there; createDirs makes the
// missing folders on the way, which is what a folder upload needs.
func SaveUpload(root, dirRel, filename string, src io.Reader, overwrite, createDirs bool) (Entry, error) {
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
	if existing, err := os.Lstat(target); err == nil {
		if !overwrite {
			return Entry{}, ErrExists
		}
		if existing.IsDir() {
			return Entry{}, errors.New("A folder with that name is already there.")
		}
	}
	dir := filepath.Dir(target)
	// A folder upload sends one request per file and carries the tree in the
	// target path, so the folders come into being on the way.
	if createDirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return Entry{}, err
		}
	}
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

// errWriteDir is what both write paths say about a path that is a directory,
// the unconditional one here and the versioned one in version.go.
var errWriteDir = errors.New("Cannot write to a directory.")

// WriteFileText writes content to root/rel atomically, preserving the existing
// file mode when the target already exists. The parent directory must exist. It
// writes whatever is there: a caller that has to keep somebody else's work goes
// through WriteFileTextIfUnchanged.
func WriteFileText(root, rel string, content []byte) (Entry, error) {
	target, err := ResolveUnder(root, rel)
	if err != nil {
		return Entry{}, err
	}
	if info, err := os.Stat(target); err == nil && info.IsDir() {
		return Entry{}, errWriteDir
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
