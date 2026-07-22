package health

import (
	"sync"
	"testing"
	"time"
)

func statsBase() time.Time {
	return time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
}

func TestStatsStoreSnapshotUnknownKeyReturnsZero(t *testing.T) {
	store := NewStatsStore()
	now := statsBase()

	for _, keyID := range []uint{0, 1} {
		if got := store.Snapshot(keyID, now); got != (KeyStats{}) {
			t.Fatalf("Snapshot(%d) = %#v, want zero value", keyID, got)
		}
	}

	store.Record(0, false, now)
	if got := store.Snapshot(0, now); got != (KeyStats{}) {
		t.Fatalf("Snapshot(0) after Record = %#v, want zero value", got)
	}
}

func TestStatsStoreRecordAggregatesRollingWindow(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	store.Record(1, true, base)
	store.Record(1, false, base.Add(-time.Minute))
	store.Record(1, true, base.Add(-4*time.Minute))
	store.Record(1, false, base.Add(-5*time.Minute))

	got := store.Snapshot(1, base)
	want := KeyStats{Success: 2, Failure: 1}
	if got != want {
		t.Fatalf("Snapshot() = %#v, want %#v", got, want)
	}
}

func TestStatsStoreRecordDiscardsOlderSlotCollision(t *testing.T) {
	base := statsBase()

	t.Run("newer minute clears reused slot", func(t *testing.T) {
		store := NewStatsStore()
		store.Record(1, false, base)
		store.Record(1, true, base.Add(5*time.Minute))

		got := store.Snapshot(1, base.Add(5*time.Minute))
		want := KeyStats{Success: 1}
		if got != want {
			t.Fatalf("Snapshot() = %#v, want %#v", got, want)
		}
	})

	t.Run("older event leaves newer slot and streak intact", func(t *testing.T) {
		store := NewStatsStore()
		store.Record(1, false, base.Add(5*time.Minute))
		store.Record(1, true, base)

		got := store.Snapshot(1, base.Add(5*time.Minute))
		want := KeyStats{Failure: 1, ConsecutiveFailure: 1}
		if got != want {
			t.Fatalf("Snapshot() = %#v, want %#v", got, want)
		}
	})
}

func TestStatsStoreSnapshotExcludesExpiredAndFutureBuckets(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	store.Record(1, false, base.Add(-5*time.Minute))
	store.Record(1, true, base.Add(time.Minute))

	got := store.Snapshot(1, base)
	want := KeyStats{ConsecutiveFailure: 0}
	if got != want {
		t.Fatalf("Snapshot() = %#v, want %#v", got, want)
	}
}

func TestStatsStoreConsecutiveFailureLifecycle(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	store.Record(1, false, base.Add(-5*time.Minute))
	store.Record(1, false, base.Add(-4*time.Minute))
	if got, want := store.Snapshot(1, base), (KeyStats{Failure: 1, ConsecutiveFailure: 2}); got != want {
		t.Fatalf("after failures Snapshot() = %#v, want %#v", got, want)
	}

	store.Record(1, true, base)
	if got, want := store.Snapshot(1, base), (KeyStats{Success: 1, Failure: 1}); got != want {
		t.Fatalf("after success Snapshot() = %#v, want %#v", got, want)
	}
}

func TestStatsStoreSnapshotReturnsValueCopy(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()
	store.Record(1, true, base)

	snapshot := store.Snapshot(1, base)
	snapshot.Success = 99
	if got, want := store.Snapshot(1, base), (KeyStats{Success: 1}); got != want {
		t.Fatalf("Snapshot() after mutating prior result = %#v, want %#v", got, want)
	}
}

func TestStatsStoreConcurrentAccess(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(1)
		go func(worker int) {
			defer group.Done()
			for index := 0; index < 100; index++ {
				at := base.Add(time.Duration(index%5) * time.Minute)
				store.Record(uint(worker%3+1), index%2 == 0, at)
				_ = store.Snapshot(uint(worker%3+1), at)
			}
		}(worker)
	}
	group.Wait()
}
