//go:build !windows

package securefile

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteUnixPermissionsAndOpenReader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "checkpoint.json")
	if e := os.WriteFile(p, []byte(`{"old":true}`), 0644); e != nil {
		t.Fatal(e)
	}
	f, e := os.Open(p)
	if e != nil {
		t.Fatal(e)
	}
	defer f.Close()
	if e := WriteAtomic(p, []byte(`{"new":true}`)); e != nil {
		t.Fatal(e)
	}
	info, e := os.Stat(p)
	if e != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("mode %v %v", info, e)
	}
	b := make([]byte, 12)
	_, _ = f.Read(b)
	if !bytes.Equal(b, []byte(`{"old":true}`)) {
		t.Fatalf("old descriptor %q", b)
	}
}
func TestAtomicWriteUnixRenameFailure(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "target")
	if e := os.Mkdir(p, 0700); e != nil {
		t.Fatal(e)
	}
	if e := WriteAtomic(p, []byte(`{}`)); e == nil {
		t.Fatal("expected rename failure")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 || !entries[0].IsDir() {
		t.Fatal(entries)
	}
}

func readAtomicTestTarget(p string) ([]byte, error) { return os.ReadFile(p) }
