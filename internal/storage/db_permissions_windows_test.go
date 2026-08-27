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
	"gpt-load/internal/platform/securefile"
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
			if err := securefile.PrepareManagedDataDir(dataDir); err != nil {
				t.Fatalf("prepare managed directory at startup: %v", err)
			}
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

			for _, target := range []struct {
				name             string
				path             string
				requireProtected bool
			}{
				{
					name:             "data directory",
					path:             dataDir,
					requireProtected: true,
				},
				{
					name: "database",
					path: databasePath,
				},
				{
					name: "write-ahead log",
					path: databasePath + "-wal",
				},
				{
					name: "shared memory",
					path: databasePath + "-shm",
				},
			} {
				t.Run(target.name, func(t *testing.T) {
					assertStorageCurrentUserOnlyACL(
						t,
						target.path,
						target.requireProtected,
					)
				})
			}
		})
	}
}

func assertStorageCurrentUserOnlyACL(
	t *testing.T,
	path string,
	requireProtected bool,
) {
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
	protected := control&windows.SE_DACL_PROTECTED != 0
	if requireProtected && !protected {
		t.Fatalf("security descriptor control = %#x, want protected DACL", control)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil {
		t.Fatalf("DACL() error = %v", err)
	}
	if dacl == nil || dacl.AceCount == 0 {
		t.Fatalf("DACL = %#v, want current-user ACEs", dacl)
	}

	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatalf("GetTokenUser() error = %v", err)
	}
	hasInheritedACE := false
	for index := uint16(0); index < dacl.AceCount; index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(index), &ace); err != nil {
			t.Fatalf("GetAce(%d) error = %v", index, err)
		}
		if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE ||
			ace.Mask != storageFileAllAccessMask {
			t.Fatalf(
				"ACE %d type/mask = %d/%#x, want allowed/%#x",
				index,
				ace.Header.AceType,
				ace.Mask,
				storageFileAllAccessMask,
			)
		}
		aceSID := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		if !aceSID.Equals(user.User.Sid) {
			t.Fatalf(
				"ACE %d SID = %s, want current user %s",
				index,
				aceSID.String(),
				user.User.Sid.String(),
			)
		}
		if ace.Header.AceFlags&windows.INHERITED_ACE != 0 {
			hasInheritedACE = true
		}
	}
	if !protected &&
		(control&windows.SE_DACL_AUTO_INHERITED == 0 || !hasInheritedACE) {
		t.Fatalf(
			"security descriptor control = %#x, want protected or inherited current-user DACL",
			control,
		)
	}
}
