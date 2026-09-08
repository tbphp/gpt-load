package scheduler

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"gpt-load/internal/state"
)

func benchmarkSchedulingRegistry(b *testing.B, small, large int) *state.CredentialRegistry {
	b.Helper()
	r := state.NewCredentialRegistry()
	entries := make([]state.CredentialEntry, 0, small+large)
	for i := range small + large {
		group := uint(1)
		if i >= small {
			group = 2
		}
		entries = append(entries, state.CredentialEntry{ID: uint(i + 1), GroupID: group, Version: 1, IdentityGeneration: 1,
			Status: state.CredentialStatusActive, Fingerprint: "benchmark", EncryptedValue: "cipher"})
	}
	if err := r.ReplaceCredentials(entries); err != nil {
		b.Fatal(err)
	}
	return r
}

func BenchmarkGlobalScheduling(b *testing.B) {
	for _, size := range []int{100, 1000, 10000} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			r := benchmarkSchedulingRegistry(b, size, 0)
			snapshot := schedulerSnapshot()
			query := fairnessQuery(0)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := New(snapshot, r, query).Next(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkGlobalSchedulingMixedConcurrent(b *testing.B) {
	r := benchmarkSchedulingRegistry(b, 100, 10000)
	snapshot := schedulerSnapshot()
	small, large := fairnessQuery(0), fairnessQuery(0)
	small.AccessKey.Filters.Groups = map[uint]struct{}{1: {}}
	large.AccessKey.Filters.Groups = map[uint]struct{}{2: {}}
	var count, smallCount, smallTotal, smallMax atomic.Uint64
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			isSmall := count.Add(1)%10 != 0
			query := large
			if isSmall {
				query = small
			}
			start := time.Now()
			_, err := New(snapshot, r, query).Next()
			elapsed := uint64(time.Since(start).Nanoseconds())
			if err != nil {
				b.Error(err)
				return
			}
			if isSmall {
				smallCount.Add(1)
				smallTotal.Add(elapsed)
				for previous := smallMax.Load(); elapsed > previous; previous = smallMax.Load() {
					if smallMax.CompareAndSwap(previous, elapsed) {
						break
					}
				}
			}
		}
	})
	if n := smallCount.Load(); n > 0 {
		b.ReportMetric(float64(smallTotal.Load())/float64(n), "small-ns/op")
	}
	b.ReportMetric(float64(smallMax.Load()), "small-max-ns")
}
