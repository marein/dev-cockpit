package filesystem

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// PrivateDir makes dir a directory only this user may enter, the place of a
// socket that is a credential by itself. A directory already standing there
// is taken only if it is a real directory of this user that nobody else could
// ever enter, the only kind the cockpit leaves behind: a path it derives in a
// shared place like /tmp is known to everybody, and another user may have put
// a link there first, or something into a directory that stood open. The
// check and the mode go through the opened directory, so swapping the path in
// between changes nothing.
func PrivateDir(dir string) error {
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return err
	}
	if err := os.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("%s is no directory of its own: %w", dir, err)
	}
	defer unix.Close(fd)
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if int(st.Uid) != os.Geteuid() {
		return fmt.Errorf("%s belongs to another user", dir)
	}
	if st.Mode&0o077 != 0 {
		return fmt.Errorf("%s was open to other users", dir)
	}
	return unix.Fchmod(fd, 0o700)
}
