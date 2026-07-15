//go:build windows

package encryption

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestGeneratedKeyFileGrantsAccessOnlyToCurrentUser(t *testing.T) {
	dataDir := t.TempDir()
	if _, err := LoadOrCreateKeyMaterial("", dataDir); err != nil {
		t.Fatalf("LoadOrCreateKeyMaterial() error = %v", err)
	}
	assertCurrentUserOnlyACL(t, filepath.Join(dataDir, KeyFileName))
}

func TestExistingKeyFileACLIsHardened(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, KeyFileName)
	material := hex.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(path, []byte(material+"\n"), 0o666); err != nil {
		t.Fatalf("write existing keyfile: %v", err)
	}
	if _, err := LoadOrCreateKeyMaterial("", dataDir); err != nil {
		t.Fatalf("LoadOrCreateKeyMaterial() error = %v", err)
	}
	assertCurrentUserOnlyACL(t, path)
}

func TestLoadOrCreateKeyMaterialRejectsSymlinkReparsePoint(t *testing.T) {
	dataDir := t.TempDir()
	target := filepath.Join(t.TempDir(), "target.key")
	material := hex.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(target, []byte(material+"\n"), 0o600); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(dataDir, KeyFileName)); err != nil {
		t.Skipf("creating Windows symlink requires unavailable privilege: %v", err)
	}

	if _, err := LoadOrCreateKeyMaterial("", dataDir); err == nil {
		t.Fatal("LoadOrCreateKeyMaterial() error = nil, want reparse-point rejection")
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
	if ace.Mask != windows.GENERIC_ALL {
		t.Fatalf("ACE mask = %#x, want GENERIC_ALL", ace.Mask)
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
