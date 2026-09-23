// Package rpm records request rates without depending on persistence or routing.
package rpm

import (
	"crypto/rand"
	"sort"
	"sync"
	"time"
)

type Kind string

const (
	AccessKey  Kind = "access_key"
	Credential Kind = "credential"
)

type Counter struct {
	Requests int64 `json:"requests"`
	Rejected int64 `json:"rejected"`
}

type Snapshot struct {
	Kind      Kind
	KeyID     uint
	MinuteMS  int64
	SessionID string
	Counter
	Peak    int64
	Version uint64
}

type key struct {
	kind Kind
	id   uint
}
type bucketKey struct {
	key
	minuteMS int64
}
type bucket struct {
	Snapshot
	persisted uint64
}
type window struct {
	seconds     [60]Counter
	current     Counter
	second      int64
	lastRequest int64
}

// Store 只在内存中聚合，通知回调必须非阻塞；快照写库由外部工作线程负责。
type Store struct {
	mu        sync.Mutex
	windows   map[key]*window
	buckets   map[bucketKey]*bucket
	sessionID string
	notify    func()
}

// 内存积压有界，数据库长期不可用时允许丢弃新增观测，不反压业务请求。
const maxBuckets = 65_536

func NewStore() *Store {
	return &Store{windows: make(map[key]*window), buckets: make(map[bucketKey]*bucket), sessionID: rand.Text()}
}

func (s *Store) Record(kind Kind, id uint, rejected bool, at time.Time) {
	if s == nil || id == 0 || (kind != AccessKey && kind != Credential) || at.Unix() < 0 {
		return
	}
	s.mu.Lock()
	k := key{kind, id}
	second := at.Unix()
	w := s.windows[k]
	if w == nil {
		if len(s.windows) >= maxBuckets {
			s.mu.Unlock()
			return
		}
		w = &window{second: second, lastRequest: second}
		s.windows[k] = w
	}
	s.advance(k, w, second)
	// 同一秒内并发记录可能乱序，不能让窗口倒退。
	second = max(second, w.second)
	w.lastRequest = second
	slot := &w.seconds[second%60]
	slot.Requests++
	w.current.Requests++
	if rejected && kind == AccessKey {
		slot.Rejected++
		w.current.Rejected++
	}
	if b := s.minute(k, second); b != nil {
		b.Requests++
		if rejected && kind == AccessKey {
			b.Rejected++
		}
		b.Peak = max(b.Peak, w.current.Requests)
		b.Version++
	}
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify()
	}
}

func (s *Store) minute(k key, second int64) *bucket {
	id := bucketKey{k, second / 60 * 60_000}
	b := s.buckets[id]
	if b == nil && len(s.buckets) < maxBuckets {
		b = &bucket{Snapshot: Snapshot{Kind: k.kind, KeyID: k.id, MinuteMS: id.minuteMS, SessionID: s.sessionID, Version: 1}}
		s.buckets[id] = b
	}
	return b
}

func (s *Store) advance(k key, w *window, second int64) {
	if second <= w.second {
		return
	}
	end := min(second, w.second+60)
	for next := w.second + 1; next <= end; next++ {
		slot := &w.seconds[next%60]
		w.current.Requests -= slot.Requests
		w.current.Rejected -= slot.Rejected
		*slot = Counter{}
		// 即使没有新请求，跨分钟时仍需保存尚未过期的滑窗峰值。
		if next%60 == 0 {
			if b := s.minute(k, next); b != nil && b.Peak < w.current.Requests {
				b.Peak = w.current.Requests
				b.Version++
			}
		}
	}
	w.second = second
}

func (s *Store) Current(kind Kind, id uint, at time.Time) Counter {
	if s == nil {
		return Counter{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key{kind, id}
	w := s.windows[k]
	if w == nil {
		return Counter{}
	}
	s.advance(k, w, at.Unix())
	return w.current
}

func (s *Store) advanceAll(now time.Time) {
	for k, w := range s.windows {
		s.advance(k, w, now.Unix())
		if now.Unix()-w.lastRequest >= 60 {
			delete(s.windows, k)
		}
	}
	for k, b := range s.buckets {
		if b.persisted == b.Version && k.minuteMS < now.Add(-2*time.Minute).UnixMilli() {
			delete(s.buckets, k)
		}
	}
}

// Snapshots 返回绝对计数副本，SessionID 防止重启同一分钟覆盖上一个进程的数据。
func (s *Store) Snapshots(now time.Time, dirtyOnly bool, limit int) []Snapshot {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	s.advanceAll(now)
	rows := make([]Snapshot, 0, len(s.buckets))
	for _, b := range s.buckets {
		if !dirtyOnly || b.persisted != b.Version {
			rows = append(rows, b.Snapshot)
		}
	}
	s.mu.Unlock()
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].MinuteMS != rows[j].MinuteMS {
			return rows[i].MinuteMS < rows[j].MinuteMS
		}
		if rows[i].Kind != rows[j].Kind {
			return rows[i].Kind < rows[j].Kind
		}
		return rows[i].KeyID < rows[j].KeyID
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}
	return rows
}

func (s *Store) Ack(snapshots []Snapshot) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, row := range snapshots {
		if row.SessionID != s.sessionID {
			continue
		}
		if b := s.buckets[bucketKey{key{row.Kind, row.KeyID}, row.MinuteMS}]; b != nil {
			b.persisted = max(b.persisted, min(row.Version, b.Version))
		}
	}
}

func (s *Store) SetDirtyNotifier(notify func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notify = notify
}

func (s *Store) Pending(now time.Time) bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.advanceAll(now)
	// 空闲时继续推进存活窗口，才能保存跨分钟但没有新请求的 60 秒峰值。
	if len(s.windows) != 0 {
		return true
	}
	for _, b := range s.buckets {
		if b.persisted != b.Version {
			return true
		}
	}
	return false
}

func (s *Store) DiscardBefore(cutoffMS int64) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.buckets {
		if k.minuteMS < cutoffMS {
			delete(s.buckets, k)
		}
	}
}
