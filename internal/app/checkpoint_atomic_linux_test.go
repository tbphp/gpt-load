package app

import (
	"context"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"
)

func TestCheckpointAtomicRealPartialWrite(t *testing.T) {
	if dir := os.Getenv("CHECKPOINT_ATOMIC_RLIMIT"); dir != "" {
		signal.Ignore(syscall.SIGXFSZ)
		if e := syscall.Setrlimit(syscall.RLIMIT_FSIZE, &syscall.Rlimit{Cur: 1, Max: 1}); e != nil {
			t.Fatal(e)
		}
		c := NewFileRuntimeStateCheckpoint(dir, nil, nil, nil)
		if e := c.Save(context.Background()); e == nil {
			t.Fatal("expected EFBIG")
		} else {
			t.Logf("actual Save failure %v", e)
		}
		return
	}
	dir := t.TempDir()
	p := filepath.Join(dir, runtimeStateCheckpointFileName)
	old := `{"stats":[]}`
	if e := os.WriteFile(p, []byte(old), 0600); e != nil {
		t.Fatal(e)
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestCheckpointAtomicRealPartialWrite$", "-test.v")
	cmd.Env = append(os.Environ(), "CHECKPOINT_ATOMIC_RLIMIT="+dir)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("%s %v", out, e)
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != old {
		t.Fatalf("%q %v", b, e)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatal(entries)
	}
	t.Logf("%s old preserved and temp removed", out)
}
func TestCheckpointAtomicRealCreateFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires unprivileged process")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, runtimeStateCheckpointFileName)
	old := `{"stats":[]}`
	if e := os.WriteFile(p, []byte(old), 0600); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(dir, 0500); e != nil {
		t.Fatal(e)
	}
	defer os.Chmod(dir, 0700)
	if e := NewFileRuntimeStateCheckpoint(dir, nil, nil, nil).Save(context.Background()); e == nil {
		t.Fatal("expected permission failure")
	}
	b, e := os.ReadFile(p)
	if e != nil || string(b) != old {
		t.Fatalf("%q %v", b, e)
	}
}
