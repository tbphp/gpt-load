//go:build !windows

package securefile

import "os"

func replaceAtomicFile(temp, path string) error { return os.Rename(temp, path) }
