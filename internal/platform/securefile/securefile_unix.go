//go:build !windows

package securefile

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func createSecureFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func publishSecureFile(temporaryPath, finalPath string) error {
	return os.Link(temporaryPath, finalPath)
}

func openExistingSecureFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
}

func secureOpenedFile(file *os.File) error {
	return file.Chmod(0o600)
}

func syncParentDirectory(path string) error {
	directory, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open parent directory: %w", err)
	}
	if err := directory.Sync(); err != nil {
		_ = directory.Close()
		return fmt.Errorf("sync parent directory: %w", err)
	}
	if err := directory.Close(); err != nil {
		return fmt.Errorf("close parent directory: %w", err)
	}
	return nil
}
