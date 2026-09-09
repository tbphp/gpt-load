package state

import (
	"container/list"
	"fmt"
	"sort"
	"sync"
	"time"
)

const (
	DefaultResponseBindingTTL      = 30 * 24 * time.Hour
	DefaultResponseBindingCapacity = 100_000
	maxResponseIDBytes             = 4 << 10
	maxResponseBindingIDBytes      = 16 << 20
)

// ResponseBinding 只保存响应归属；可用性仍由当前路由和凭据运行态决定。
type ResponseBinding struct {
	AccessKeyID        uint      `json:"access_key_id"`
	ResponseID         string    `json:"response_id"`
	GroupID            uint      `json:"group_id"`
	CredentialID       uint      `json:"credential_id"`
	IdentityGeneration uint64    `json:"identity_generation"`
	ExpiresAt          time.Time `json:"expires_at"`
}

type responseBindingKey struct {
	accessKeyID uint
	responseID  string
}

// ResponseBindings 是有界的内存归属索引，不持有 DB、文件或软亲和配置。
type ResponseBindings struct {
	mu       sync.Mutex
	entries  map[responseBindingKey]*list.Element
	order    list.List
	idBytes  int
	capacity int
	ttl      time.Duration
	now      func() time.Time
}

func NewResponseBindings() *ResponseBindings {
	return &ResponseBindings{
		entries:  make(map[responseBindingKey]*list.Element),
		capacity: DefaultResponseBindingCapacity,
		ttl:      DefaultResponseBindingTTL,
		now:      time.Now,
	}
}

func (bindings *ResponseBindings) Lookup(accessKeyID uint, responseID string) (ResponseBinding, bool) {
	if bindings == nil {
		return ResponseBinding{}, false
	}
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	element := bindings.entries[responseBindingKey{accessKeyID, responseID}]
	if element == nil {
		return ResponseBinding{}, false
	}
	binding := element.Value.(ResponseBinding)
	if !binding.ExpiresAt.After(bindings.now()) {
		bindings.remove(element)
		return ResponseBinding{}, false
	}
	return binding, true
}

// Record 在响应下发前登记；不同归属冲突时拒绝当前响应，不覆盖已有归属。
func (bindings *ResponseBindings) Record(accessKeyID uint, responseID string, ref CredentialRef) bool {
	if bindings == nil || accessKeyID == 0 || responseID == "" || ref.ID == 0 ||
		ref.GroupID == 0 || ref.IdentityGeneration == 0 || len(responseID) > maxResponseIDBytes {
		return false
	}
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	now := bindings.now()
	bindings.expire(now)
	return bindings.insert(ResponseBinding{
		AccessKeyID: accessKeyID, ResponseID: responseID,
		GroupID: ref.GroupID, CredentialID: ref.ID, IdentityGeneration: ref.IdentityGeneration,
		ExpiresAt: now.Add(bindings.ttl),
	})
}

func (bindings *ResponseBindings) insert(binding ResponseBinding) bool {
	key := responseBindingKey{binding.AccessKeyID, binding.ResponseID}
	if element := bindings.entries[key]; element != nil {
		existing := element.Value.(ResponseBinding)
		return existing.GroupID == binding.GroupID && existing.CredentialID == binding.CredentialID &&
			existing.IdentityGeneration == binding.IdentityGeneration
	}
	if bindings.capacity <= 0 || bindings.ttl <= 0 {
		return false
	}
	for len(bindings.entries) >= bindings.capacity || bindings.idBytes+len(binding.ResponseID) > maxResponseBindingIDBytes {
		bindings.remove(bindings.order.Front())
	}
	bindings.entries[key] = bindings.order.PushBack(binding)
	bindings.idBytes += len(binding.ResponseID)
	return true
}

func (bindings *ResponseBindings) CaptureCheckpoint() []ResponseBinding {
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	bindings.expire(bindings.now())
	checkpoint := make([]ResponseBinding, 0, len(bindings.entries))
	for element := bindings.order.Front(); element != nil; element = element.Next() {
		checkpoint = append(checkpoint, element.Value.(ResponseBinding))
	}
	return checkpoint
}

// RestoreCheckpoint 只恢复归属，不在这里复制路由、权限和健康判断。
func (bindings *ResponseBindings) RestoreCheckpoint(checkpoint []ResponseBinding) error {
	ordered := append([]ResponseBinding(nil), checkpoint...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ExpiresAt.Before(ordered[j].ExpiresAt) })
	bindings.mu.Lock()
	defer bindings.mu.Unlock()
	bindings.reset()
	now := bindings.now()
	for _, binding := range ordered {
		if binding.AccessKeyID == 0 || binding.ResponseID == "" || binding.CredentialID == 0 ||
			binding.GroupID == 0 || binding.IdentityGeneration == 0 || !binding.ExpiresAt.After(now) ||
			len(binding.ResponseID) > maxResponseIDBytes {
			continue
		}
		if !bindings.insert(binding) {
			bindings.reset()
			return fmt.Errorf("response checkpoint contains conflicting ownership")
		}
	}
	return nil
}

func (bindings *ResponseBindings) reset() {
	bindings.entries = make(map[responseBindingKey]*list.Element)
	bindings.order.Init()
	bindings.idBytes = 0
}

func (bindings *ResponseBindings) expire(now time.Time) {
	for element := bindings.order.Front(); element != nil; element = bindings.order.Front() {
		if element.Value.(ResponseBinding).ExpiresAt.After(now) {
			return
		}
		bindings.remove(element)
	}
}

func (bindings *ResponseBindings) remove(element *list.Element) {
	binding := element.Value.(ResponseBinding)
	delete(bindings.entries, responseBindingKey{binding.AccessKeyID, binding.ResponseID})
	bindings.idBytes -= len(binding.ResponseID)
	bindings.order.Remove(element)
}
