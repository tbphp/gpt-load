package gateway

import (
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
)

// bodyMemoryBudget bounds the total memory held by data-plane request bodies that
// are in flight at the same time. Each request is already capped at
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

// tryCharge adds n bytes to the in-flight total if the total stays within the
// budget. A nil budget is unlimited.
func (b *bodyMemoryBudget) tryCharge(n int64) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if n > b.max-b.used {
		return false
	}
	b.used += n
	return true
}

// release returns n previously charged bytes. A nil budget is a no-op.
func (b *bodyMemoryBudget) release(n int64) {
	if b == nil || n <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= n
	if b.used < 0 {
		b.used = 0
	}
}

// bodyMemoryMeter charges one request's body bytes to the shared budget as they
// are buffered, so a request holds only the memory it has actually received
// instead of a worst-case reservation that slow or paused uploads could pin.
type bodyMemoryMeter struct {
	budget *bodyMemoryBudget
	held   int64
}

func (m *bodyMemoryMeter) charge(n int) error {
	if !m.budget.tryCharge(int64(n)) {
		return errServerBusy
	}
	m.held += int64(n)
	return nil
}

// shrinkTo returns any charge above n bytes, keeping n charged.
func (m *bodyMemoryMeter) shrinkTo(n int64) {
	if excess := m.held - n; excess > 0 {
		m.budget.release(excess)
		m.held = n
	}
}

func (m *bodyMemoryMeter) release() {
	m.budget.release(m.held)
	m.held = 0
}

// meteredBodyReader reports each chunk of request-body bytes it reads to charge.
// Bytes past remaining are only read to detect an oversized body, so they are not
// reported.
type meteredBodyReader struct {
	reader    io.Reader
	charge    func(int) error
	remaining int64
}

func (r *meteredBodyReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if chargeable := min(int64(n), r.remaining); chargeable > 0 {
		r.remaining -= chargeable
		if chargeErr := r.charge(int(chargeable)); chargeErr != nil {
			return n, chargeErr
		}
	}
	return n, err
}

const (
	// bodyMemoryBudgetFraction reserves 1/N of the detected memory ceiling for
	// in-flight request bodies; the rest absorbs allocator and GC overhead beyond
	// the buffered bytes the budget counts.
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

// detectMemoryLimitBytes reports the memory ceiling the process runs under: the
// smaller of its tightest cgroup limit and total host memory. It returns 0 when
// nothing can be determined.
func detectMemoryLimitBytes() int64 {
	return detectMemoryLimitBytesAt("/")
}

// detectMemoryLimitBytesAt resolves every path below root so tests can supply a
// fake /proc and /sys tree.
func detectMemoryLimitBytesAt(root string) int64 {
	host := readMemTotalBytes(root)
	limit := readCgroupMemoryLimitBytes(root)
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

// readCgroupMemoryLimitBytes returns the tightest memory limit applying to the
// process: those of its own cgroup and every ancestor, plus the mount root (the
// process's own group under a private cgroup namespace). It returns 0 when no
// limit is set.
func readCgroupMemoryLimitBytes(root string) int64 {
	v2Group, v1MemoryGroup := parseProcCgroup(readFileTrimmed(filepath.Join(root, "proc/self/cgroup")))
	var tightest int64
	consider := func(mount, group, file string) {
		for _, dir := range cgroupLineage(group) {
			limit, ok := parseCgroupMemoryLimit(readFileTrimmed(filepath.Join(root, mount, filepath.FromSlash(dir), file)))
			if ok && (tightest == 0 || limit < tightest) {
				tightest = limit
			}
		}
	}
	consider("sys/fs/cgroup", v2Group, "memory.max")                         // cgroup v2
	consider("sys/fs/cgroup/memory", v1MemoryGroup, "memory.limit_in_bytes") // cgroup v1
	return tightest
}

// parseProcCgroup extracts the process's cgroup v2 path and cgroup v1 memory
// controller path from /proc/self/cgroup.
func parseProcCgroup(content string) (v2Group, v1MemoryGroup string) {
	for _, line := range strings.Split(content, "\n") {
		fields := strings.SplitN(line, ":", 3)
		if len(fields) != 3 {
			continue
		}
		if fields[0] == "0" && fields[1] == "" {
			v2Group = fields[2]
			continue
		}
		for _, controller := range strings.Split(fields[1], ",") {
			if controller == "memory" {
				v1MemoryGroup = fields[2]
			}
		}
	}
	return v2Group, v1MemoryGroup
}

// cgroupLineage lists a cgroup path, its ancestors and the mount root. A missing
// or unclean path (such as a group outside the process's cgroup namespace) only
// consults the mount root.
func cgroupLineage(group string) []string {
	if group == "" || !path.IsAbs(group) || path.Clean(group) != group {
		return []string{"/"}
	}
	lineage := []string{group}
	for group != "/" {
		group = path.Dir(group)
		lineage = append(lineage, group)
	}
	return lineage
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

func readMemTotalBytes(root string) int64 {
	content, err := os.ReadFile(filepath.Join(root, "proc/meminfo"))
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
