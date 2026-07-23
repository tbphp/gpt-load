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
