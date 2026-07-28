package securefile

import (
	"errors"
	"path/filepath"
)

var (
	errManagedDataDir = errors.New("managed data directory could not be secured")
	errManagedFile    = errors.New("managed file could not be secured")
)

// PrepareManagedDataDir creates or tightens an application-managed data
// directory using the current platform's least-privilege controls.
func PrepareManagedDataDir(path string) error {
	cleanPath, ok := cleanManagedPath(path)
	if !ok {
		return errManagedDataDir
	}
	if err := prepareManagedDataDir(cleanPath); err != nil {
		return errManagedDataDir
	}
	return nil
}

// HardenManagedFileIfExists tightens an existing application-managed file. A
// missing file is allowed so callers can cover SQLite's optional recovery files.
func HardenManagedFileIfExists(path string) error {
	cleanPath, ok := cleanManagedPath(path)
	if !ok {
		return errManagedFile
	}
	if err := hardenManagedFileIfExists(cleanPath); err != nil {
		return errManagedFile
	}
	return nil
}

func cleanManagedPath(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	return filepath.Clean(path), true
}
