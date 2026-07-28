//go:build windows

package storage_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/storage"
)

const storageFileAllAccessMask windows.ACCESS_MASK = windows.STANDARD_RIGHTS_REQUIRED |
	windows.SYNCHRONIZE | 0x1ff

func TestOpenWithSourceManagedWindowsProtectsSQLiteRecoverySet(t *testing.T) {
	for _, tt := range []struct {
		name string
		dsn  func(string) string
	}{
		{
			name: "ordinary path with query",
			dsn: func(path string) string {
				return path + "?cache=shared"
			},
		},
		{
			name: "file URI with drive path",
			dsn: func(path string) string {
				return "file:///" + strings.TrimPrefix(filepath.ToSlash(path), "/") +
					"?cache=shared"
			},
		},
		{
			name: "opaque file URI with drive path",
			dsn: func(path string) string {
				return "file:" + filepath.ToSlash(path) + "?cache=shared"
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "managed")
			databasePath := filepath.Join(dataDir, "gpt-load.db")

			db, err := storage.OpenWithSource(
				tt.dsn(databasePath),
				config.DatabaseSourceManaged,
			)
			if err != nil {
				t.Fatalf("OpenWithSource(managed) error = %v", err)
			}
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatalf("DB() error = %v", err)
			}
			t.Cleanup(func() {
				if err := sqlDB.Close(); err != nil {
					t.Errorf("close database: %v", err)
				}
			})
			if err := db.Exec(
				"CREATE TABLE permission_probe (value TEXT NOT NULL)",
			).Error; err != nil {
				t.Fatalf("create permission probe table: %v", err)
			}
			if err := db.Exec(
				"INSERT INTO permission_probe(value) VALUES ('probe')",
			).Error; err != nil {
				t.Fatalf("write permission probe: %v", err)
			}

			for _, path := range []string{
				dataDir,
				databasePath,
				databasePath + "-wal",
				databasePath + "-shm",
			} {
				assertStorageCurrentUserOnlyProtectedACL(t, path)
			}
		})
	}
}

func assertStorageCurrentUserOnlyProtectedACL(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stat protected path: %v", err)
	}
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
		t.Fatalf("security descriptor control = %#x, want protected DACL", control)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("DACL() error = %v", err)
	}
	if dacl == nil || dacl.AceCount != 1 {
		t.Fatalf("DACL = %#v, want exactly one current-user ACE", dacl)
	}

	var ace *windows.ACCESS_ALLOWED_ACE
	if err := windows.GetAce(dacl, 0, &ace); err != nil {
		t.Fatalf("GetAce() error = %v", err)
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
		ace.Mask != storageFileAllAccessMask {
		t.Fatalf(
			"ACE type/mask = %d/%#x, want allowed/%#x",
			ace.Header.AceType,
			ace.Mask,
			storageFileAllAccessMask,
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
