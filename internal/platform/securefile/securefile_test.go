package securefile

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestLoadOrCreateHexCreatesAndReusesMaterial(t *testing.T) {
	dataDir := t.TempDir()

	result, err := LoadOrCreateHex(dataDir, "auth.key")
	if err != nil {
		t.Fatalf("LoadOrCreateHex() error = %v", err)
	}
	if result.Path != filepath.Join(dataDir, "auth.key") || !result.Created {
		t.Fatalf("result = %#v", result)
	}
	if len(result.Value) != 64 {
		t.Fatalf("material length = %d, want 64", len(result.Value))
	}
	if _, err := hex.DecodeString(result.Value); err != nil {
		t.Fatalf("material is not lowercase hex: %v", err)
	}

	reused, err := LoadOrCreateHex(dataDir, "auth.key")
	if err != nil {
		t.Fatalf("reuse LoadOrCreateHex() error = %v", err)
	}
	if reused.Value != result.Value || reused.Created {
		t.Fatalf("reused result = %#v, first = %#v", reused, result)
	}
}

func TestLoadOrCreateHexUsesRestrictivePlatformPermissions(t *testing.T) {
	dataDir := t.TempDir()

	result, err := LoadOrCreateHex(dataDir, "auth.key")
	if err != nil {
		t.Fatalf("LoadOrCreateHex() error = %v", err)
	}
	info, err := os.Stat(result.Path)
	if err != nil {
		t.Fatalf("Stat(secure file) error = %v", err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("secure file permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestLoadOrCreateHexConcurrentFirstStartReturnsOneMaterial(t *testing.T) {
	dataDir := t.TempDir()
	const workers = 32
	start := make(chan struct{})
	results := make(chan Result, workers)
	errors := make(chan error, workers)
	var waitGroup sync.WaitGroup

	for range workers {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			result, err := LoadOrCreateHex(dataDir, "auth.key")
			if err != nil {
				errors <- err
				return
			}
			results <- result
		}()
	}
	close(start)
	waitGroup.Wait()
	close(results)
	close(errors)

	for err := range errors {
		t.Errorf("concurrent LoadOrCreateHex() error = %v", err)
	}
	var want string
	created := 0
	for result := range results {
		if want == "" {
			want = result.Value
		}
		if result.Value != want {
			t.Errorf("concurrent material = %q, want %q", result.Value, want)
		}
		if result.Created {
			created++
		}
	}
	if want == "" {
		t.Fatal("no concurrent caller returned material")
	}
	if created != 1 {
		t.Fatalf("created results = %d, want 1", created)
	}
}

func TestLoadOrCreateHexRejectsInvalidFileName(t *testing.T) {
	for _, fileName := range []string{"", ".", "..", "nested/auth.key", `nested\auth.key`} {
		t.Run(fileName, func(t *testing.T) {
			if _, err := LoadOrCreateHex(t.TempDir(), fileName); err == nil {
				t.Fatalf("LoadOrCreateHex(%q) error = nil, want invalid filename error", fileName)
			}
		})
	}
}

func TestLoadOrCreateHexRejectsCorruptExistingFile(t *testing.T) {
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
			path := filepath.Join(dataDir, "auth.key")
			if err := os.WriteFile(path, []byte(tt.contents), 0o600); err != nil {
				t.Fatalf("write corrupt secure file: %v", err)
			}
			if _, err := LoadOrCreateHex(dataDir, "auth.key"); err == nil {
				t.Fatal("LoadOrCreateHex() error = nil, want corrupt secure file error")
			}
		})
	}
}

func TestLoadOrCreateHexRejectsDirectory(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dataDir, "auth.key"), 0o700); err != nil {
		t.Fatalf("create directory at secure file path: %v", err)
	}

	if _, err := LoadOrCreateHex(dataDir, "auth.key"); err == nil {
		t.Fatal("LoadOrCreateHex() error = nil, want directory rejection")
	}
}

func TestPersistGeneratedHexPublishesOnlyCompleteFile(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "auth.key")
	material := hex.EncodeToString(make([]byte, 32))
	publishReady := make(chan error, 1)
	releasePublish := make(chan struct{})
	done := make(chan error, 1)

	go func() {
		done <- persistGeneratedHex(
			path,
			"auth.key",
			material,
			func(temporaryPath, finalPath string) error {
				contents, err := os.ReadFile(temporaryPath)
				if err == nil && string(contents) != material+"\n" {
					err = errors.New("temporary secure file is incomplete")
				}
				publishReady <- err
				if err != nil {
					return err
				}
				<-releasePublish
				return publishSecureFile(temporaryPath, finalPath)
			},
			func(string) error { return nil },
		)
	}()

	select {
	case err := <-publishReady:
		if err != nil {
			t.Fatalf("temporary secure file validation: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for atomic secure file publication")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("final secure file became visible before publication: %v", err)
	}

	close(releasePublish)
	if err := <-done; err != nil {
		t.Fatalf("persistGeneratedHex() error = %v", err)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read published secure file: %v", err)
	}
	if string(contents) != material+"\n" {
		t.Fatalf("published secure file = %q, want complete material", contents)
	}
}

func TestLoadOrCreateHexWaitsForDirectorySyncBeforeReturning(t *testing.T) {
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

	result, err := loadOrCreateHex(dataDir, "auth.key", syncDirectory)
	if !errors.Is(err, syncFailure) {
		t.Fatalf("first loadOrCreateHex() error = %v, want directory sync failure", err)
	}
	if result != (Result{}) {
		t.Fatalf("result returned before directory sync = %#v", result)
	}

	path := filepath.Join(dataDir, "auth.key")
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read secure file retained after sync failure: %v", err)
	}
	want := strings.TrimSpace(string(contents))
	if len(want) != encodedMaterialBytes {
		t.Fatalf("retained material length = %d, want %d", len(want), encodedMaterialBytes)
	}

	reused, err := loadOrCreateHex(dataDir, "auth.key", syncDirectory)
	if err != nil {
		t.Fatalf("second loadOrCreateHex() error = %v", err)
	}
	if reused.Value != want || reused.Path != path || reused.Created {
		t.Fatalf("second loadOrCreateHex() result = %#v, want reused material", reused)
	}
	if syncCalls != 2 {
		t.Fatalf("directory sync calls = %d, want 2", syncCalls)
	}
}

func TestLoadDurableHexSyncsDirectoryBeforeReturning(t *testing.T) {
	dataDir := t.TempDir()
	path := filepath.Join(dataDir, "auth.key")
	want := hex.EncodeToString(make([]byte, 32))
	if err := os.WriteFile(path, []byte(want+"\n"), 0o600); err != nil {
		t.Fatalf("write secure file: %v", err)
	}

	syncCalls := 0
	syncDirectory := func(string) error {
		syncCalls++
		return nil
	}

	got, err := loadDurableHex(path, syncDirectory)
	if err != nil {
		t.Fatalf("loadDurableHex() error = %v", err)
	}
	if got != want {
		t.Fatalf("loadDurableHex() = %q, want %q", got, want)
	}
	if syncCalls != 1 {
		t.Fatalf("directory sync calls = %d, want 1", syncCalls)
	}
}
