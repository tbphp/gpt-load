package state

import (
	"reflect"
	"sync"
	"sync/atomic"
)

type Manager struct {
	publishMu  sync.Mutex
	current    atomic.Pointer[ConfigSnapshot]
	reconciler SnapshotReconciler
	updates    chan struct{}
}

// SnapshotReconciler synchronizes infrastructure resources derived from a
// compiled configuration before it becomes visible to the data plane.
type SnapshotReconciler interface {
	ReconcileConfigSnapshot(*ConfigSnapshot) error
}

func NewManager() *Manager {
	return &Manager{updates: make(chan struct{})}
}

// SetSnapshotReconciler installs the process-owned runtime reconciler during
// dependency assembly, before the first snapshot publication.
func (m *Manager) SetSnapshotReconciler(reconciler SnapshotReconciler) {
	if m == nil {
		return
	}
	m.publishMu.Lock()
	m.reconciler = reconciler
	m.publishMu.Unlock()
}

func (m *Manager) Current() *ConfigSnapshot {
	return m.current.Load()
}

// CurrentWithUpdates returns a snapshot and a channel closed by the next
// successful publication. The pair is captured under the publication lock so
// consumers cannot miss a change between reading the snapshot and subscribing.
func (m *Manager) CurrentWithUpdates() (*ConfigSnapshot, <-chan struct{}) {
	if m == nil {
		return nil, nil
	}
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	if m.updates == nil {
		m.updates = make(chan struct{})
	}
	return m.current.Load(), m.updates
}

// WithCurrentSnapshot runs a short callback while publication is blocked.
// Allowed callback work is limited to loading/reading the current snapshot,
// pure signature recomputation, and coordinated Registry/Stats recovery. It
// must not decrypt, probe, compile, access DB/network, or log. The lock order
// is publishMu -> MutationCoordinator stripe -> Registry/Stats internal locks.
func (m *Manager) WithCurrentSnapshot(fn func(*ConfigSnapshot) bool) bool {
	if m == nil || fn == nil {
		return false
	}
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	return fn(m.current.Load())
}

func (m *Manager) Publish(input CompileInput) (*ConfigSnapshot, error) {
	next, err := Compile(input)
	if err != nil {
		return nil, err
	}
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	if m.reconciler != nil {
		if err := m.reconciler.ReconcileConfigSnapshot(next); err != nil {
			return nil, err
		}
	}
	return m.publishCompiledLocked(next), nil
}

// Matches reports whether input compiles to the currently published runtime
// configuration. Snapshot revisions are ordering metadata and are ignored.
func (m *Manager) Matches(input CompileInput) (bool, error) {
	next, err := Compile(input)
	if err != nil {
		return false, err
	}
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	current := m.current.Load()
	if current == nil {
		return false, nil
	}
	currentValue := *current
	currentValue.Revision = 0
	return reflect.DeepEqual(&currentValue, next), nil
}

func (m *Manager) publishCompiled(next *ConfigSnapshot, beforeLock func()) *ConfigSnapshot {
	if beforeLock != nil {
		beforeLock()
	}
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	return m.publishCompiledLocked(next)
}

func (m *Manager) publishCompiledLocked(next *ConfigSnapshot) *ConfigSnapshot {
	next.Revision = 1
	if current := m.current.Load(); current != nil {
		next.Revision = current.Revision + 1
	}
	m.current.Store(next)
	if m.updates != nil {
		close(m.updates)
	}
	m.updates = make(chan struct{})
	return next
}
