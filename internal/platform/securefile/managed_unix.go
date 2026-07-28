//go:build !windows

package securefile

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

var (
	managedFstat  = unix.Fstat
	managedFchmod = unix.Fchmod
)

func prepareManagedDataDir(path string) error {
	unix.Umask(0o077)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}

	fd, err := unix.Open(
		path,
		unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC,
		0,
	)
	if err != nil {
		return err
	}
	defer func() {
		_ = unix.Close(fd)
	}()

	var stat unix.Stat_t
	if err := managedFstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return unix.ENOTDIR
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return unix.EPERM
	}
	return managedFchmod(fd, 0o700)
}

func hardenManagedFileIfExists(path string) error {
	fd, err := unix.Open(
		path,
		unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC,
		0,
	)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		_ = unix.Close(fd)
	}()

	var stat unix.Stat_t
	if err := managedFstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return unix.EINVAL
	}
	if stat.Uid != uint32(os.Geteuid()) {
		return unix.EPERM
	}
	return managedFchmod(fd, 0o600)
}
