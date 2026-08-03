//go:build windows

package catalog

import (
	"errors"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

var replaceFileW = windows.NewLazySystemDLL("kernel32.dll").NewProc("ReplaceFileW")

// replaceCatalogFileAtomic uses ReplaceFileW when the destination exists and
// MoveFileEx only for first publication when there is no destination to
// replace. Both paths are one same-directory Windows system operation.
func replaceCatalogFileAtomic(temporaryPath, finalPath string) error {
	return replaceCatalogFileWindows(temporaryPath, finalPath, windowsCatalogReplacePrimitives{
		targetExists:    windowsCatalogTargetExists,
		replaceExisting: replaceExistingCatalogFileWindows,
		moveNew:         moveNewCatalogFileWindows,
	})
}

func windowsCatalogTargetExists(path string) (bool, error) {
	_, err := os.Lstat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func replaceExistingCatalogFileWindows(temporaryPath, finalPath string) error {
	temporaryPathPtr, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return err
	}
	finalPathPtr, err := windows.UTF16PtrFromString(finalPath)
	if err != nil {
		return err
	}
	result, _, callErr := replaceFileW.Call(
		uintptr(unsafe.Pointer(finalPathPtr)),
		uintptr(unsafe.Pointer(temporaryPathPtr)),
		0,
		0,
		0,
		0,
	)
	if result == 0 {
		return &os.LinkError{Op: "replace", Old: temporaryPath, New: finalPath, Err: callErr}
	}
	return nil
}

func moveNewCatalogFileWindows(temporaryPath, finalPath string) error {
	temporaryPathPtr, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return err
	}
	finalPathPtr, err := windows.UTF16PtrFromString(finalPath)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(
		temporaryPathPtr,
		finalPathPtr,
		windows.MOVEFILE_WRITE_THROUGH,
	); err != nil {
		return &os.LinkError{Op: "move", Old: temporaryPath, New: finalPath, Err: err}
	}
	return nil
}
