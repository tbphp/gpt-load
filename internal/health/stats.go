package health

import (
	"sync"
	"time"
)

const (
	statsBucketCount = 5
	statsBucketWidth = time.Minute
	StatsWindow      = time.Duration(statsBucketCount) * statsBucketWidth
)

type KeyStats struct {
	Success             uint64
	Failure             uint64
	ConsecutiveFailure  uint64
	ConsecutiveProblem  uint64
	LastFailureCategory FailureCategory
	LastStatusCode      int
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
	buckets             [statsBucketCount]statsBucket
	consecutiveFailure  uint64
	consecutiveProblem  uint64
	lastEventAt         time.Time
	lastEventRecorded   bool
	lastFailureCategory FailureCategory
	lastStatusCode      int
}

type statsEvent uint8

const (
	statsEventSuccess statsEvent = iota
	statsEventFailure
	statsEventProblem
)

func NewStatsStore() *StatsStore {
	return &StatsStore{windows: make(map[uint]*keyStatsWindow)}
}

func (store *StatsStore) RecordSuccess(keyID uint, at time.Time) {
	store.record(keyID, statsEventSuccess, FailureCategoryOK, 0, at)
}

func (store *StatsStore) RecordFailure(
	keyID uint,
	category FailureCategory,
	statusCode int,
	at time.Time,
) {
	store.record(keyID, statsEventFailure, category, statusCode, at)
}

func (store *StatsStore) RecordProblem(
	keyID uint,
	category FailureCategory,
	statusCode int,
	at time.Time,
) {
	store.record(keyID, statsEventProblem, category, statusCode, at)
}

func (store *StatsStore) record(
	keyID uint,
	event statsEvent,
	category FailureCategory,
	statusCode int,
	at time.Time,
) {
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

	if event != statsEventProblem {
		bucket := &window.buckets[slot]
		if !bucket.valid || minute > bucket.minute {
			*bucket = statsBucket{minute: minute, valid: true}
		} else if minute < bucket.minute {
			return
		}

		if event == statsEventSuccess {
			bucket.success++
		} else {
			bucket.failure++
		}
	}

	if window.lastEventRecorded && at.Before(window.lastEventAt) {
		return
	}
	window.lastEventAt = at
	window.lastEventRecorded = true
	if event == statsEventSuccess {
		window.consecutiveFailure = 0
		window.consecutiveProblem = 0
		window.lastFailureCategory = FailureCategoryAmbiguous
		window.lastStatusCode = 0
		return
	}
	if event == statsEventFailure {
		window.consecutiveFailure++
	}
	window.consecutiveProblem++
	window.lastFailureCategory = category
	window.lastStatusCode = statusCode
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

	stats := KeyStats{
		ConsecutiveFailure:  window.consecutiveFailure,
		ConsecutiveProblem:  window.consecutiveProblem,
		LastFailureCategory: window.lastFailureCategory,
		LastStatusCode:      window.lastStatusCode,
	}
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
