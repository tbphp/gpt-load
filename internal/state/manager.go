package state

import (
	"reflect"
	"sync"
	"sync/atomic"
)

type Manager struct {
	publishMu sync.Mutex
	current   atomic.Pointer[ConfigSnapshot]
}

func NewManager() *Manager {
	return &Manager{}
}

func (m *Manager) Current() *ConfigSnapshot {
	return m.current.Load()
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
	return m.publishCompiled(next, nil), nil
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
	next.Revision = 1
	if current := m.current.Load(); current != nil {
		next.Revision = current.Revision + 1
	}
	m.current.Store(next)
	return next
}
