//go:build !windows

package encryption

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateKeyMaterialRejectsSymlink(t *testing.T) {
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
	if err := os.Symlink(target, filepath.Join(dataDir, KeyFileName)); err != nil {
		t.Fatalf("create keyfile symlink: %v", err)
	}

	if _, err := LoadOrCreateKeyMaterial("", dataDir); err == nil {
		t.Fatal("LoadOrCreateKeyMaterial() error = nil, want symlink rejection")
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat symlink target: %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("symlink target permissions = %o, want unchanged 644", info.Mode().Perm())
	}
}
