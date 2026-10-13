package filesystem

import (
	"os"
	"path/filepath"
	"testing"
)

// A missing directory is made, a closed one of this user is taken, and a
// loose one, a link or a file standing in its place is refused.
func TestPrivateDirTakesOnlyARealDirectoryOfThisUser(t *testing.T) {
	root := t.TempDir()
	made := filepath.Join(root, "parent", "made")
	if err := PrivateDir(made); err != nil {
		t.Fatal(err)
	}
	closed := filepath.Join(root, "closed")
	if err := os.Mkdir(closed, 0o500); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(closed); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{made, closed} {
		info, err := os.Lstat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Fatalf("%s reads %v, want a directory with 0700", dir, info.Mode())
		}
	}
	loose := filepath.Join(root, "loose")
	if err := os.Mkdir(loose, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(loose, 0o777); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(loose); err == nil {
		t.Fatal("a directory that stood open was taken as the private directory")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(made, link); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(link); err == nil {
		t.Fatal("a link was taken as the private directory")
	}
	file := filepath.Join(root, "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PrivateDir(file); err == nil {
		t.Fatal("a file was taken as the private directory")
	}
}
