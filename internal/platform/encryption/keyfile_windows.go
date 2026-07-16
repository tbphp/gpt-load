//go:build windows

package encryption

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createSecureKeyFile(path string) (*os.File, error) {
	descriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		return nil, err
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	attributes := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: descriptor,
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		0,
		&attributes,
		windows.CREATE_NEW,
		windows.FILE_ATTRIBUTE_NORMAL,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(handle), path), nil
}

func publishSecureKeyFile(temporaryPath, finalPath string) error {
	temporaryPathPtr, err := windows.UTF16PtrFromString(temporaryPath)
	if err != nil {
		return err
	}
	finalPathPtr, err := windows.UTF16PtrFromString(finalPath)
	if err != nil {
		return err
	}
	if err := windows.MoveFileEx(temporaryPathPtr, finalPathPtr, windows.MOVEFILE_WRITE_THROUGH); err != nil {
		return &os.LinkError{Op: "rename", Old: temporaryPath, New: finalPath, Err: err}
	}
	return nil
}

func secureKeyFile(path string) error {
	if err := requireRegularKeyFile(path); err != nil {
		return err
	}
	descriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}

// Windows does not expose a portable directory fsync equivalent. File.Sync
// and the restrictive keyfile handle/ACL provide the strongest common path.
func syncParentDirectory(string) error {
	return nil
}

func currentUserSecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + user.User.Sid.String() + ")")
}
