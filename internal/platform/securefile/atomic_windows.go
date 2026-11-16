//go:build windows

package securefile

import (
	"golang.org/x/sys/windows"
	"os"
)

// The candidate is a sibling on the same volume. Do not enable COPY_ALLOWED:
// copy/delete would expose partial contents. Keep the candidate's restrictive
// ACL rather than merging a potentially wider destination ACL via ReplaceFileW.
func replaceAtomicFile(temp, path string) error {
	a, e := windows.UTF16PtrFromString(temp)
	if e != nil {
		return e
	}
	b, e := windows.UTF16PtrFromString(path)
	if e != nil {
		return e
	}
	if e = windows.MoveFileEx(a, b, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH); e != nil {
		return &os.LinkError{Op: "replace", Old: temp, New: path, Err: e}
	}
	return nil
}
