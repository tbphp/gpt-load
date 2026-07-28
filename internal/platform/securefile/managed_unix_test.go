//go:build !windows

package securefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestPrepareManagedDataDirTightensOwnedWideDirectory(t *testing.T) {
	restoreUmask := preserveProcessUmask()
	t.Cleanup(restoreUmask)

	dataDir := filepath.Join(t.TempDir(), "managed")
	if err := os.Mkdir(dataDir, 0o777); err != nil {
		t.Fatalf("create wide DATA_DIR: %v", err)
	}
	if err := os.Chmod(dataDir, 0o777); err != nil {
		t.Fatalf("chmod wide DATA_DIR: %v", err)
	}

	if err := PrepareManagedDataDir(dataDir); err != nil {
		t.Fatalf("PrepareManagedDataDir() error = %v", err)
	}
	info, err := os.Stat(dataDir)
	if err != nil {
		t.Fatalf("stat DATA_DIR: %v", err)
	}
	if info.Mode().Perm() != 0o700 {
		t.Fatalf("DATA_DIR permissions = %o, want 700", info.Mode().Perm())
	}

	probePath := filepath.Join(dataDir, "umask-probe")
	probe, err := os.OpenFile(probePath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		t.Fatalf("create umask probe: %v", err)
	}
	if err := probe.Close(); err != nil {
		t.Fatalf("close umask probe: %v", err)
	}
	probeInfo, err := os.Stat(probePath)
	if err != nil {
		t.Fatalf("stat umask probe: %v", err)
	}
	if probeInfo.Mode().Perm() != 0o600 {
		t.Fatalf("umask probe permissions = %o, want 600", probeInfo.Mode().Perm())
	}
}

func TestPrepareManagedDataDirRejectsSymlinkNonDirectoryForeignOwnerAndChmodFailure(t *testing.T) {
	restoreUmask := preserveProcessUmask()
	t.Cleanup(restoreUmask)

	t.Run("symlink", func(t *testing.T) {
		target := t.TempDir()
		dataDir := filepath.Join(t.TempDir(), "sensitive-symlink-data-dir")
		if err := os.Symlink(target, dataDir); err != nil {
			t.Fatalf("create DATA_DIR symlink: %v", err)
		}

		err := PrepareManagedDataDir(dataDir)
		assertManagedDataDirError(t, err, dataDir)
	})

	for _, tt := range []struct {
		name   string
		suffix string
	}{
		{name: "symlink with trailing separator", suffix: string(os.PathSeparator)},
		{name: "symlink with dot suffix", suffix: string(os.PathSeparator) + "."},
	} {
		t.Run(tt.name, func(t *testing.T) {
			targetParent := t.TempDir()
			target := filepath.Join(targetParent, "target")
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatalf("create symlink target: %v", err)
			}
			if err := os.Chmod(target, 0o755); err != nil {
				t.Fatalf("chmod symlink target: %v", err)
			}
			link := filepath.Join(t.TempDir(), "sensitive-symlink-data-dir")
			if err := os.Symlink(target, link); err != nil {
				t.Fatalf("create DATA_DIR symlink: %v", err)
			}
			dataDir := link + tt.suffix

			err := PrepareManagedDataDir(dataDir)
			if err == nil {
				t.Error("PrepareManagedDataDir() error = nil, want rejection")
			} else if strings.Contains(err.Error(), dataDir) {
				t.Errorf("PrepareManagedDataDir() error exposes path: %v", err)
			}
			info, statErr := os.Stat(target)
			if statErr != nil {
				t.Fatalf("stat symlink target: %v", statErr)
			}
			if info.Mode().Perm() != 0o755 {
				t.Errorf("symlink target permissions = %o, want unchanged 755", info.Mode().Perm())
			}
		})
	}

	t.Run("non-directory", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "sensitive-file-data-dir")
		if err := os.WriteFile(dataDir, []byte("not a directory"), 0o600); err != nil {
			t.Fatalf("write non-directory DATA_DIR: %v", err)
		}

		err := PrepareManagedDataDir(dataDir)
		assertManagedDataDirError(t, err, dataDir)
	})

	t.Run("foreign owner", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "sensitive-foreign-data-dir")
		if err := os.Mkdir(dataDir, 0o700); err != nil {
			t.Fatalf("create DATA_DIR: %v", err)
		}
		originalFstat := managedFstat
		managedFstat = func(fd int, stat *unix.Stat_t) error {
			if err := originalFstat(fd, stat); err != nil {
				return err
			}
			stat.Uid = uint32(os.Geteuid() + 1)
			return nil
		}
		t.Cleanup(func() {
			managedFstat = originalFstat
		})

		err := PrepareManagedDataDir(dataDir)
		assertManagedDataDirError(t, err, dataDir)
	})

	t.Run("chmod failure", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "sensitive-chmod-data-dir")
		if err := os.Mkdir(dataDir, 0o777); err != nil {
			t.Fatalf("create DATA_DIR: %v", err)
		}
		if err := os.Chmod(dataDir, 0o777); err != nil {
			t.Fatalf("chmod DATA_DIR: %v", err)
		}
		originalFchmod := managedFchmod
		managedFchmod = func(int, uint32) error {
			return unix.EPERM
		}
		t.Cleanup(func() {
			managedFchmod = originalFchmod
		})

		err := PrepareManagedDataDir(dataDir)
		assertManagedDataDirError(t, err, dataDir)
		info, statErr := os.Stat(dataDir)
		if statErr != nil {
			t.Fatalf("stat DATA_DIR: %v", statErr)
		}
		if info.Mode().Perm() != 0o777 {
			t.Fatalf("DATA_DIR permissions = %o, want unchanged 777", info.Mode().Perm())
		}
	})
}

