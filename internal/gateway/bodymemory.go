package gateway

import (
	"os"
	"strconv"
	"strings"
	"sync"
)

// bodyMemoryBudget bounds the total memory held by decoded data-plane request
// bodies that are in flight at the same time. Each request is already capped at
// maxRequestBodyBytes, but in-flight concurrency is unbounded by default, so
// enough simultaneous requests can exhaust process memory. The budget caps the
// aggregate instead of changing per-request or concurrency defaults.
type bodyMemoryBudget struct {
	max  int64
	mu   sync.Mutex
	used int64
}

// newBodyMemoryBudget returns a budget capped at max bytes, or nil (unlimited)
// when max is non-positive.
func newBodyMemoryBudget(max int64) *bodyMemoryBudget {
	if max <= 0 {
		return nil
	}
	return &bodyMemoryBudget{max: max}
}

// reserve claims up to want bytes of the shared budget. It grants the smaller of
// want and the remaining budget, returning ok=false only when nothing is left. A
// nil budget is unlimited and grants want in full.
func (b *bodyMemoryBudget) reserve(want int64) (granted int64, ok bool) {
	if b == nil {
		return want, true
	}
	if want < 0 {
		want = 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.max - b.used
	if remaining <= 0 {
		return 0, false
	}
	granted = want
	if granted > remaining {
		granted = remaining
	}
	b.used += granted
	return granted, true
}

// adjust changes the amount currently charged by delta; pass a negative delta to
// shrink a reservation to its actual size or to release it. A nil budget is a
// no-op.
func (b *bodyMemoryBudget) adjust(delta int64) {
	if b == nil || delta == 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used += delta
	if b.used < 0 {
		b.used = 0
	}
}

const (
	// bodyMemoryBudgetFraction reserves 1/N of the detected memory ceiling for
	// in-flight decoded request bodies.
	bodyMemoryBudgetFraction = 4
	// fallbackBodyMemoryBudgetBytes applies when the memory ceiling cannot be
	// detected, for example on hosts without cgroup or /proc/meminfo.
	fallbackBodyMemoryBudgetBytes = int64(512 << 20)
)

// defaultBodyMemoryBudgetBytes sizes the in-flight decoded request-body budget
// from the memory available to the process, always leaving room for at least one
// maximum-size request.
func defaultBodyMemoryBudgetBytes() int64 {
	available := detectMemoryLimitBytes()
	if available <= 0 {
		return fallbackBodyMemoryBudgetBytes
	}
	budget := available / bodyMemoryBudgetFraction
	if budget < maxRequestBodyBytes {
		budget = maxRequestBodyBytes
	}
	return budget
}

// detectMemoryLimitBytes reports the memory ceiling the process runs under,
// preferring an active cgroup limit over total host memory. It returns 0 when
// nothing can be determined.
func detectMemoryLimitBytes() int64 {
	host := readMemTotalBytes()
	limit := readCgroupMemoryLimitBytes()
	switch {
	case limit > 0 && host > 0:
		if limit < host {
			return limit
		}
		return host
	case limit > 0:
		return limit
	default:
		return host
	}
}

func readCgroupMemoryLimitBytes() int64 {
	if v, ok := parseCgroupMemoryLimit(readFileTrimmed("/sys/fs/cgroup/memory.max")); ok {
		return v // cgroup v2
	}
	if v, ok := parseCgroupMemoryLimit(readFileTrimmed("/sys/fs/cgroup/memory/memory.limit_in_bytes")); ok {
		return v // cgroup v1
	}
	return 0
}

func parseCgroupMemoryLimit(value string) (int64, bool) {
	if value == "" || value == "max" {
		return 0, false
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	// cgroup v1 encodes "unlimited" as a value near math.MaxInt64 rounded to the
	// page size; treat implausibly large limits as unset.
	if n >= 1<<62 {
		return 0, false
	}
	return n, true
}

func readMemTotalBytes() int64 {
	content, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil || kb <= 0 {
			return 0
		}
		return kb * 1024
	}
	return 0
}

func readFileTrimmed(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(content))
}
