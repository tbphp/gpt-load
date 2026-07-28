//go:build windows

package securefile

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestPrepareManagedDataDirWindowsUsesProtectedInheritableCurrentUserDACL(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "managed")

	if err := PrepareManagedDataDir(dataDir); err != nil {
		t.Fatalf("PrepareManagedDataDir() error = %v", err)
	}
	assertCurrentUserOnlyACL(t, dataDir)

	descriptor, err := windows.GetNamedSecurityInfo(
		dataDir,
		windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil {
		t.Fatalf("GetNamedSecurityInfo() error = %v", err)
	}
	control, _, err := descriptor.Control()
	if err != nil {
		t.Fatalf("Security descriptor Control() error = %v", err)
	}
	if control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("security descriptor control = %#x, want SE_DACL_PROTECTED", control)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("DACL() error = %v", err)
	}
	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatalf("GetAce() error = %v", err)
	}
	wantFlags := byte(windows.OBJECT_INHERIT_ACE | windows.CONTAINER_INHERIT_ACE)
	if ace.Header.AceFlags&wantFlags != wantFlags {
		t.Fatalf("ACE flags = %#x, want object/container inheritance %#x", ace.Header.AceFlags, wantFlags)
	}
}

func TestPrepareManagedDataDirWindowsRejectsReparsePointAndNonDirectory(t *testing.T) {
	t.Run("reparse point", func(t *testing.T) {
		target := t.TempDir()
		dataDir := filepath.Join(t.TempDir(), "managed-link")
		if err := os.Symlink(target, dataDir); err != nil {
			t.Skipf("creating Windows symlink requires unavailable privilege: %v", err)
		}
		if err := PrepareManagedDataDir(dataDir); err == nil {
			t.Fatal("PrepareManagedDataDir() error = nil, want reparse-point rejection")
		}
	})

	t.Run("non-directory", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "managed-file")
		if err := os.WriteFile(dataDir, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("write non-directory DATA_DIR: %v", err)
		}
		if err := PrepareManagedDataDir(dataDir); err == nil {
			t.Fatal("PrepareManagedDataDir() error = nil, want non-directory rejection")
		}
	})
}
