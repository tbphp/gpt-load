//go:build windows

package securefile

import (
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createSecureFile(path string) (*os.File, error) {
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
		windows.GENERIC_READ|windows.GENERIC_WRITE|windows.WRITE_DAC,
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

func publishSecureFile(temporaryPath, finalPath string) error {
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

func openExistingSecureFile(path string) (*os.File, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.GENERIC_READ|windows.WRITE_DAC,
		windows.FILE_SHARE_READ,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = windows.CloseHandle(handle)
		return nil, &os.PathError{Op: "stat", Path: path, Err: err}
	}
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("secure file %s must be a regular file", path)
	}
	return os.NewFile(uintptr(handle), path), nil
}

func secureOpenedFile(file *os.File) error {
	descriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		return err
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(
		windows.Handle(file.Fd()),
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}

// Windows does not expose a portable directory fsync equivalent. File.Sync
// and the restrictive secure file handle/ACL provide the strongest common path.
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
