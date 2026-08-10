package catalog

import "sync/atomic"

// Runtime publishes whole immutable catalog generations atomically. Published
// generations are never exposed or mutated; every public data-returning read
// copies only the caller-visible boundary it returns.
type Runtime struct {
	snapshot atomic.Pointer[Snapshot]
}

// Load returns a caller-owned deep clone of the current snapshot.
func (runtime *Runtime) Load() *Snapshot {
	if runtime == nil {
		return nil
	}
	return cloneSnapshot(runtime.snapshot.Load())
}

// HasGeneration reports whether a catalog generation is currently published.
// It does not clone the generation and is safe because published snapshots are
// immutable inside Runtime.
func (runtime *Runtime) HasGeneration() bool {
	return runtime != nil && runtime.snapshot.Load() != nil
}

// Publish deep-clones and atomically replaces the current snapshot.
func (runtime *Runtime) Publish(snapshot *Snapshot) {
	if runtime == nil {
		return
	}
	runtime.snapshot.Store(cloneSnapshot(snapshot))
}
