package health

import (
	"sync"
	"time"
)

const (
	statsBucketCount = 5
	statsBucketWidth = time.Minute
)

type KeyStats struct {
	Success            uint64
	Failure            uint64
	ConsecutiveFailure uint64
}

type StatsStore struct {
	mu      sync.Mutex
	windows map[uint]*keyStatsWindow
}

type statsBucket struct {
	minute  int64
	valid   bool
	success uint64
	failure uint64
}

type keyStatsWindow struct {
	buckets            [statsBucketCount]statsBucket
	consecutiveFailure uint64
}

func NewStatsStore() *StatsStore {
	return &StatsStore{windows: make(map[uint]*keyStatsWindow)}
}

func (store *StatsStore) Record(keyID uint, ok bool, at time.Time) {
	if keyID == 0 {
		return
	}

	minute := at.UnixNano() / int64(statsBucketWidth)
	slot := statsBucketSlot(minute)

	store.mu.Lock()
	defer store.mu.Unlock()

	window := store.windows[keyID]
	if window == nil {
		window = &keyStatsWindow{}
		store.windows[keyID] = window
	}

	bucket := &window.buckets[slot]
	if !bucket.valid || minute > bucket.minute {
		*bucket = statsBucket{minute: minute, valid: true}
	} else if minute < bucket.minute {
		return
	}

	if ok {
		bucket.success++
		window.consecutiveFailure = 0
		return
	}

	bucket.failure++
	window.consecutiveFailure++
}

func (store *StatsStore) Reset(keyID uint) {
	if keyID == 0 {
		return
	}

	store.mu.Lock()
	delete(store.windows, keyID)
	store.mu.Unlock()
}

func (store *StatsStore) Snapshot(keyID uint, now time.Time) KeyStats {
	if keyID == 0 {
		return KeyStats{}
	}

	currentMinute := now.UnixNano() / int64(statsBucketWidth)
	store.mu.Lock()
	defer store.mu.Unlock()

	window := store.windows[keyID]
	if window == nil {
		return KeyStats{}
	}

	stats := KeyStats{ConsecutiveFailure: window.consecutiveFailure}
	firstMinute := currentMinute - (statsBucketCount - 1)
	for _, bucket := range window.buckets {
		if !bucket.valid || bucket.minute < firstMinute || bucket.minute > currentMinute {
			continue
		}
		stats.Success += bucket.success
		stats.Failure += bucket.failure
	}
	return stats
}

func statsBucketSlot(minute int64) int {
	slot := minute % statsBucketCount
	if slot < 0 {
		slot += statsBucketCount
	}
	return int(slot)
}
