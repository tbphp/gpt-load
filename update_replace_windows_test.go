//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReplaceExecutableRemovesUnlockedLeftover(t *testing.T) {
	directory := t.TempDir()
	target := filepath.Join(directory, "gpt-load.exe")
	replacement := filepath.Join(directory, ".gpt-load.exe.update-1")
	for path, content := range map[string]string{
		target:                            "old binary",
		target + replacedExecutableSuffix: "older binary",
		replacement:                       "new binary",
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := replaceExecutable(target, replacement); err != nil {
		t.Fatalf("replaceExecutable() error = %v", err)
	}

	assertExecutableContent(t, target, "new binary")
	assertOnlyExecutableRemains(t, target)
}
