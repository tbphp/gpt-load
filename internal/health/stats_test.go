package health

import (
	"sync"
	"testing"
	"time"
)

func statsBase() time.Time {
	return time.Date(2026, time.July, 22, 10, 0, 0, 0, time.UTC)
}

func TestStatsWindowMatchesBucketConfiguration(t *testing.T) {
	if StatsWindow != 5*time.Minute {
		t.Fatalf("StatsWindow = %s, want 5m", StatsWindow)
	}
	if StatsWindow != time.Duration(statsBucketCount)*statsBucketWidth {
		t.Fatalf(
			"StatsWindow = %s, bucket configuration = %s",
			StatsWindow,
			time.Duration(statsBucketCount)*statsBucketWidth,
		)
	}
}

func TestStatsStoreSnapshotUnknownKeyReturnsZero(t *testing.T) {
	store := NewStatsStore()
	now := statsBase()

	for _, keyID := range []uint{0, 1} {
		if got := store.Snapshot(keyID, now); got != (KeyStats{}) {
			t.Fatalf("Snapshot(%d) = %#v, want zero value", keyID, got)
		}
	}

	store.RecordFailure(0, FailureCategoryAmbiguous, 0, now)
	if got := store.Snapshot(0, now); got != (KeyStats{}) {
		t.Fatalf("Snapshot(0) after Record = %#v, want zero value", got)
	}
}

func TestStatsStoreRecordAggregatesRollingWindow(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	store.RecordSuccess(1, base)
	store.RecordFailure(1, FailureCategoryAmbiguous, 0, base.Add(-time.Minute))
	store.RecordSuccess(1, base.Add(-4*time.Minute))
	store.RecordFailure(1, FailureCategoryAmbiguous, 0, base.Add(-5*time.Minute))

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
		store.RecordFailure(1, FailureCategoryAmbiguous, 0, base)
		store.RecordSuccess(1, base.Add(5*time.Minute))

		got := store.Snapshot(1, base.Add(5*time.Minute))
		want := KeyStats{Success: 1}
		if got != want {
			t.Fatalf("Snapshot() = %#v, want %#v", got, want)
		}
	})

	t.Run("older event leaves newer slot and streak intact", func(t *testing.T) {
		store := NewStatsStore()
		store.RecordFailure(1, FailureCategoryAmbiguous, 0, base.Add(5*time.Minute))
		store.RecordSuccess(1, base)

		got := store.Snapshot(1, base.Add(5*time.Minute))
		want := KeyStats{Failure: 1, ConsecutiveFailure: 1, ConsecutiveProblem: 1}
		if got != want {
			t.Fatalf("Snapshot() = %#v, want %#v", got, want)
		}
	})
}

func TestStatsStoreSnapshotExcludesExpiredAndFutureBuckets(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	store.RecordFailure(1, FailureCategoryAmbiguous, 0, base.Add(-5*time.Minute))
	store.RecordSuccess(1, base.Add(time.Minute))

	got := store.Snapshot(1, base)
	want := KeyStats{ConsecutiveFailure: 0}
	if got != want {
		t.Fatalf("Snapshot() = %#v, want %#v", got, want)
	}
}

func TestStatsStoreConsecutiveFailureLifecycle(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	store.RecordFailure(1, FailureCategoryAmbiguous, 0, base.Add(-5*time.Minute))
	store.RecordFailure(1, FailureCategoryAmbiguous, 0, base.Add(-4*time.Minute))
	if got, want := store.Snapshot(1, base), (KeyStats{
		Failure: 1, ConsecutiveFailure: 2, ConsecutiveProblem: 2,
	}); got != want {
		t.Fatalf("after failures Snapshot() = %#v, want %#v", got, want)
	}

	store.RecordSuccess(1, base)
	if got, want := store.Snapshot(1, base), (KeyStats{Success: 1, Failure: 1}); got != want {
		t.Fatalf("after success Snapshot() = %#v, want %#v", got, want)
	}
}

func TestStatsStoreProblemContextLifecycle(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	store.RecordProblem(7, FailureCategoryRateLimited, 429, base)
	if got, want := store.Snapshot(7, base), (KeyStats{
		ConsecutiveProblem:  1,
		LastFailureCategory: FailureCategoryRateLimited,
		LastStatusCode:      429,
	}); got != want {
		t.Fatalf("after rate limit Snapshot() = %#v, want %#v", got, want)
	}

	store.RecordFailure(7, FailureCategoryInvalidKey, 401, base.Add(time.Second))
	if got, want := store.Snapshot(7, base), (KeyStats{
		Failure:             1,
		ConsecutiveFailure:  1,
		ConsecutiveProblem:  2,
		LastFailureCategory: FailureCategoryInvalidKey,
		LastStatusCode:      401,
	}); got != want {
		t.Fatalf("after attributable failure Snapshot() = %#v, want %#v", got, want)
	}

	store.RecordSuccess(7, base.Add(2*time.Second))
	if got, want := store.Snapshot(7, base), (KeyStats{
		Success: 1,
		Failure: 1,
	}); got != want {
		t.Fatalf("after success Snapshot() = %#v, want %#v", got, want)
	}
}

