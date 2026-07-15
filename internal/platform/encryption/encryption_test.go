package encryption

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestServiceEncryptDecryptAndStableHash(t *testing.T) {
	service, err := NewService("a-test-master-key")
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	ciphertext, err := service.Encrypt("sk-secret-value")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if ciphertext == "sk-secret-value" {
		t.Fatal("Encrypt() returned plaintext")
	}

	plaintext, err := service.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if plaintext != "sk-secret-value" {
		t.Fatalf("Decrypt() = %q", plaintext)
	}

	first := service.Hash("sk-secret-value")
	second := service.Hash("sk-secret-value")
	if first == "" || first != second {
		t.Fatalf("Hash() is not stable: %q != %q", first, second)
	}
}

func TestNewServiceRejectsEmptyMasterKey(t *testing.T) {
	if _, err := NewService(""); err == nil {
		t.Fatal("NewService() error = nil, want error")
	}
}

func TestLoadOrCreateKeyMaterialGeneratesAndReusesKeyFile(t *testing.T) {
	dataDir := t.TempDir()

	first, err := LoadOrCreateKeyMaterial("", dataDir)
	if err != nil {
		t.Fatalf("first LoadOrCreateKeyMaterial() error = %v", err)
	}
	if len(first) != 64 {
		t.Fatalf("generated key length = %d, want 64 hex chars", len(first))
	}

	second, err := LoadOrCreateKeyMaterial("", dataDir)
	if err != nil {
		t.Fatalf("second LoadOrCreateKeyMaterial() error = %v", err)
	}
	if second != first {
		t.Fatalf("keyfile was not reused: %q != %q", second, first)
	}

	info, err := os.Stat(filepath.Join(dataDir, KeyFileName))
	if err != nil {
		t.Fatalf("Stat(keyfile) error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("keyfile permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadOrCreateKeyMaterialPrefersExplicitKey(t *testing.T) {
	dataDir := t.TempDir()

	got, err := LoadOrCreateKeyMaterial("explicit-key", dataDir)
	if err != nil {
		t.Fatalf("LoadOrCreateKeyMaterial() error = %v", err)
	}
	if got != "explicit-key" {
		t.Fatalf("key material = %q", got)
	}
	if _, err := os.Stat(filepath.Join(dataDir, KeyFileName)); !os.IsNotExist(err) {
		t.Fatalf("explicit key unexpectedly created keyfile: %v", err)
	}
}

func TestNewServiceWithKeyFileUsesGeneratedMaterial(t *testing.T) {
	dataDir := t.TempDir()
	service, err := NewServiceWithKeyFile("", dataDir)
	if err != nil {
		t.Fatalf("NewServiceWithKeyFile() error = %v", err)
	}
	ciphertext, err := service.Encrypt("value")
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if _, err := service.Decrypt(ciphertext); err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
}
