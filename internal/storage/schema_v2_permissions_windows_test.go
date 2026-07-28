//go:build windows

package storage_test

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows does not expose FILE_ALL_ACCESS from WinNT.h.
const backupFileAllAccessMask windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED |
	windows.SYNCHRONIZE | 0x1ff

func assertSecureBackupPermissions(t *testing.T, path string) {
	t.Helper()

	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo() error = %v", err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("security descriptor Control() error = %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("security descriptor control = %#x, want SE_DACL_PROTECTED", control)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("DACL() error = %v", err)
	}
	if dacl == nil {
		t.Fatal("DACL() = nil, want protected current-user ACL")
	}
	if dacl.AceCount != 1 {
		t.Fatalf("DACL ACE count = %d, want 1", dacl.AceCount)
	}

	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatalf("GetAce() error = %v", err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
		t.Fatalf("ACE type = %d, want ACCESS_ALLOWED_ACE_TYPE", ace.Header.AceType)
	}
	if ace.Mask != backupFileAllAccessMask {
		t.Fatalf(
			"ACE mask = %#x, want FILE_ALL_ACCESS (%#x)",
			ace.Mask,
			backupFileAllAccessMask,
		)
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !aceSID.Equals(user.User.Sid) {
		t.Fatalf("ACE SID = %s, want current user %s", aceSID.String(), user.User.Sid.String())
	}
}
