package state

import (
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

func (m *Manager) Publish(input CompileInput) (*ConfigSnapshot, error) {
	next, err := Compile(input)
	if err != nil {
		return nil, err
	}

	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	next.Revision = 1
	if current := m.current.Load(); current != nil {
		next.Revision = current.Revision + 1
	}
	m.current.Store(next)
	return next, nil
}
