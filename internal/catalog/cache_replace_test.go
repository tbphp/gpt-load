package catalog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicCatalogReplaceReplacesExistingSibling(t *testing.T) {
	directory := t.TempDir()
	temporaryPath := filepath.Join(directory, ".models.dev.catalog.json.new.tmp")
	finalPath := filepath.Join(directory, "models.dev.catalog.json")
	if err := os.WriteFile(temporaryPath, []byte("new catalog bytes\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(temporary) error = %v", err)
	}
	if err := os.WriteFile(finalPath, []byte("old catalog bytes\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(final) error = %v", err)
	}

	if err := replaceCatalogFileAtomic(temporaryPath, finalPath); err != nil {
		t.Fatalf("replaceCatalogFileAtomic() error = %v", err)
	}
	contents, err := os.ReadFile(finalPath)
	if err != nil {
		t.Fatalf("ReadFile(final) error = %v", err)
	}
	if !bytes.Equal(contents, []byte("new catalog bytes\n")) {
		t.Fatalf("final contents = %q, want new bytes", contents)
	}
	if _, err := os.Stat(temporaryPath); !os.IsNotExist(err) {
		t.Fatalf("temporary path still exists after replace: %v", err)
	}
}

func TestAtomicCatalogReplaceRejectsCrossDirectoryMove(t *testing.T) {
	sourceDirectory := t.TempDir()
	targetDirectory := t.TempDir()
	temporaryPath := filepath.Join(sourceDirectory, "catalog.tmp")
	finalPath := filepath.Join(targetDirectory, "catalog.json")
	if err := os.WriteFile(temporaryPath, []byte("new"), 0o600); err != nil {
		t.Fatalf("WriteFile(temporary) error = %v", err)
	}
	if err := os.WriteFile(finalPath, []byte("old"), 0o600); err != nil {
		t.Fatalf("WriteFile(final) error = %v", err)
	}

	if err := replaceCatalogFileAtomic(temporaryPath, finalPath); err == nil {
		t.Fatal("replaceCatalogFileAtomic() accepted cross-directory replacement")
	}
	if contents, err := os.ReadFile(finalPath); err != nil || string(contents) != "old" {
		t.Fatalf("cross-directory rejection changed final: %q, %v", contents, err)
	}
	if contents, err := os.ReadFile(temporaryPath); err != nil || string(contents) != "new" {
		t.Fatalf("cross-directory rejection changed temporary: %q, %v", contents, err)
	}
}
