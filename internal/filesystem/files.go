package filesystem

import (
	"errors"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
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
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
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
	if err := os.Rename(tmpName, target); err != nil {
		return File{}, err
	}
	cleanup = false
	info, err := os.Stat(target)
	if err != nil {
		return File{}, err
	}
	out := sessionFileFromInfo(info, target)
	out.Name = name
	return out, nil
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
