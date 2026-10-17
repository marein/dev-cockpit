package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// A worktree project that will not go ends the cascade and leaves the main
// standing, and the main's links are gone all the same: they end when its
// deletion starts, not when its directory goes.
func TestAFailedCascadeStillEndsTheMainsLinks(t *testing.T) {
	f := newDeleteFixture(t)
	worktree := f.addWorktreeProject(t)
	lockAgainstRemoval(t, worktree)
	for _, name := range []string{f.project, "shop-feature"} {
		if _, _, err := f.s.shares.Create("", linkTo(name), time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	if rec := f.post(t, "/projects/delete", url.Values{"project": {f.project}}, false, ""); rec.Code != http.StatusBadRequest {
		t.Fatalf("the delete answered %d: %s", rec.Code, rec.Body.String())
	}
	if !exists(f.dir) {
		t.Fatal("the main went although its worktree project would not")
	}
	if links := f.s.shares.List(); len(links) != 0 {
		t.Fatalf("links outlived the deletion's start: %+v", links)
	}
}

// lockAgainstRemoval puts a file into dir that nobody can remove, the way a
// user's chattr +i does. A read only directory stops everybody but root, root
// needs the immutable flag, which not every filesystem and container grants.
func lockAgainstRemoval(t *testing.T, dir string) {
	t.Helper()
	if os.Geteuid() != 0 {
		if err := os.Chmod(dir, 0o555); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
		return
	}
	path := filepath.Join(dir, "locked")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	flags, err := unix.IoctlGetUint32(int(file.Fd()), unix.FS_IOC_GETFLAGS)
	if err != nil {
		t.Skipf("the filesystem carries no file flags: %v", err)
	}
	// statx reports the immutable flag under the bit the ioctl sets, x/sys
	// names only that one.
	if err := unix.IoctlSetPointerInt(int(file.Fd()), unix.FS_IOC_SETFLAGS, int(flags|unix.STATX_ATTR_IMMUTABLE)); err != nil {
		t.Skipf("the immutable flag is refused here: %v", err)
	}
	t.Cleanup(func() {
		file, err := os.Open(path)
		if err != nil {
			return
		}
		defer file.Close()
		_ = unix.IoctlSetPointerInt(int(file.Fd()), unix.FS_IOC_SETFLAGS, int(flags))
	})
}
