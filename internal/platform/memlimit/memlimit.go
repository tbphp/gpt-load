// Package memlimit 探测进程可用的内存，并据此设置 Go 运行时的软内存上限。
package memlimit

import (
	"os"
	"path"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
)

// Memory 描述进程运行时受到的内存约束。
type Memory struct {
	// CgroupLimit 是进程所在 cgroup 及其上级组中最小的内存限额；未设限额或无法读取时为 0。
	CgroupLimit int64
	// Total 是 /proc/meminfo 报告的主机内存；无法读取时为 0。
	Total int64
}

// Detect 读取进程的 cgroup 内存限额和主机内存；没有 cgroup 或 /proc 的平台返回零值。
func Detect() Memory { return detectAt("/") }

// detectAt 把所有路径解析到 root 之下，便于测试提供伪造的 /proc 和 /sys。
func detectAt(root string) Memory {
	return Memory{CgroupLimit: readCgroupMemoryLimitBytes(root), Total: readMemTotalBytes(root)}
}

// Available 返回已知值中较小的一个；两者都未知时返回 0。
func (memory Memory) Available() int64 {
	if memory.CgroupLimit > 0 && (memory.Total <= 0 || memory.CgroupLimit < memory.Total) {
		return memory.CgroupLimit
	}
	return max(memory.Total, 0)
}

// GoMemoryLimitSource 说明当前 Go 软内存上限的来源。
type GoMemoryLimitSource string

const (
	GoMemoryLimitEnvironment GoMemoryLimitSource = "environment"
	GoMemoryLimitCgroup      GoMemoryLimitSource = "cgroup"
	GoMemoryLimitMemTotal    GoMemoryLimitSource = "memtotal"
	GoMemoryLimitNone        GoMemoryLimitSource = "none"
)

// ApplyGoMemoryLimit 在运营者未设置 GOMEMLIMIT 时，把 Go 软内存上限设为可用内存（cgroup 限额与 MemTotal
// 中较小者）的 80%，返回之后生效的上限（未限制时为 math.MaxInt64）及其来源。
func ApplyGoMemoryLimit(memory Memory) (int64, GoMemoryLimitSource) {
	// 运行时在启动时已读取 GOMEMLIMIT（含 off），这里只需不覆盖。
	if os.Getenv("GOMEMLIMIT") != "" {
		return debug.SetMemoryLimit(-1), GoMemoryLimitEnvironment
	}
	available := memory.Available()
	if available <= 0 {
		return debug.SetMemoryLimit(-1), GoMemoryLimitNone
	}
	source := GoMemoryLimitMemTotal
	if available == memory.CgroupLimit {
		source = GoMemoryLimitCgroup
	}
	// 剩余 20% 留给非堆内存，以及 GC 来不及回收时的余量。
	limit := available - available/5
	debug.SetMemoryLimit(limit)
	return limit, source
}

// readCgroupMemoryLimitBytes 返回进程所在 cgroup、各级上级组以及挂载根（私有 cgroup namespace 下即
// 进程自身的组）中最小的内存限额；均未设限额时返回 0。
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

// parseProcCgroup 从 /proc/self/cgroup 中取出 cgroup v2 路径和 cgroup v1 memory 控制器路径。
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

// cgroupLineage 列出 cgroup 路径、它的各级上级组和挂载根。路径缺失或不规整（例如位于进程 cgroup
// namespace 之外的组）时只查挂载根。
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
	// cgroup v1 用接近 math.MaxInt64 且按页对齐的值表示“无限制”，过大的值按未设限额处理。
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