func TestManagedOperationsRejectEmptyPath(t *testing.T) {
	if err := PrepareManagedDataDir(""); err == nil {
		t.Fatal("PrepareManagedDataDir(empty) error = nil, want rejection")
	}
	if err := HardenManagedFileIfExists(""); err == nil {
		t.Fatal("HardenManagedFileIfExists(empty) error = nil, want rejection")
	}
}

func TestHardenManagedFileIfExistsTightensRegularFileAndRejectsSymlink(t *testing.T) {
	restoreUmask := preserveProcessUmask()
	t.Cleanup(restoreUmask)

	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "managed.db")
	if err := os.WriteFile(path, []byte("database"), 0o666); err != nil {
		t.Fatalf("write managed file: %v", err)
	}
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatalf("chmod managed file: %v", err)
	}

	if err := HardenManagedFileIfExists(path); err != nil {
		t.Fatalf("HardenManagedFileIfExists() error = %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat managed file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("managed file permissions = %o, want 600", info.Mode().Perm())
	}
	if err := HardenManagedFileIfExists(filepath.Join(dataDir, "missing.db")); err != nil {
		t.Fatalf("HardenManagedFileIfExists(missing) error = %v", err)
	}

	target := filepath.Join(dataDir, "target.db")
	if err := os.WriteFile(target, []byte("target"), 0o644); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatalf("chmod symlink target: %v", err)
	}
	link := filepath.Join(dataDir, "sensitive-link.db")
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("create managed file symlink: %v", err)
	}
	err = HardenManagedFileIfExists(link)
	if err == nil {
		t.Fatal("HardenManagedFileIfExists(symlink) error = nil, want rejection")
	}
	if strings.Contains(err.Error(), link) {
		t.Fatalf("HardenManagedFileIfExists() error exposes path: %v", err)
	}
	targetInfo, statErr := os.Stat(target)
	if statErr != nil {
		t.Fatalf("stat symlink target: %v", statErr)
	}
	if targetInfo.Mode().Perm() != 0o644 {
		t.Fatalf("symlink target permissions = %o, want unchanged 644", targetInfo.Mode().Perm())
	}
}

func assertManagedDataDirError(t *testing.T, err error, sensitivePath string) {
	t.Helper()
	if err == nil {
		t.Fatal("PrepareManagedDataDir() error = nil, want rejection")
	}
	if strings.Contains(err.Error(), sensitivePath) {
		t.Fatalf("PrepareManagedDataDir() error exposes path: %v", err)
	}
}

func preserveProcessUmask() func() {
	previous := unix.Umask(0o077)
	unix.Umask(previous)
	return func() {
		unix.Umask(previous)
	}
}
