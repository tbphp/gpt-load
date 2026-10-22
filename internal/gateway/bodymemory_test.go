package gateway

import (
	"bytes"
	"compress/gzip"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"gpt-load/internal/platform/contentcoding"
)

func gzipRequest(t *testing.T, decodedBytes int) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	writer := gzip.NewWriter(&buf)
	if _, err := writer.Write(bytes.Repeat([]byte("A"), decodedBytes)); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(buf.Bytes()))
	request.ContentLength = int64(buf.Len())
	return request
}

func TestNewBodyMemoryBudgetDisabledForNonPositive(t *testing.T) {
	if newBodyMemoryBudget(0) != nil {
		t.Fatal("newBodyMemoryBudget(0) should be nil (unlimited)")
	}
	if newBodyMemoryBudget(-1) != nil {
		t.Fatal("newBodyMemoryBudget(-1) should be nil (unlimited)")
	}
	if newBodyMemoryBudget(1) == nil {
		t.Fatal("newBodyMemoryBudget(1) should be enabled")
	}
}

func TestBodyMemoryBudgetNilIsUnlimited(t *testing.T) {
	var budget *bodyMemoryBudget
	granted, ok := budget.reserve(123)
	if !ok || granted != 123 {
		t.Fatalf("nil.reserve(123) = %d, %v; want 123, true", granted, ok)
	}
	budget.adjust(-5) // must not panic
}

func TestBodyMemoryBudgetGrantsUpToRemaining(t *testing.T) {
	budget := newBodyMemoryBudget(100)

	granted, ok := budget.reserve(60)
	if !ok || granted != 60 {
		t.Fatalf("reserve(60) = %d, %v; want 60, true", granted, ok)
	}
	// Only 40 left: a larger request is granted the remainder, not refused.
	granted, ok = budget.reserve(60)
	if !ok || granted != 40 {
		t.Fatalf("reserve(60) = %d, %v; want 40, true", granted, ok)
	}
	// Exhausted.
	granted, ok = budget.reserve(1)
	if ok || granted != 0 {
		t.Fatalf("reserve(1) = %d, %v; want 0, false", granted, ok)
	}
}

func TestBodyMemoryBudgetAdjustReleasesAndShrinks(t *testing.T) {
	budget := newBodyMemoryBudget(100)
	if _, ok := budget.reserve(100); !ok {
		t.Fatal("reserve(100) should succeed")
	}
	if _, ok := budget.reserve(1); ok {
		t.Fatal("reserve(1) should fail when full")
	}
	// Shrink the held amount from 100 to 10 (release 90).
	budget.adjust(10 - 100)
	granted, ok := budget.reserve(80)
	if !ok || granted != 80 {
		t.Fatalf("reserve(80) after shrink = %d, %v; want 80, true", granted, ok)
	}
	// Over-release must clamp at zero, never go negative.
	budget.adjust(-1 << 30)
	if budget.used != 0 {
		t.Fatalf("used = %d after over-release; want 0", budget.used)
	}
}

func TestBodyMemoryBudgetConcurrentNeverExceedsMax(t *testing.T) {
	const max = 64 << 20
	budget := newBodyMemoryBudget(max)
	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				granted, ok := budget.reserve(4 << 20)
				if !ok {
					continue
				}
				budget.mu.Lock()
				if budget.used > max {
					budget.mu.Unlock()
					t.Errorf("used %d exceeded max %d", budget.used, max)
					return
				}
				budget.mu.Unlock()
				budget.adjust(-granted)
			}
		}()
	}
	wg.Wait()
	if budget.used != 0 {
		t.Fatalf("used = %d after all releases; want 0", budget.used)
	}
}

func TestDefaultBodyMemoryBudgetLeavesRoomForMaxRequest(t *testing.T) {
	budget := defaultBodyMemoryBudgetBytes()
	if budget < maxRequestBodyBytes {
		t.Fatalf("default budget = %d; want >= %d (one max request)", budget, maxRequestBodyBytes)
	}
}

func TestParseCgroupMemoryLimit(t *testing.T) {
	cases := []struct {
		in     string
		want   int64
		wantOK bool
	}{
		{"max", 0, false},
		{"", 0, false},
		{"0", 0, false},
		{"536870912", 536870912, true},
		{"9223372036854771712", 0, false}, // cgroup v1 "unlimited" sentinel
	}
	for _, tc := range cases {
		got, ok := parseCgroupMemoryLimit(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("parseCgroupMemoryLimit(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

// TestAcquireDecodedRequestBodyBoundsConcurrentInFlight is the regression guard:
// with a small shared budget, concurrently held request bodies are admitted only
// up to the budget and the rest are refused as server-busy, so aggregate decoded
// memory cannot grow without bound.
func TestAcquireDecodedRequestBodyBoundsConcurrentInFlight(t *testing.T) {
	const (
		budgetMax = 20 << 20
		bodySize  = 8 << 20
	)
	handler := &Handler{bodyMemory: newBodyMemoryBudget(budgetMax)}

	body1, release1, err := handler.acquireDecodedRequestBody(gzipRequest(t, bodySize), contentcoding.Gzip)
	if err != nil || release1 == nil || len(body1) != bodySize {
		t.Fatalf("first acquire = len %d, %v; want %d, nil", len(body1), err, bodySize)
	}
	_, release2, err := handler.acquireDecodedRequestBody(gzipRequest(t, bodySize), contentcoding.Gzip)
	if err != nil || release2 == nil {
		t.Fatalf("second acquire err = %v; want nil", err)
	}

	// 16MB held, 4MB left: an 8MB body cannot fit and must be refused as busy,
	// never partially decoded and never pushing aggregate memory past the budget.
	_, release3, err := handler.acquireDecodedRequestBody(gzipRequest(t, bodySize), contentcoding.Gzip)
	if !errors.Is(err, errServerBusy) || release3 != nil {
		t.Fatalf("third acquire = %v, release!=nil=%t; want errServerBusy, nil", err, release3 != nil)
	}
	if handler.bodyMemory.used > budgetMax {
		t.Fatalf("used %d exceeded budget %d", handler.bodyMemory.used, budgetMax)
	}

	// Releasing the first frees room for a later request.
	release1()
	body4, release4, err := handler.acquireDecodedRequestBody(gzipRequest(t, bodySize), contentcoding.Gzip)
	if err != nil || release4 == nil || len(body4) != bodySize {
		t.Fatalf("acquire after release = len %d, %v; want %d, nil", len(body4), err, bodySize)
	}

	release2()
	release4()
	if handler.bodyMemory.used != 0 {
		t.Fatalf("used = %d after releasing all; want 0", handler.bodyMemory.used)
	}
}

// TestAcquireDecodedRequestBodyUnlimitedAdmitsAll documents that without the
// budget (the pre-fix default) in-flight bodies are never refused.
func TestAcquireDecodedRequestBodyUnlimitedAdmitsAll(t *testing.T) {
	handler := &Handler{bodyMemory: nil}
	for i := 0; i < 8; i++ {
		body, release, err := handler.acquireDecodedRequestBody(gzipRequest(t, 4<<20), contentcoding.Gzip)
		if err != nil || release == nil || len(body) != 4<<20 {
			t.Fatalf("acquire %d = len %d, %v; want admitted", i, len(body), err)
		}
	}
}
