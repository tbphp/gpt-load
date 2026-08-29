//go:build windows

package securefile

import (
	"fmt"
	"sync"

	"golang.org/x/sys/windows"
)

var windowsManagedACL struct {
	sync.RWMutex
	file      *windows.SECURITY_DESCRIPTOR
	directory *windows.SECURITY_DESCRIPTOR
}

// UseWindowsServiceACL switches managed Windows paths to a protected DACL that
// grants access only to the per-service SID and the local Administrators group.
// It must be called by the SCM entry point before configuration is loaded.
func UseWindowsServiceACL(serviceName string) error {
	fileDescriptor, directoryDescriptor, err := windowsServiceDescriptors(serviceName)
	if err != nil {
		return err
	}
	windowsManagedACL.Lock()
	windowsManagedACL.file = fileDescriptor
	windowsManagedACL.directory = directoryDescriptor
	windowsManagedACL.Unlock()
	return nil
}

// PrepareWindowsServiceDirectories creates or tightens service-owned
// directories before SCM starts the low-privilege service account.
func PrepareWindowsServiceDirectories(serviceName string, paths ...string) error {
	_, directoryDescriptor, err := windowsServiceDescriptors(serviceName)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if err := prepareManagedDataDirWithDescriptor(path, directoryDescriptor); err != nil {
			return err
		}
	}
	return nil
}

func windowsServiceDescriptors(
	serviceName string,
) (*windows.SECURITY_DESCRIPTOR, *windows.SECURITY_DESCRIPTOR, error) {
	serviceSID, _, _, err := windows.LookupSID("", `NT SERVICE\`+serviceName)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve Windows service SID: %w", err)
	}
	administratorsSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return nil, nil, fmt.Errorf("resolve Windows Administrators SID: %w", err)
	}
	return windowsServiceDescriptorsForSIDs(serviceSID.String(), administratorsSID.String())
}

func windowsServiceDescriptorsForSIDs(
	serviceSID string,
	administratorsSID string,
) (*windows.SECURITY_DESCRIPTOR, *windows.SECURITY_DESCRIPTOR, error) {
	fileDescriptor, err := windows.SecurityDescriptorFromString(
		windowsServiceFileSDDL(serviceSID, administratorsSID),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("build Windows service file DACL: %w", err)
	}
	directoryDescriptor, err := windows.SecurityDescriptorFromString(
		windowsServiceDirectorySDDL(serviceSID, administratorsSID),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("build Windows service directory DACL: %w", err)
	}
	return fileDescriptor, directoryDescriptor, nil
}

func managedFileSecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	windowsManagedACL.RLock()
	descriptor := windowsManagedACL.file
	windowsManagedACL.RUnlock()
	if descriptor != nil {
		return descriptor, nil
	}
	return currentUserSecurityDescriptor()
}

func managedDirectorySecurityDescriptor() (*windows.SECURITY_DESCRIPTOR, error) {
	windowsManagedACL.RLock()
	descriptor := windowsManagedACL.directory
	windowsManagedACL.RUnlock()
	if descriptor != nil {
		return descriptor, nil
	}
	return currentUserInheritableSecurityDescriptor()
}
