//go:build !windows

package encryption

import "os"

func createSecureKeyFile(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
}

func secureKeyFile(path string) error {
	if err := requireRegularKeyFile(path); err != nil {
		return err
	}
	return os.Chmod(path, 0o600)
}
