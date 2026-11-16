package app

import (
	"context"
	"encoding/json"
	"gpt-load/internal/state"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestCheckpointAtomicRepeatedSaveRestore(t *testing.T) {
	dir := t.TempDir()
	c := NewFileRuntimeStateCheckpoint(dir, nil, nil, nil)
	for i := 0; i < 10; i++ {
		if e := c.Save(context.Background()); e != nil {
			t.Fatal(e)
		}
	}
	if e := c.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(dir, runtimeStateCheckpointFileName)); !os.IsNotExist(e) {
		t.Fatal(e)
	}
	if e := c.Restore(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func TestCheckpointAtomicConcurrentSaveRestore(t *testing.T) {
	c := NewFileRuntimeStateCheckpoint(t.TempDir(), state.NewCredentialRegistry(), nil, nil)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var e error
			if i%3 == 0 {
				e = c.Restore(context.Background())
			} else {
				e = c.Save(context.Background())
			}
			if e != nil {
				t.Error(e)
			}
		}(i)
	}
	wg.Wait()
	if e := c.Save(context.Background()); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(c.path)
	if e != nil || !json.Valid(b) {
		t.Fatalf("%q %v", b, e)
	}
}
func TestCheckpointAtomicCanceledPreservesOld(t *testing.T) {
	c := NewFileRuntimeStateCheckpoint(t.TempDir(), nil, nil, nil)
	old := []byte(`{"stats":[]}`)
	if e := os.WriteFile(c.path, old, 0600); e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := c.Save(ctx); e != context.Canceled {
		t.Fatal(e)
	}
	b, e := os.ReadFile(c.path)
	if e != nil || string(b) != string(old) {
		t.Fatalf("%q %v", b, e)
	}
}
