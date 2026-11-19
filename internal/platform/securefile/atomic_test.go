package securefile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
)

type faultAtomicFile struct {
	atomicFile
	stage string
	fault error
	order *[]string
}

func (f *faultAtomicFile) Write(b []byte) (int, error) {
	*f.order = append(*f.order, "write")
	if f.stage == "write" {
		n, e := f.atomicFile.Write(b[:len(b)/2])
		if e != nil {
			return n, e
		}
		return n, f.fault
	}
	if f.stage == "short" {
		return f.atomicFile.Write(b[:len(b)/2])
	}
	return f.atomicFile.Write(b)
}
func (f *faultAtomicFile) Sync() error {
	*f.order = append(*f.order, "sync")
	if f.stage == "sync" {
		return f.fault
	}
	return f.atomicFile.Sync()
}
func (f *faultAtomicFile) Close() error {
	*f.order = append(*f.order, "close")
	e := f.atomicFile.Close()
	if f.stage == "close" {
		return errors.Join(e, f.fault)
	}
	return e
}
func TestAtomicWriteFaults(t *testing.T) {
	for _, stage := range []string{"create", "secure", "write", "short", "sync", "close", "replace", "directory", "success"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "runtime-state.checkpoint.json")
			old := []byte(`{"stats":[]}`)
			next := []byte(`{"credentials":[]}`)
			if e := os.WriteFile(path, old, 0600); e != nil {
				t.Fatal(e)
			}
			fault := errors.New("injected " + stage)
			ops := defaultAtomicWriteOps()
			base := ops
			var order []string
			ops.create = func(dir, name string) (atomicFile, string, error) {
				order = append(order, "create")
				if stage == "create" {
					return nil, "", fault
				}
				f, p, e := base.create(dir, name)
				if e != nil {
					return nil, "", e
				}
				if filepath.Dir(p) != dir {
					t.Fatal("not sibling")
				}
				return &faultAtomicFile{f, stage, fault, &order}, p, nil
			}
			ops.secure = func(f atomicFile) error {
				order = append(order, "secure")
				if stage == "secure" {
					return fault
				}
				return base.secure(f.(*faultAtomicFile).atomicFile)
			}
			ops.replace = func(a, b string) error {
				order = append(order, "replace")
				if stage == "replace" {
					return fault
				}
				return base.replace(a, b)
			}
			ops.syncDirectory = func(p string) error {
				order = append(order, "directory")
				if stage == "directory" {
					return fault
				}
				return base.syncDirectory(p)
			}
			e := writeAtomic(path, next, ops)
			if stage == "success" {
				if e != nil {
					t.Fatal(e)
				}
			} else if stage == "short" {
				if !errors.Is(e, io.ErrShortWrite) {
					t.Fatal(e)
				}
			} else if !errors.Is(e, fault) {
				t.Fatalf("lost error %v", e)
			}
			got, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			want := old
			if stage == "success" || stage == "directory" {
				want = next
			}
			if !bytes.Equal(got, want) || !json.Valid(got) {
				t.Fatalf("target %q", got)
			}
			entries, e := os.ReadDir(dir)
			if e != nil || len(entries) != 1 {
				t.Fatalf("temp leak %v %v", entries, e)
			}
			if stage == "success" && fmt.Sprint(order) != "[create secure write sync close replace directory]" {
				t.Fatal(order)
			}
			t.Logf("stage=%s order=%v target=%s temp_count=0", stage, order, got)
		})
	}
}
func TestAtomicWriteFirstPublicationAndReplacement(t *testing.T) {
	p := filepath.Join(t.TempDir(), "checkpoint.json")
	for i := 0; i < 10; i++ {
		b := []byte(fmt.Sprintf(`{"value":%d}`, i))
		if e := WriteAtomic(p, b); e != nil {
			t.Fatal(e)
		}
		got, e := os.ReadFile(p)
		if e != nil || !bytes.Equal(got, b) {
			t.Fatalf("%q %v", got, e)
		}
	}
}
func TestAtomicWriteCreateRealFailure(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "missing", "checkpoint.json")
	if e := WriteAtomic(p, []byte(`{}`)); e == nil {
		t.Fatal("expected missing directory")
	}
}
func TestAtomicWriteCleanupError(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "checkpoint.json")
	ops := defaultAtomicWriteOps()
	writeErr := errors.New("secure failed")
	cleanupErr := errors.New("remove failed")
	ops.secure = func(atomicFile) error { return writeErr }
	ops.remove = func(string) error { return cleanupErr }
	e := writeAtomic(p, []byte(`{}`), ops)
	if !errors.Is(e, writeErr) || !errors.Is(e, cleanupErr) {
		t.Fatal(e)
	}
}
