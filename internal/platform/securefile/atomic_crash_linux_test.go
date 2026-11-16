package securefile

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
)

func TestAtomicWriteProcessKill(t *testing.T) {
	if path := os.Getenv("CHECKPOINT_CRASH_PATH"); path != "" {
		stage := os.Getenv("CHECKPOINT_CRASH_STAGE")
		ops := defaultAtomicWriteOps()
		base := ops
		kill := func() { _ = syscall.Kill(os.Getpid(), syscall.SIGKILL); panic("kill failed") }
		if stage == "partial" {
			ops.secure = func(f atomicFile) error {
				if e := base.secure(f); e != nil {
					return e
				}
				if _, e := f.Write([]byte(`{"new":`)); e != nil {
					return e
				}
				kill()
				return nil
			}
		}
		if stage == "synced" {
			ops.replace = func(string, string) error { kill(); return nil }
		}
		if stage == "published" {
			ops.syncDirectory = func(string) error { kill(); return nil }
		}
		_ = writeAtomic(path, []byte(`{"new":true}`), ops)
		t.Fatal("child survived")
		return
	}
	for _, stage := range []string{"partial", "synced", "published"} {
		t.Run(stage, func(t *testing.T) {
			dir := t.TempDir()
			p := filepath.Join(dir, "checkpoint.json")
			if e := os.WriteFile(p, []byte(`{"old":true}`), 0600); e != nil {
				t.Fatal(e)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestAtomicWriteProcessKill$")
			cmd.Env = append(os.Environ(), "CHECKPOINT_CRASH_PATH="+p, "CHECKPOINT_CRASH_STAGE="+stage)
			out, e := cmd.CombinedOutput()
			if e == nil {
				t.Fatalf("expected SIGKILL %s", out)
			}
			exit, ok := e.(*exec.ExitError)
			if !ok || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
				t.Fatalf("not SIGKILL %s %v", out, e)
			}
			b, e := os.ReadFile(p)
			want := `{"old":true}`
			count := 2
			if stage == "published" {
				want = `{"new":true}`
				count = 1
			}
			if e != nil || string(b) != want || !json.Valid(b) {
				t.Fatalf("%q %v", b, e)
			}
			entries, _ := os.ReadDir(dir)
			if len(entries) != count {
				t.Fatal(entries)
			}
			t.Logf("SIGKILL stage=%s target=%s remaining_files=%d", stage, b, count)
		})
	}
}
