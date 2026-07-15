package encryption

import (
	"encoding/hex"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
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

func TestLoadOrCreateKeyMaterialRejectsCorruptKeyFile(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{name: "empty", contents: ""},
		{name: "short", contents: "abcd"},
		{name: "odd length", contents: "abc"},
		{name: "non hex", contents: "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			path := filepath.Join(dataDir, KeyFileName)
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatalf("write corrupt keyfile: %v", err)
			}
			if _, err := LoadOrCreateKeyMaterial("", dataDir); err == nil {
				t.Fatal("LoadOrCreateKeyMaterial() error = nil, want corrupt keyfile error")
			}
		})
	}
}

func TestLoadOrCreateKeyMaterialConcurrentFirstStartReusesOneKey(t *testing.T) {
	dataDir := t.TempDir()
	const workers = 32
	start := make(chan struct{})
	results := make(chan string, workers)
	errors := make(chan error, workers)
	var waitGroup sync.WaitGroup

	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			material, err := LoadOrCreateKeyMaterial("", dataDir)
			if err != nil {
				errors <- err
				return
			}
			results <- material
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errors)

	for err := range errors {
		t.Errorf("concurrent LoadOrCreateKeyMaterial() error = %v", err)
	}
	var want string
	for material := range results {
		if want == "" {
			want = material
		}
		if material != want {
			t.Errorf("concurrent key material = %q, want %q", material, want)
		}
	}
	if want == "" {
		t.Fatal("no concurrent caller returned key material")
	}
}

func TestLoadOrCreateKeyMaterialRetriesKeyFileBeingWrittenByAnotherProcess(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, KeyFileName)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("create partial keyfile: %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close partial keyfile: %v", err)
	}

	want := hex.EncodeToString(make([]byte, 32))
	writeDone := make(chan error, 1)
	go func() {
		time.Sleep(10 * time.Millisecond)
		writeDone <- os.WriteFile(path, []byte(want+"\n"), 0o600)
	}()

	got, err := LoadOrCreateKeyMaterial("", dataDir)
	if err != nil {
		t.Fatalf("LoadOrCreateKeyMaterial() error = %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("complete keyfile write: %v", err)
	}
	if got != want {
		t.Fatalf("key material = %q, want %q", got, want)
	}
}
