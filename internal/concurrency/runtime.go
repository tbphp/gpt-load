// Package concurrency owns process-local, non-blocking request admission.
package concurrency

import (
	"strconv"
	"sync"
)

type Subject string

const (
	Global            Subject = "global"
	Upstream          Subject = "upstream"
	DefaultGroup      Subject = "default_group"
	DefaultAccessKey  Subject = "default_access_key"
	DefaultCredential Subject = "default_credential"
	MaximumLimit      int64   = 1_000_000
)

func Group(id uint) Subject                    { return Subject("group:" + strconv.FormatUint(uint64(id), 10)) }
func AccessKey(id uint) Subject                { return Subject("access_key:" + strconv.FormatUint(uint64(id), 10)) }
func Credential(id uint) Subject               { return Subject("credential:" + strconv.FormatUint(uint64(id), 10)) }
func Account(channel, identity string) Subject { return Subject("account:" + channel + ":" + identity) }

type Runtime struct {
	mu     sync.Mutex
	counts map[Subject]int64
}

func New() *Runtime { return &Runtime{counts: make(map[Subject]int64)} }

type Limit struct {
	Subject Subject
	Maximum int64
}

// Lease owns all the slots in an admission. Release is safe on every exit path.
type Lease struct {
	once     sync.Once
	runtime  *Runtime
	subjects []Subject
}

// TryAcquire checks and increments all scopes under one lock. A rejection has
// no side effects. Unlimited scopes are still counted for observation and for
// changing a limit while requests are running.
func (r *Runtime) TryAcquire(limits ...Limit) (*Lease, Subject) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, limit := range limits {
		if limit.Maximum > 0 && r.counts[limit.Subject] >= limit.Maximum {
			return nil, limit.Subject
		}
	}
	lease := &Lease{runtime: r}
	seen := make(map[Subject]bool, len(limits))
	for _, limit := range limits {
		if !seen[limit.Subject] {
			seen[limit.Subject] = true
			r.counts[limit.Subject]++
			lease.subjects = append(lease.subjects, limit.Subject)
		}
	}
	return lease, ""
}

func (l *Lease) Release() {
	if l == nil {
		return
	}
	l.once.Do(func() {
		l.runtime.mu.Lock()
		defer l.runtime.mu.Unlock()
		for _, subject := range l.subjects {
			l.runtime.counts[subject]--
			if l.runtime.counts[subject] == 0 {
				delete(l.runtime.counts, subject)
			}
		}
	})
}

// Observe returns one coherent sample, without exposing the mutable counter map.
func (r *Runtime) Observe(subjects []Subject) map[Subject]int64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[Subject]int64, len(subjects))
	for _, subject := range subjects {
		result[subject] = r.counts[subject]
	}
	return result
}
