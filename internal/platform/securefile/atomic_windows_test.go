//go:build windows

package securefile

import (
	"errors"
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

// Windows replacement can be refused while the target has an open reader,
// including a reader with DELETE sharing. A refused save must preserve the
// old file; publication must succeed once that reader releases its handle.
func TestAtomicWriteWindowsOpenReaderAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		sharing  uint32
		mustFail bool
	}{
		{"read-only-sharing", windows.FILE_SHARE_READ, true},
		{"delete-sharing", windows.FILE_SHARE_READ | windows.FILE_SHARE_WRITE | windows.FILE_SHARE_DELETE, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "checkpoint.json")
			old, next := `{"old":true}`, `{"new":true}`
			if err := WriteAtomic(path, []byte(old)); err != nil {
				t.Fatal(err)
			}
			name, err := windows.UTF16PtrFromString(path)
			if err != nil {
				t.Fatal(err)
			}
			h, err := windows.CreateFile(name, windows.GENERIC_READ, tc.sharing, nil, windows.OPEN_EXISTING, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			reader := os.NewFile(uintptr(h), path)
			defer func() {
				if reader != nil {
					_ = reader.Close()
				}
			}()
			err = WriteAtomic(path, []byte(next))
			want := next
			if err != nil {
				if !errors.Is(err, windows.ERROR_ACCESS_DENIED) && !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
					t.Fatalf("unexpected replacement error: %v", err)
				}
				want = old
				t.Logf("open target refused replacement without losing old contents: %v", err)
			} else if tc.mustFail {
				t.Fatal("replacement succeeded without DELETE sharing")
			}
			got, err := os.ReadFile(path)
			if err != nil || string(got) != want {
				t.Fatalf("target=%q want=%q err=%v", got, want, err)
			}
			previous, err := io.ReadAll(reader)
			if err != nil || string(previous) != old {
				t.Fatalf("open reader=%q err=%v", previous, err)
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("temporary cleanup: %v %v", entries, err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			reader = nil
			if err := WriteAtomic(path, []byte(next)); err != nil {
				t.Fatalf("retry after reader close: %v", err)
			}
			got, err = os.ReadFile(path)
			if err != nil || string(got) != next {
				t.Fatalf("retry target=%q err=%v", got, err)
			}
			entries, err = os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatalf("retry temporary cleanup: %v %v", entries, err)
			}
			assertCurrentUserOnlyACL(t, path)
		})
	}
}
