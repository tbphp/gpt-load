package securefile

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type atomicFile interface {
	io.Writer
	Sync() error
	Close() error
}
type atomicWriteOps struct {
	create        func(string, string) (atomicFile, string, error)
	secure        func(atomicFile) error
	replace       func(string, string) error
	syncDirectory func(string) error
	remove        func(string) error
}

func defaultAtomicWriteOps() atomicWriteOps {
	return atomicWriteOps{
		create:  func(dir, base string) (atomicFile, string, error) { return createSecureTemporaryFile(dir, base) },
		secure:  func(f atomicFile) error { return secureOpenedFile(f.(*os.File)) },
		replace: replaceAtomicFile, syncDirectory: syncParentDirectory, remove: os.Remove,
	}
}

// WriteAtomic publishes complete bytes through a secure sibling temporary file.
// Callers serialize writes when snapshot order matters. A directory sync error
// is returned after publication; the new target must not be removed or rolled back.
func WriteAtomic(path string, payload []byte) error {
	return writeAtomic(path, payload, defaultAtomicWriteOps())
}
func writeAtomic(path string, payload []byte, ops atomicWriteOps) (result error) {
	f, temp, err := ops.create(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return fmt.Errorf("create checkpoint temporary: %w", err)
	}
	closed, published := false, false
	defer func() {
		if !closed {
			result = errors.Join(result, f.Close())
		}
		if !published {
			if e := ops.remove(temp); e != nil && !os.IsNotExist(e) {
				result = errors.Join(result, fmt.Errorf("cleanup checkpoint temporary: %w", e))
			}
		}
	}()
	if err = ops.secure(f); err != nil {
		return fmt.Errorf("secure checkpoint temporary: %w", err)
	}
	n, err := f.Write(payload)
	if err != nil {
		return fmt.Errorf("write checkpoint temporary: %w", err)
	}
	if n != len(payload) {
		return io.ErrShortWrite
	}
	if err = f.Sync(); err != nil {
		return fmt.Errorf("sync checkpoint temporary: %w", err)
	}
	err = f.Close()
	closed = true
	if err != nil {
		return fmt.Errorf("close checkpoint temporary: %w", err)
	}
	if err = ops.replace(temp, path); err != nil {
		return fmt.Errorf("replace checkpoint: %w", err)
	}
	published = true
	if err = ops.syncDirectory(path); err != nil {
		return fmt.Errorf("sync checkpoint directory after replacement: %w", err)
	}
	return nil
}
