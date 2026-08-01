package health

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestMutationCoordinatorSerializesKeysInSameStripe(t *testing.T) {
	coordinator := NewMutationCoordinator()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan struct{})
	go func() {
		coordinator.Do(1, func() {
			close(firstEntered)
			<-releaseFirst
		})
		close(firstDone)
	}()
	awaitMutationSignal(t, firstEntered)

	firstStripe := int(uint(1) % uint(len(coordinator.stripes)))
	secondStripe := int(uint(65) % uint(len(coordinator.stripes)))
	if firstStripe != secondStripe {
		t.Fatalf("keys 1 and 65 mapped to stripes %d and %d, want the same stripe", firstStripe, secondStripe)
	}
	if coordinator.stripes[firstStripe].TryLock() {
		coordinator.stripes[firstStripe].Unlock()
		t.Fatal("same stripe lock was available while key 1 callback was blocked")
	}

	secondEntered := make(chan struct{})
	go func() {
		coordinator.Do(65, func() { close(secondEntered) })
	}()

	close(releaseFirst)
	awaitMutationSignal(t, firstDone)
	awaitMutationSignal(t, secondEntered)
}

func TestMutationCoordinatorAllowsKeysInDifferentStripesToOverlap(t *testing.T) {
	coordinator := NewMutationCoordinator()
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	go coordinator.Do(1, func() {
		close(firstEntered)
		<-releaseFirst
	})
	awaitMutationSignal(t, firstEntered)

	secondEntered := make(chan struct{})
	go coordinator.Do(2, func() { close(secondEntered) })
	awaitMutationSignal(t, secondEntered)
	close(releaseFirst)
}

func TestMutationCoordinatorUnlocksAfterCallbackPanic(t *testing.T) {
	coordinator := NewMutationCoordinator()
	func() {
		defer func() {
			if recovered := recover(); recovered != "boom" {
				t.Fatalf("panic = %#v, want boom", recovered)
			}
		}()
		coordinator.Do(1, func() { panic("boom") })
	}()

	called := false
	coordinator.Do(65, func() { called = true })
	if !called {
		t.Fatal("same stripe remained locked after callback panic")
	}
}

func TestMutationCoordinatorNilCallbackIsNoop(t *testing.T) {
	coordinator := NewMutationCoordinator()
	coordinator.Do(1, nil)

	var calls atomic.Int64
	coordinator.Do(65, func() { calls.Add(1) })
	if got := calls.Load(); got != 1 {
		t.Fatalf("subsequent callback calls = %d, want 1", got)
	}
}

func TestMutationCoordinatorDoManySerializesEveryOverlappingStripe(t *testing.T) {
	coordinator := NewMutationCoordinator()
	holderEntered := make(chan struct{})
	releaseHolder := make(chan struct{})
	go coordinator.Do(1, func() {
		close(holderEntered)
		<-releaseHolder
	})
	awaitMutationSignal(t, holderEntered)

	manyEntered := make(chan struct{})
	releaseMany := make(chan struct{})
	manyDone := make(chan struct{})
	go func() {
		coordinator.DoMany([]uint{65, 2, 1}, func() {
			close(manyEntered)
			<-releaseMany
		})
		close(manyDone)
	}()
	select {
	case <-manyEntered:
		t.Fatal("DoMany entered while overlapping stripe was held")
	default:
	}
	close(releaseHolder)
	awaitMutationSignal(t, manyEntered)

	overlapEntered := make(chan struct{})
	go coordinator.Do(2, func() { close(overlapEntered) })
	select {
	case <-overlapEntered:
		t.Fatal("overlapping key entered during DoMany callback")
	default:
	}
	close(releaseMany)
	awaitMutationSignal(t, manyDone)
	awaitMutationSignal(t, overlapEntered)
}

func awaitMutationSignal(t *testing.T, signal <-chan struct{}) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for mutation barrier")
	}
}
