package state

import (
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/ratelimit"
)

type Manager struct {
	concurrency *ratelimit.Concurrency
	scheduling  *SchedulingState
	affinity    AffinitySynchronizer
	publishMu   sync.RWMutex
	current     atomic.Pointer[ConfigSnapshot]
	reconciler  SnapshotReconciler
	updates     chan struct{}
}

// AffinitySynchronizer receives runtime affinity updates on snapshot publication.
type AffinitySynchronizer interface {
	Configure(revision uint64, affinityRevision uint64, capacity int, ttl time.Duration) bool
}

// SnapshotReconciler synchronizes infrastructure resources derived from a
// compiled configuration before it becomes visible to the data plane.
type SnapshotReconciler interface {
	ReconcileConfigSnapshot(*ConfigSnapshot) error
}

func NewManager() *Manager {
	return &Manager{updates: make(chan struct{}), concurrency: ratelimit.NewConcurrency()}
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
// must not decrypt, probe, compile, access DB/network, log, or acquire mutation
// stripes. Callers needing a stripe acquire it before entering this boundary:
// MutationCoordinator stripe -> publishMu -> Registry/Stats internal locks.
func (m *Manager) WithCurrentSnapshot(fn func(*ConfigSnapshot) bool) bool {
	if m == nil || fn == nil {
		return false
	}
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	return fn(m.current.Load())
}

// WithCurrentSnapshotRead runs a short read-only callback while publication
// is blocked. Multiple data-plane readers may run concurrently. Callbacks may
// only read the snapshot and perform quota or concurrency admission following
// the lock order publishMu.RLock -> quota/concurrency runtime locks.
func (m *Manager) WithCurrentSnapshotRead(fn func(*ConfigSnapshot) bool) bool {
	if m == nil || fn == nil {
		return false
	}
	m.publishMu.RLock()
	defer m.publishMu.RUnlock()
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
	c := sanitizeSnapshotForComparison(current)
	n := sanitizeSnapshotForComparison(next)
	c.Policies = current.Policies
	n.Policies = next.Policies
	return reflect.DeepEqual(&c, &n), nil
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
	next.AffinityRevision = 1
	if current := m.current.Load(); current != nil {
		next.Revision = current.Revision + 1
		if isPolicyOnlyPublication(current, next) {
			next.AffinityRevision = current.AffinityRevision
		} else {
			next.AffinityRevision = current.AffinityRevision + 1
		}
	}
	if m.affinity != nil {
		m.affinity.Configure(next.Revision, next.AffinityRevision, next.Settings.AffinityCapacity, next.Settings.AffinityTTL)
	}
	if m.scheduling != nil {
		m.scheduling.SyncGroups(next)
	}
	m.current.Store(next)
	if m.updates != nil {
		close(m.updates)
	}
	m.updates = make(chan struct{})
	return next
}

func isPolicyOnlyPublication(current, next *ConfigSnapshot) bool {
	if current == nil || next == nil {
		return false
	}
	c := sanitizeSnapshotForComparison(current)
	n := sanitizeSnapshotForComparison(next)
	return reflect.DeepEqual(&c, &n)
}

func sanitizeSnapshotForComparison(s *ConfigSnapshot) ConfigSnapshot {
	c := *s
	c.Revision = 0
	c.AffinityRevision = 0
	c.Policies = nil
	c.Groups = cloneGroupsStrippingResolvers(c.Groups)
	c.ExecutionCandidates = cloneExecutionIndexStrippingResolvers(c.ExecutionCandidates)
	c.ExecutionRouteCatalog = cloneExecutionIndexStrippingResolvers(c.ExecutionRouteCatalog)
	return c
}

func cloneGroupsStrippingResolvers(groups map[uint]GroupView) map[uint]GroupView {
	if groups == nil {
		return nil
	}
	out := make(map[uint]GroupView, len(groups))
	for id, gv := range groups {
		gv.ResolvedTarget = gv.ResolvedTarget.WithoutResolverFunctions()
		out[id] = gv
	}
	return out
}

func cloneExecutionIndexStrippingResolvers(idx ExecutionCandidateIndex) ExecutionCandidateIndex {
	if idx == nil {
		return nil
	}
	out := make(ExecutionCandidateIndex, len(idx))
	for proto, byOp := range idx {
		newByOp := make(map[execution.Operation]map[string][]RouteTarget, len(byOp))
		for op, byModel := range byOp {
			newByModel := make(map[string][]RouteTarget, len(byModel))
			for model, targets := range byModel {
				newTargets := make([]RouteTarget, len(targets))
				for i, target := range targets {
					target.ResolvedTarget = target.ResolvedTarget.WithoutResolverFunctions()
					newTargets[i] = target
				}
				newByModel[model] = newTargets
			}
			newByOp[op] = newByModel
		}
		out[proto] = newByOp
	}
	return out
}

// SetSchedulingState 将分组配置发布与单实例调度状态衔接；不持有 Registry 锁。
func (m *Manager) SetSchedulingState(scheduling *SchedulingState) {
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	m.scheduling = scheduling
	if scheduling != nil {
		scheduling.SyncGroups(m.current.Load())
	}
}

// SetAffinitySynchronizer binds an affinity cache to receive synchronous publication updates.
func (m *Manager) SetAffinitySynchronizer(sync AffinitySynchronizer) {
	if m == nil {
		return
	}
	m.publishMu.Lock()
	defer m.publishMu.Unlock()
	m.affinity = sync
	if sync != nil {
		if current := m.current.Load(); current != nil {
			sync.Configure(current.Revision, current.AffinityRevision, current.Settings.AffinityCapacity, current.Settings.AffinityTTL)
		}
	}
}

// Concurrency 返回独立于配置快照的进程级业务计数。
func (m *Manager) Concurrency() *ratelimit.Concurrency { return m.concurrency }