func TestStatsStoreOlderEventDoesNotReplaceLatestFailureContext(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	store.RecordFailure(7, FailureCategoryInvalidKey, 401, base)
	store.RecordProblem(7, FailureCategoryRateLimited, 429, base.Add(-time.Minute))

	got := store.Snapshot(7, base)
	want := KeyStats{
		Failure:             1,
		ConsecutiveFailure:  1,
		ConsecutiveProblem:  1,
		LastFailureCategory: FailureCategoryInvalidKey,
		LastStatusCode:      401,
	}
	if got != want {
		t.Fatalf("Snapshot() = %#v, want %#v", got, want)
	}
}

func TestStatsStoreSnapshotReturnsValueCopy(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()
	store.RecordSuccess(1, base)

	snapshot := store.Snapshot(1, base)
	snapshot.Success = 99
	if got, want := store.Snapshot(1, base), (KeyStats{Success: 1}); got != want {
		t.Fatalf("Snapshot() after mutating prior result = %#v, want %#v", got, want)
	}
}

func TestStatsStoreResetClearsWindowAndStreak(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	store.RecordSuccess(7, base)
	store.RecordFailure(7, FailureCategoryAmbiguous, 0, base)
	store.Reset(7)

	if got := store.Snapshot(7, base); got != (KeyStats{}) {
		t.Fatalf("Snapshot() after Reset = %#v, want zero", got)
	}
}

func TestStatsStoreClearProblemStatePreservesRollingBuckets(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()
	store.RecordSuccess(7, base.Add(-2*time.Minute))
	store.RecordFailure(7, FailureCategoryRateLimited, 429, base.Add(-time.Minute))
	store.RecordFailure(7, FailureCategoryInvalidKey, 401, base)

	store.ClearProblemState(7)

	got := store.Snapshot(7, base)
	want := KeyStats{Success: 1, Failure: 2}
	if got != want {
		t.Fatalf("Snapshot() after ClearProblemState = %#v, want %#v", got, want)
	}
	store.ClearProblemState(0)
	store.ClearProblemState(99)
	if got := store.Snapshot(7, base); got != want {
		t.Fatalf("Snapshot() after no-op clears = %#v, want %#v", got, want)
	}
}

func TestStatsStoreResetUnknownAndZeroKeyIsNoop(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	store.Reset(0)
	store.Reset(7)
	if got := store.Snapshot(7, base); got != (KeyStats{}) {
		t.Fatalf("Snapshot() after Reset unknown key = %#v, want zero", got)
	}

	store.RecordFailure(7, FailureCategoryAmbiguous, 0, base)
	store.Reset(0)
	if got, want := store.Snapshot(7, base), (KeyStats{
		Failure: 1, ConsecutiveFailure: 1, ConsecutiveProblem: 1,
	}); got != want {
		t.Fatalf("Snapshot() after Reset(0) = %#v, want %#v", got, want)
	}
}

func TestStatsStoreConcurrentRecordResetAndSnapshot(t *testing.T) {
	store := NewStatsStore()
	base := statsBase()

	var group sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		group.Add(3)
		go func(worker int) {
			defer group.Done()
			for index := 0; index < 100; index++ {
				at := base.Add(time.Duration(worker+index%5) * time.Minute)
				if index%2 == 0 {
					store.RecordSuccess(7, at)
				} else {
					store.RecordFailure(7, FailureCategoryAmbiguous, 0, at)
				}
			}
		}(worker)
		go func() {
			defer group.Done()
			for index := 0; index < 100; index++ {
				store.Reset(7)
			}
		}()
		go func(worker int) {
			defer group.Done()
			for index := 0; index < 100; index++ {
				_ = store.Snapshot(7, base.Add(time.Duration(worker+index%5)*time.Minute))
			}
		}(worker)
	}
	group.Wait()
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
				keyID := uint(worker%3 + 1)
				if index%2 == 0 {
					store.RecordSuccess(keyID, at)
				} else {
					store.RecordFailure(keyID, FailureCategoryAmbiguous, 0, at)
				}
				_ = store.Snapshot(uint(worker%3+1), at)
			}
		}(worker)
	}
	group.Wait()
}
