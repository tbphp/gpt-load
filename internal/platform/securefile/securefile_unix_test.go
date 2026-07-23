//go:build !windows

package securefile

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLoadOrCreateHexRejectsSymlink(t *testing.T) {
	dataDir := t.TempDir()
	targetDir := t.TempDir()
	target := filepath.Join(targetDir, "target.key")
	material := hex.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(target, []byte(material+"\n"), 0o644); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}
	if err := os.Chmod(target, 0o644); err != nil {
		t.Fatalf("chmod symlink target: %v", err)
	}
	if err := os.Symlink(target, filepath.Join(dataDir, "auth.key")); err != nil {
		t.Fatalf("create secure file symlink: %v", err)
	}

	if _, err := LoadOrCreateHex(dataDir, "auth.key"); err == nil {
		t.Fatal("LoadOrCreateHex() error = nil, want symlink rejection")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat symlink target: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("symlink target permissions = %o, want unchanged 644", info.Mode().Perm())
	}
}

func TestLoadOrCreateHexRejectsNamedPipe(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "auth.key")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("create named pipe: %v", err)
	}

	if _, err := LoadOrCreateHex(dataDir, "auth.key"); err == nil {
		t.Fatal("LoadOrCreateHex() error = nil, want named pipe rejection")
	}
}

func TestOpenedSecureFileRemainsBoundAfterPathReplacement(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "auth.key")
	openedPath := filepath.Join(dataDir, "opened.key")
	targetPath := filepath.Join(dataDir, "target.key")
	openedMaterial := strings.Repeat("1", encodedMaterialBytes)
	targetMaterial := strings.Repeat("2", encodedMaterialBytes)
	if err := os.WriteFile(path, []byte(openedMaterial+"\n"), 0o644); err != nil {
		t.Fatalf("write opened secure file: %v", err)
	}
	if err := os.WriteFile(targetPath, []byte(targetMaterial+"\n"), 0o644); err != nil {
		t.Fatalf("write symlink target: %v", err)
	}

	file, err := openExistingSecureFile(path)
	if err != nil {
		t.Fatalf("openExistingSecureFile() error = %v", err)
	}
	defer func() {
		_ = file.Close()
	}()

	if err := os.Rename(path, openedPath); err != nil {
		t.Fatalf("rename opened secure file: %v", err)
	}
	if err := os.Symlink(targetPath, path); err != nil {
		t.Fatalf("replace secure file path with symlink: %v", err)
	}

	got, err := readOpenedHex(file, path)
	if err != nil {
		t.Fatalf("readOpenedHex() error = %v", err)
	}
	if got != openedMaterial {
		t.Fatalf("readOpenedHex() = %q, want originally opened material", got)
	}

	openedInfo, err := os.Stat(openedPath)
	if err != nil {
		t.Fatalf("stat originally opened file: %v", err)
	}
	if openedInfo.Mode().Perm() != 0o600 {
		t.Fatalf("originally opened file permissions = %o, want 600", openedInfo.Mode().Perm())
	}
	targetInfo, err := os.Stat(targetPath)
	if err != nil {
		t.Fatalf("stat symlink target: %v", err)
	}
	if targetInfo.Mode().Perm() != 0o644 {
		t.Fatalf("symlink target permissions = %o, want unchanged 644", targetInfo.Mode().Perm())
	}
}
