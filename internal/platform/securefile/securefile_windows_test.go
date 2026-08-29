//go:build windows

package securefile

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows does not expose FILE_ALL_ACCESS from WinNT.h.
const fileAllAccessMask windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED | windows.SYNCHRONIZE | 0x1ff

func TestGeneratedSecureFileGrantsAccessOnlyToCurrentUser(t *testing.T) {
	dataDir := t.TempDir()
	result, err := LoadOrCreateHex(dataDir, "auth.key")
	if err != nil {
		t.Fatalf("LoadOrCreateHex() error = %v", err)
	}
	assertCurrentUserOnlyACL(t, result.Path)
}

func TestExistingSecureFileACLIsHardened(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "auth.key")
	material := hex.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(path, []byte(material+"\n"), 0o666); err != nil {
		t.Fatalf("write existing secure file: %v", err)
	}
	if _, err := LoadOrCreateHex(dataDir, "auth.key"); err != nil {
		t.Fatalf("LoadOrCreateHex() error = %v", err)
	}
	assertCurrentUserOnlyACL(t, path)
}

func TestGeneratedSecureFileServiceACLIncludesServiceAndAdministrators(t *testing.T) {
	serviceSID, administratorsSID := useTestWindowsServiceACL(t)
	dataDir := t.TempDir()
	result, err := LoadOrCreateHex(dataDir, "auth.key")
	if err != nil {
		t.Fatalf("LoadOrCreateHex() error = %v", err)
	}
	assertACLPrincipals(t, result.Path, serviceSID, administratorsSID)
}

func TestLoadOrCreateHexRejectsSymlinkReparsePoint(t *testing.T) {
	dataDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.key")
	material := hex.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(target, []byte(material+"\n"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(dataDir, "auth.key")); err != nil {
		t.Skipf("creating Windows symlink requires unavailable privilege: %v", err)
	}

	if _, err := LoadOrCreateHex(dataDir, "auth.key"); err == nil {
		t.Fatal("LoadOrCreateHex() error = nil, want reparse-point rejection")
	}
}

func assertCurrentUserOnlyACL(t *testing.T, path string) {
	t.Helper()

	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo() error = %v", err)
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
	if ace.Mask != fileAllAccessMask {
		t.Fatalf("ACE mask = %#x, want FILE_ALL_ACCESS (%#x)", ace.Mask, fileAllAccessMask)
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

func useTestWindowsServiceACL(t *testing.T) (*windows.SID, *windows.SID) {
	t.Helper()
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	administratorsSID, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		t.Fatalf("CreateWellKnownSid() error = %v", err)
	}
	fileDescriptor, directoryDescriptor, err := windowsServiceDescriptorsForSIDs(
		user.User.Sid.String(),
		administratorsSID.String(),
	)
	if err != nil {
		t.Fatalf("windowsServiceDescriptorsForSIDs() error = %v", err)
	}
	windowsManagedACL.Lock()
	previousFile := windowsManagedACL.file
	previousDirectory := windowsManagedACL.directory
	windowsManagedACL.file = fileDescriptor
	windowsManagedACL.directory = directoryDescriptor
	windowsManagedACL.Unlock()
	t.Cleanup(func() {
		windowsManagedACL.Lock()
		windowsManagedACL.file = previousFile
		windowsManagedACL.directory = previousDirectory
		windowsManagedACL.Unlock()
	})
	return user.User.Sid, administratorsSID
}

func assertACLPrincipals(t *testing.T, path string, wantSIDs ...*windows.SID) {
	t.Helper()
	descriptor, err := windows.GetNamedSecurityInfo(
		path,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo() error = %v", err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("DACL() error = %v", err)
	}
	if dacl == nil {
		t.Fatal("DACL() = nil")
	}
	if int(dacl.AceCount) != len(wantSIDs) {
		t.Fatalf("DACL ACE count = %d, want %d", dacl.AceCount, len(wantSIDs))
	}
	remaining := make(map[string]bool, len(wantSIDs))
	for _, sid := range wantSIDs {
		remaining[sid.String()] = true
	}
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil {
			t.Fatalf("GetAce(%d) error = %v", index, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || ace.Mask != fileAllAccessMask {
			t.Fatalf("ACE %d type/mask = %d/%#x", index, ace.Header.AceType, ace.Mask)
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		delete(remaining, aceSID.String())
	}
	if len(remaining) != 0 {
		t.Fatalf("DACL missing SIDs: %v", remaining)
	}
}
