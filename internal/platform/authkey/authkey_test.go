package authkey

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"
)

func TestResolvePrefersExplicitValueWithoutCreatingFile(t *testing.T) {
	dataDir := t.TempDir()

	value, err := Resolve("explicit-auth-key", dataDir)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if value != "explicit-auth-key" {
		t.Fatalf("Resolve() = %q, want explicit value", value)
	}
	if _, err := os.Stat(filepath.Join(dataDir, FileName)); !os.IsNotExist(err) {
		t.Fatalf("explicit AUTH_KEY created auth.key: %v", err)
	}
}

func TestResolveRejectsWhitespaceInExplicitValue(t *testing.T) {
	for _, explicit := range []string{
		"admin key",
		"admin\tkey",
		"admin\nkey",
		"admin\u00a0key",
	} {
		t.Run(strings.ReplaceAll(explicit, "\n", "newline"), func(t *testing.T) {
			if _, err := Resolve(explicit, t.TempDir()); err == nil {
				t.Fatalf("Resolve(%q) error = nil, want whitespace rejection", explicit)
			}
		})
	}
}

func TestResolveCreatesAndReusesGeneratedValue(t *testing.T) {
	dataDir := t.TempDir()

	generated, err := Resolve("", dataDir)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if len(generated) != 64 {
		t.Fatalf("generated AUTH_KEY length = %d, want 64", len(generated))
	}
	contents, err := os.ReadFile(filepath.Join(dataDir, FileName))
	if err != nil {
		t.Fatalf("read auth.key: %v", err)
	}
	if strings.TrimSpace(string(contents)) != generated {
		t.Fatal("generated AUTH_KEY does not match auth.key")
	}

	reused, err := Resolve("", dataDir)
	if err != nil {
		t.Fatalf("reuse Resolve() error = %v", err)
	}
	if reused != generated {
		t.Fatal("reuse Resolve() changed generated AUTH_KEY")
	}
}

func TestResolveLogsPathWithoutMaterialOnlyWhenCreated(t *testing.T) {
	dataDir := t.TempDir()
	var logs bytes.Buffer
	logger := logrus.StandardLogger()
	originalOutput := logger.Out
	logger.SetOutput(&logs)
	t.Cleanup(func() {
		logger.SetOutput(originalOutput)
	})

	material, err := Resolve("", dataDir)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !strings.Contains(logs.String(), filepath.Join(dataDir, FileName)) {
		t.Fatalf("generation log does not contain path: %s", logs.String())
	}
	if strings.Contains(logs.String(), material) {
		t.Fatal("generation log contains AUTH_KEY material")
	}
	logs.Reset()
	if _, err := Resolve("", dataDir); err != nil {
		t.Fatalf("reuse Resolve() error = %v", err)
	}
	if logs.Len() != 0 {
		t.Fatalf("reuse unexpectedly logged generation: %s", logs.String())
	}
}

func TestResolveRejectsCorruptExistingFile(t *testing.T) {
	dataDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dataDir, FileName), []byte("not-hex\n"), 0o600); err != nil {
		t.Fatalf("write corrupt auth.key: %v", err)
	}

	if _, err := Resolve("", dataDir); err == nil {
		t.Fatal("Resolve() error = nil, want corrupt auth.key rejection")
	}
}
