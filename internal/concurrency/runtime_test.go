package concurrency

import (
	"sync"
	"testing"
)

func TestAtomicAdmissionUnderContention(t *testing.T) {
	runtime := New()
	const contenders, maximum = 200, 7
	start := make(chan struct{})
	leases := make(chan *Lease, contenders)
	var wait sync.WaitGroup
	for range contenders {
		wait.Go(func() {
			<-start
			lease, _ := runtime.TryAcquire(Limit{Global, 30}, Limit{Group(1), 15}, Limit{Credential(1), maximum})
			leases <- lease
		})
	}
	close(start)
	wait.Wait()
	close(leases)
	counts := runtime.Observe([]Subject{Global, Group(1), Credential(1)})
	for subject, count := range counts {
		if count != maximum {
			t.Fatalf("%s: got %d, want %d", subject, count, maximum)
		}
	}
	for lease := range leases {
		// Every cleanup path may release, including concurrent cancellation.
		for range 3 {
			wait.Go(func() { lease.Release() })
		}
	}
	wait.Wait()
	for subject, count := range runtime.Observe([]Subject{Global, Group(1), Credential(1)}) {
		if count != 0 {
			t.Fatalf("leaked %s: %d", subject, count)
		}
	}
}

func TestUnlimitedStillCountsAndLimitCanChange(t *testing.T) {
	runtime := New()
	var leases []*Lease
	for range 3 {
		lease, _ := runtime.TryAcquire(Limit{AccessKey(1), 0})
		leases = append(leases, lease)
	}
	if lease, blocked := runtime.TryAcquire(Limit{Global, 0}, Limit{AccessKey(1), 2}); lease != nil || blocked != AccessKey(1) {
		t.Fatal("lowered limit admitted a new request")
	}
	if got := runtime.Observe([]Subject{Global})[Global]; got != 0 {
		t.Fatalf("partial admission: %d", got)
	}
	leases[0].Release()
	leases[1].Release()
	lease, blocked := runtime.TryAcquire(Limit{AccessKey(1), 2}, Limit{AccessKey(1), 2})
	if blocked != "" {
		t.Fatal(blocked)
	}
	if got := runtime.Observe([]Subject{AccessKey(1)})[AccessKey(1)]; got != 2 {
		t.Fatalf("duplicate scope counted twice: %d", got)
	}
	lease.Release()
	leases[2].Release()
}
