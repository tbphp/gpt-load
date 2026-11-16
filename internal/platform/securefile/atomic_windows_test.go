//go:build windows

package securefile

import (
	"golang.org/x/sys/windows"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicWriteWindowsRestrictiveACL(t *testing.T) {
	p := filepath.Join(t.TempDir(), "checkpoint.json")
	if e := os.WriteFile(p, []byte(`{"old":true}`), 0600); e != nil {
		t.Fatal(e)
	}
	if e := WriteAtomic(p, []byte(`{"new":true}`)); e != nil {
		t.Fatal(e)
	}
	assertCurrentUserOnlyACL(t, p)
}
func TestAtomicWriteWindowsServiceACL(t *testing.T) {
	service, admin := useTestWindowsServiceACL(t)
	p := filepath.Join(t.TempDir(), "checkpoint.json")
	if e := WriteAtomic(p, []byte(`{}`)); e != nil {
		t.Fatal(e)
	}
	assertACLPrincipals(t, p, service, admin)
	if e := WriteAtomic(p, []byte(`{"new":true}`)); e != nil {
		t.Fatal(e)
	}
	assertACLPrincipals(t, p, service, admin)
}
func TestAtomicWriteWindowsSharingViolationPreservesOld(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "checkpoint.json")
	old := `{"old":true}`
	if e := os.WriteFile(p, []byte(old), 0600); e != nil {
		t.Fatal(e)
	}
	name, e := windows.UTF16PtrFromString(p)
	if e != nil {
		t.Fatal(e)
	}
	h, e := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, 0, 0)
	if e != nil {
		t.Fatal(e)
	}
	defer windows.CloseHandle(h)
	if e := WriteAtomic(p, []byte(`{"new":true}`)); e == nil {
		t.Fatal("expected sharing violation")
	} else {
		t.Logf("actual native replacement failure: %v", e)
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != old {
		t.Fatalf("%q %v", b, e)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal(entries)
	}
}

// DELETE sharing permits publication while this observer holds its old handle.
func readAtomicTestTarget(p string) ([]byte, error) {
	name, e := windows.UTF16PtrFromString(p)
	if e != nil {
		return nil, e
	}
	h, e := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if e != nil {
		return nil, e
	}
	f := os.NewFile(uintptr(h), p)
	defer f.Close()
	return io.ReadAll(f)
}
