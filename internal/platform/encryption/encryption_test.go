package encryption

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/platform/utils"
)

func TestServiceUsesDomainSeparatedFingerprintKey(t *testing.T) {
	const (
		masterKey = "domain-separated-master-key"
		plaintext = "sk-secret-value"
	)
	service, err := NewService(masterKey)
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	legacyMAC := hmac.New(sha256.New, utils.DeriveAESKey(masterKey))
	_, _ = legacyMAC.Write([]byte(plaintext))
	legacyHash := hex.EncodeToString(legacyMAC.Sum(nil))

	if got := service.Hash(plaintext); got == legacyHash {
		t.Fatal("Hash() reuses the AES key; fingerprinting requires a domain-separated subkey")
	}
}

func TestPersistGeneratedKeyMaterialPublishesOnlyCompleteFile(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, KeyFileName)
	material := hex.EncodeToString(make([]byte, 32))
	publishReady := make(chan error, 1)
	releasePublish := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- persistGeneratedKeyMaterial(
			path,
			material,
			func(temporaryPath, finalPath string) error {
				contents, err := os.ReadFile(temporaryPath)
				if err == nil && string(contents) != material+"\n" {
					err = errors.New("temporary keyfile is incomplete")
				}
				publishReady <- err
				if err != nil {
					return err
				}
				<-releasePublish
				return publishSecureKeyFile(temporaryPath, finalPath)
			},
			func(string) error { return nil },
		)
	}()

	select {
	case err := <-publishReady:
		if err != nil {
			t.Fatalf("temporary keyfile validation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for atomic keyfile publication")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("final keyfile became visible before publication: %v", err)
	}

	close(releasePublish)
	if err := <-done; err != nil {
		t.Fatalf("persistGeneratedKeyMaterial() error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read published keyfile: %v", err)
	}
	if string(contents) != material+"\n" {
		t.Fatalf("published keyfile = %q, want complete material", contents)
	}
}

func TestLoadOrCreateKeyMaterialWaitsForDirectorySyncBeforeUse(t *testing.T) {
	dataDir := t.TempDir()
	syncFailure := errors.New("directory sync failed")
	syncCalls := 0
	syncDirectory := func(string) error {
		syncCalls++
		if syncCalls == 1 {
			return syncFailure
		}
		return nil
	}

	material, err := loadOrCreateKeyMaterial("", dataDir, syncDirectory)
	if !errors.Is(err, syncFailure) {
		t.Fatalf("first LoadOrCreateKeyMaterial() error = %v, want directory sync failure", err)
	}
	if material != "" {
		t.Fatalf("key material returned before directory sync = %q", material)
	}

	path := filepath.Join(dataDir, KeyFileName)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read keyfile retained after sync failure: %v", err)
	}
	want := strings.TrimSpace(string(contents))
	if len(want) != encodedKeyMaterialBytes {
		t.Fatalf("retained key material length = %d, want %d", len(want), encodedKeyMaterialBytes)
	}

	got, err := loadOrCreateKeyMaterial("", dataDir, syncDirectory)
	if err != nil {
		t.Fatalf("second LoadOrCreateKeyMaterial() error = %v", err)
	}
	if got != want {
		t.Fatalf("second LoadOrCreateKeyMaterial() = %q, want retained material %q", got, want)
	}
	if syncCalls != 2 {
		t.Fatalf("directory sync calls = %d, want 2", syncCalls)
	}
}

func TestLoadDurableKeyMaterialSyncsDirectoryBeforeUse(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, KeyFileName)
	want := hex.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(path, []byte(want+"\n"), 0o600); err != nil {
		t.Fatalf("write keyfile: %v", err)
	}

	syncCalls := 0
	syncDirectory := func(string) error {
		syncCalls++
		return nil
	}

	got, err := loadDurableKeyMaterial(path, syncDirectory)
	if err != nil {
		t.Fatalf("loadDurableKeyMaterial() error = %v", err)
	}
	if got != want {
		t.Fatalf("loadDurableKeyMaterial() = %q, want %q", got, want)
	}
	if syncCalls != 1 {
		t.Fatalf("directory sync calls = %d, want 1", syncCalls)
	}
}

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
