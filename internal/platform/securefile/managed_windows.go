//go:build windows

package securefile

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func prepareManagedDataDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}

	handle, information, err := openManagedPath(
		path,
		windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT,
	)
	if err != nil {
		return err
	}
	defer func() {
		_ = windows.CloseHandle(handle)
	}()
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
		return windows.ERROR_INVALID_DATA
	}

	descriptor, err := currentUserInheritableSecurityDescriptor()
	if err != nil {
		return err
	}
	return setManagedDACL(handle, descriptor)
}

func hardenManagedFileIfExists(path string) error {
	handle, information, err := openManagedPath(path, windows.FILE_FLAG_OPEN_REPARSE_POINT)
	if errors.Is(err, windows.ERROR_FILE_NOT_FOUND) ||
		errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() {
		_ = windows.CloseHandle(handle)
	}()
	if information.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 ||
		information.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY != 0 {
		return windows.ERROR_INVALID_DATA
	}

	descriptor, err := currentUserSecurityDescriptor()
	if err != nil {
		return err
	}
	return setManagedDACL(handle, descriptor)
}

func openManagedPath(
	path string,
	flags uint32,
) (windows.Handle, windows.ByHandleFileInformation, error) {
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, windows.ByHandleFileInformation{}, err
	}
	handle, err := windows.CreateFile(
		pathPtr,
		windows.READ_CONTROL|windows.WRITE_DAC,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		flags,
		0,
	)
	if err != nil {
		return 0, windows.ByHandleFileInformation{}, err
	}
	var information windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &information); err != nil {
		_ = windows.CloseHandle(handle)
		return 0, windows.ByHandleFileInformation{}, err
	}
	return handle, information, nil
}

func setManagedDACL(handle windows.Handle, descriptor *windows.SECURITY_DESCRIPTOR) error {
	dacl, _, err := descriptor.DACL()
	if err != nil {
		return err
	}
	return windows.SetSecurityInfo(
		handle,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION,
		nil,
		nil,
		dacl,
		nil,
	)
}

func currentUserInheritableSecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return nil, err
	}
	return windows.SecurityDescriptorFromString(
		"D:P(A;OICI;GA;;;" + user.User.Sid.String() + ")",
	)
}
