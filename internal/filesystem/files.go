package filesystem

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type File struct {
	Name     string
	Path     string
	Size     int64
	SizeText string
	ModTime  string
}

type OpenedFile struct {
	File
	io.ReadCloser
}

func CleanBaseName(raw string) (string, error) {
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(raw), "\\", "/"))
	if name == "" || name == "." || name == ".." || strings.ContainsRune(name, 0) {
		return "", errors.New("File name is invalid.")
	}
	return name, nil
}

func ListFiles(dir string) ([]File, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	files := make([]File, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		files = append(files, sessionFileFromInfo(info, filepath.Join(dir, info.Name())))
	}
	sort.Slice(files, func(i, j int) bool {
		return strings.ToLower(files[i].Name) < strings.ToLower(files[j].Name)
	})
	return files, nil
}

// maxSaveNameTries bounds the hunt for a free name, so a directory that keeps
// answering "exists" (a filesystem that lies, a name that is a folder) cannot
// spin forever. A thousand copies of one name is far past what one session's
// files dialog holds.
const maxSaveNameTries = 1000

// SaveFile writes src into dir under the cleaned name and never replaces a
// file that is already there: a taken name gets a counter before its
// extension chain (image.png, image-2.png, image-3.png). The name is claimed
// with a hard link from the finished temp file, which fails when the target
// exists, so the check and the placement are one step and two uploads of one
// name that arrive together cannot both land on the same file. The File that
// comes back carries the name that was actually written.
func SaveFile(dir, rawName string, src io.Reader) (File, error) {
	name, err := CleanBaseName(rawName)
	if err != nil {
		return File{}, err
	}
	target := filepath.Join(dir, name)
	if !IsUnder(target, dir) {
		return File{}, errors.New("Refusing to access a file outside the files directory.")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return File{}, err
	}
	tmp, err := os.CreateTemp(dir, ".upload-*")
	if err != nil {
		return File{}, err
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		return File{}, err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return File{}, err
	}
	if err := tmp.Close(); err != nil {
		return File{}, err
	}
	stem, ext := splitExtChain(name)
	for try := 1; ; try++ {
		if try > 1 {
			name = stem + "-" + strconv.Itoa(try) + ext
			target = filepath.Join(dir, name)
		}
		err := os.Link(tmpName, target)
		if err == nil {
			break
		}
		if !errors.Is(err, fs.ErrExist) {
			return File{}, err
		}
		if try == maxSaveNameTries {
			return File{}, errors.New("Too many files with that name already.")
		}
	}
	info, err := os.Stat(target)
	if err != nil {
		return File{}, err
	}
	out := sessionFileFromInfo(info, target)
	out.Name = name
	return out, nil
}

// splitExtChain cuts a file name into the stem and the extension chain a
// counter has to stay in front of. The chain is the last dot part, plus every
// part before it that is one to four letters, so archive.tar.gz, bundle.min.js
// and types.d.ts stay whole, while a number or a longer word before the last
// dot stays in the stem: backup.2026.sql counts as backup.2026-2.sql and
// my.notes.txt as my.notes-2.txt. A name without a dot, or with the dot in
// front (a dotfile), has no chain and counts at its end.
func splitExtChain(name string) (stem, ext string) {
	cut := strings.LastIndexByte(name, '.')
	if cut <= 0 {
		return name, ""
	}
	for {
		prev := strings.LastIndexByte(name[:cut], '.')
		if prev <= 0 || !isShortWord(name[prev+1:cut]) {
			break
		}
		cut = prev
	}
	return name[:cut], name[cut:]
}

func isShortWord(part string) bool {
	letters := 0
	for _, r := range part {
		if !unicode.IsLetter(r) {
			return false
		}
		letters++
	}
	return letters >= 1 && letters <= 4
}

func OpenFile(dir, rawName string) (OpenedFile, error) {
	name, err := CleanBaseName(rawName)
	if err != nil {
		return OpenedFile{}, err
	}
	target := filepath.Join(dir, name)
	if !IsUnder(target, dir) {
		return OpenedFile{}, errors.New("Refusing to access a file outside the files directory.")
	}
	f, err := os.Open(target)
	if err != nil {
		return OpenedFile{}, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return OpenedFile{}, err
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return OpenedFile{}, errors.New("Only regular files can be downloaded.")
	}
	return OpenedFile{File: sessionFileFromInfo(info, target), ReadCloser: f}, nil
}

func DeleteFile(dir, rawName string) (File, error) {
	name, err := CleanBaseName(rawName)
	if err != nil {
		return File{}, err
	}
	target := filepath.Join(dir, name)
	if !IsUnder(target, dir) {
		return File{}, errors.New("Refusing to access a file outside the files directory.")
	}
	info, err := os.Stat(target)
	if err != nil {
		return File{}, err
	}
	if !info.Mode().IsRegular() {
		return File{}, errors.New("Only regular files can be deleted.")
	}
	file := sessionFileFromInfo(info, target)
	if err := os.Remove(target); err != nil {
		return File{}, err
	}
	return file, nil
}

func sessionFileFromInfo(info os.FileInfo, path string) File {
	return File{
		Name:     info.Name(),
		Path:     path,
		Size:     info.Size(),
		SizeText: HumanSize(info.Size()),
		ModTime:  info.ModTime().UTC().Format("2006-01-02 15:04:05 UTC"),
	}
}
