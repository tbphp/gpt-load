//go:build !windows

package encryption

import (
	"fmt"
	"os"
	"path/filepath"
)

func createSecureKeyFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func publishSecureKeyFile(temporaryPath, finalPath string) error {
	return os.Link(temporaryPath, finalPath)
}

func secureKeyFile(path string) error {
	if err := requireRegularKeyFile(path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
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
