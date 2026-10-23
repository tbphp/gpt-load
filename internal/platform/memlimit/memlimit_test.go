package memlimit

import (
	"math"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"testing"
)

// mountinfo 中常见的挂载行：可选字段数量不定，以单独的 "-" 结束。
const (
	rootfsMountLine   = "25 1 8:1 / / rw,relatime shared:1 - ext4 /dev/sda1 rw"
	cgroup2MountLine  = "30 25 0:26 / /sys/fs/cgroup rw,nosuid,nodev,noexec,relatime shared:4 - cgroup2 cgroup2 rw,nsdelegate"
	v1MemoryMountLine = "38 30 0:33 / /sys/fs/cgroup/memory rw,nosuid,nodev,noexec,relatime shared:16 - cgroup cgroup rw,memory"
)

// TestDetectAtResolvesProcessCgroup 回归：内存限额要按进程实际所在的 cgroup 及其上级组读取。组路径要按
// mountinfo 中的挂载点和挂载根换算，不能只看挂载根，也不能把完整组路径直接拼到默认挂载点下（子树挂载、
// host cgroupns、systemd MemoryMax 等场景）。
func TestDetectAtResolvesProcessCgroup(t *testing.T) {
	const hostMemInfo = "MemTotal:       16777216 kB\n"
	const host = int64(16 << 30)
	tests := []struct {
		name      string
		mountinfo string
		files     map[string]string
		wantLimit int64
	}{
		{
			name:      "v2 private namespace",
			mountinfo: rootfsMountLine + "\n1290 1283 0:31 / /sys/fs/cgroup ro,nosuid,nodev,noexec,relatime - cgroup2 cgroup rw,nsdelegate",
			files: map[string]string{
				"proc/self/cgroup":         "0::/",
				"sys/fs/cgroup/memory.max": "536870912",
			},
			wantLimit: 512 << 20,
		},
		{
			name:      "v2 host namespace",
			mountinfo: rootfsMountLine + "\n" + cgroup2MountLine,
			files: map[string]string{
				"proc/self/cgroup":                    "0::/docker/abc",
				"sys/fs/cgroup/memory.max":            "max",
				"sys/fs/cgroup/docker/memory.max":     "max",
				"sys/fs/cgroup/docker/abc/memory.max": "536870912",
			},
			wantLimit: 512 << 20,
		},
		{
			name:      "v2 limit on an ancestor",
			mountinfo: rootfsMountLine + "\n" + cgroup2MountLine,
			files: map[string]string{
				"proc/self/cgroup": "0::/system.slice/gpt-load.service",
				"sys/fs/cgroup/system.slice/gpt-load.service/memory.max": "max",
				"sys/fs/cgroup/system.slice/memory.max":                  "1073741824",
			},
			wantLimit: 1 << 30,
		},
		{
			// 挂载根是进程组的上级组：去掉挂载根前缀后再拼到挂载点下。
			name:      "v2 subtree mounted below the hierarchy root",
			mountinfo: "1290 1283 0:31 /tenant /sys/fs/cgroup ro,nosuid,nodev,noexec,relatime - cgroup2 cgroup rw",
			files: map[string]string{
				"proc/self/cgroup":                 "0::/tenant/service",
				"sys/fs/cgroup/memory.max":         "max",
				"sys/fs/cgroup/service/memory.max": "536870912",
			},
			wantLimit: 512 << 20,
		},
		{
			name:      "v2 mount root is the process group",
			mountinfo: "1290 1283 0:31 /docker/abc /sys/fs/cgroup ro,nosuid,nodev,noexec,relatime - cgroup2 cgroup rw",
			files: map[string]string{
				"proc/self/cgroup":         "0::/docker/abc",
				"sys/fs/cgroup/memory.max": "536870912",
			},
			wantLimit: 512 << 20,
		},
		{
			name:      "v2 mounted outside /sys/fs/cgroup",
			mountinfo: rootfsMountLine + "\n40 25 0:26 / /mnt/cgroup2 rw,relatime shared:9 - cgroup2 none rw",
			files: map[string]string{
				"proc/self/cgroup":             "0::/app",
				"mnt/cgroup2/memory.max":       "max",
				"mnt/cgroup2/app/memory.max":   "536870912",
				"sys/fs/cgroup/app/memory.max": "1048576",
			},
			wantLimit: 512 << 20,
		},
		{
			name:      "v1 container subtree mounted at the controller root",
			mountinfo: rootfsMountLine + "\n1312 1305 0:40 /docker/abc /sys/fs/cgroup/memory ro,nosuid,nodev,noexec,relatime master:21 - cgroup cgroup rw,memory",
			files: map[string]string{
				"proc/self/cgroup":                           "11:memory:/docker/abc\n1:name=systemd:/docker/abc",
				"sys/fs/cgroup/memory/memory.limit_in_bytes": "536870912",
			},
			wantLimit: 512 << 20,
		},
		{
			name:      "v1 native service",
			mountinfo: rootfsMountLine + "\n" + v1MemoryMountLine,
			files: map[string]string{
				"proc/self/cgroup":                                                  "11:memory:/system.slice/x.service",
				"sys/fs/cgroup/memory/memory.limit_in_bytes":                        "9223372036854771712",
				"sys/fs/cgroup/memory/system.slice/x.service/memory.limit_in_bytes": "268435456",
			},
			wantLimit: 256 << 20,
		},
		{
			name:      "v1 subtree mounted below the hierarchy root",
			mountinfo: "1312 1305 0:40 /kubepods /sys/fs/cgroup/memory ro,nosuid,nodev,noexec,relatime master:21 - cgroup cgroup rw,memory",
			files: map[string]string{
				"proc/self/cgroup":                                    "9:memory:/kubepods/pod1/ctr",
				"sys/fs/cgroup/memory/memory.limit_in_bytes":          "9223372036854771712",
				"sys/fs/cgroup/memory/pod1/ctr/memory.limit_in_bytes": "268435456",
			},
			wantLimit: 256 << 20,
		},
		{
			name:      "group outside the namespace only consults the mount root",
			mountinfo: rootfsMountLine + "\n" + cgroup2MountLine,
			files: map[string]string{
				"proc/self/cgroup":                 "0::/../escaped",
				"sys/fs/cgroup/memory.max":         "max",
				"sys/fs/cgroup/escaped/memory.max": "1048576",
			},
			wantLimit: 0,
		},
		{
			name:      "group outside the mount root only consults the mount root",
			mountinfo: "1290 1283 0:31 /tenant /sys/fs/cgroup ro - cgroup2 cgroup rw",
			files: map[string]string{
				"proc/self/cgroup":                       "0::/other/service",
				"sys/fs/cgroup/memory.max":               "max",
				"sys/fs/cgroup/other/service/memory.max": "1048576",
			},
			wantLimit: 0,
		},
		{
			name:      "no cgroup limit",
			mountinfo: rootfsMountLine + "\n" + cgroup2MountLine,
			files: map[string]string{
				"proc/self/cgroup":         "0::/",
				"sys/fs/cgroup/memory.max": "max",
			},
			wantLimit: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFakeFile(t, root, "proc/meminfo", hostMemInfo)
			writeFakeFile(t, root, "proc/self/mountinfo", tt.mountinfo)
			for name, content := range tt.files {
				writeFakeFile(t, root, name, content)
			}
			got := detectAt(root)
			if got.CgroupLimit != tt.wantLimit || got.Total != host {
				t.Fatalf("detectAt() = %+v; want cgroup limit %d, total %d", got, tt.wantLimit, host)
			}
		})
	}

	t.Run("nothing detectable", func(t *testing.T) {
		if got := detectAt(t.TempDir()); got != (Memory{}) {
			t.Fatalf("detectAt() = %+v; want zero", got)
		}
	})
}

func TestParseCgroupMounts(t *testing.T) {
	content := strings.Join([]string{
		rootfsMountLine,
		cgroup2MountLine,
		"31 25 0:27 /tenant /mnt/cgroup ro master:3 propagate_from:2 - cgroup2 cgroup2 rw",
		"32 25 0:28 /docker/abc /sys/fs/cgroup/memory ro,nosuid - cgroup cgroup rw,memory",
		"33 25 0:29 / /sys/fs/cgroup/cpu,cpuacct rw shared:5 - cgroup cgroup rw,cpu,cpuacct",
		"34 25 0:30 / /sys/fs/cgroup/blkio,memory rw - cgroup cgroup rw,blkio,memory",
		`35 25 0:31 /with\040space /mnt/esc\134aped rw - cgroup2 cgroup2 rw`,
		"36 25 0:32 / /mnt/broken rw shared:6 -",
		"not a mountinfo line",
	}, "\n")
	v2, v1Memory := parseCgroupMounts(content)
	wantV2 := []cgroupMount{
		{root: "/", point: "/sys/fs/cgroup"},
		{root: "/tenant", point: "/mnt/cgroup"},
		{root: "/with space", point: `/mnt/esc\aped`},
	}
	wantV1Memory := []cgroupMount{
		{root: "/docker/abc", point: "/sys/fs/cgroup/memory"},
		{root: "/", point: "/sys/fs/cgroup/blkio,memory"},
	}
	if !slices.Equal(v2, wantV2) || !slices.Equal(v1Memory, wantV1Memory) {
		t.Fatalf("parseCgroupMounts() = %+v, %+v; want %+v, %+v", v2, v1Memory, wantV2, wantV1Memory)
	}
}

func writeFakeFile(t *testing.T, root, name, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestMemoryAvailable(t *testing.T) {
	tests := []struct {
		name   string
		memory Memory
		want   int64
	}{
		{name: "unknown", memory: Memory{}, want: 0},
		{name: "host only", memory: Memory{Total: 16 << 30}, want: 16 << 30},
		{name: "cgroup only", memory: Memory{CgroupLimit: 512 << 20}, want: 512 << 20},
		{name: "cgroup below host", memory: Memory{CgroupLimit: 512 << 20, Total: 16 << 30}, want: 512 << 20},
		{name: "cgroup above host", memory: Memory{CgroupLimit: 64 << 30, Total: 16 << 30}, want: 16 << 30},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.memory.Available(); got != tt.want {
				t.Fatalf("Available() = %d; want %d", got, tt.want)
			}
		})
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
		{"9223372036854771712", 0, false}, // cgroup v1 的“无限制”取值
	}
	for _, tc := range cases {
		got, ok := parseCgroupMemoryLimit(tc.in)
		if got != tc.want || ok != tc.wantOK {
			t.Errorf("parseCgroupMemoryLimit(%q) = %d, %v; want %d, %v", tc.in, got, ok, tc.want, tc.wantOK)
		}
	}
}

// setStartupGOMEMLIMIT 模拟进程启动时环境中的 GOMEMLIMIT，测试结束后恢复。
func setStartupGOMEMLIMIT(t *testing.T, value string) {
	t.Helper()
	previous := startupGOMEMLIMIT
	startupGOMEMLIMIT = value
	t.Cleanup(func() { startupGOMEMLIMIT = previous })
}

// restoreGoMemoryLimit 先把进程的 Go 软内存上限设为 initial，测试结束后恢复原值。
func restoreGoMemoryLimit(t *testing.T, initial int64) {
	t.Helper()
	previous := debug.SetMemoryLimit(initial)
	t.Cleanup(func() { debug.SetMemoryLimit(previous) })
}

func TestApplyGoMemoryLimit(t *testing.T) {
	t.Run("cgroup limit without GOMEMLIMIT", func(t *testing.T) {
		setStartupGOMEMLIMIT(t, "")
		restoreGoMemoryLimit(t, math.MaxInt64)
		const want = int64(429496730) // 512MiB 的 80%
		limit, source := ApplyGoMemoryLimit(Memory{CgroupLimit: 512 << 20, Total: 16 << 30})
		if limit != want || source != GoMemoryLimitCgroup {
			t.Fatalf("ApplyGoMemoryLimit() = %d, %q; want %d, %q", limit, source, want, GoMemoryLimitCgroup)
		}
		if got := debug.SetMemoryLimit(-1); got != want {
			t.Fatalf("runtime memory limit = %d; want %d", got, want)
		}
	})

	t.Run("host memory below the cgroup limit", func(t *testing.T) {
		setStartupGOMEMLIMIT(t, "")
		restoreGoMemoryLimit(t, math.MaxInt64)
		const want = int64(858993460) // 1GiB 的 80%
		limit, source := ApplyGoMemoryLimit(Memory{CgroupLimit: 64 << 30, Total: 1 << 30})
		if limit != want || source != GoMemoryLimitMemTotal {
			t.Fatalf("ApplyGoMemoryLimit() = %d, %q; want %d, %q", limit, source, want, GoMemoryLimitMemTotal)
		}
	})

	// 运营者设置的 GOMEMLIMIT 已由运行时在启动时读取，任何取值都不能被覆盖。
	for _, value := range []string{"3GiB", "off"} {
		t.Run("operator GOMEMLIMIT="+value, func(t *testing.T) {
			setStartupGOMEMLIMIT(t, value)
			const operatorLimit = int64(3 << 30)
			restoreGoMemoryLimit(t, operatorLimit)
			limit, source := ApplyGoMemoryLimit(Memory{CgroupLimit: 512 << 20, Total: 16 << 30})
			if limit != operatorLimit || source != GoMemoryLimitEnvironment {
				t.Fatalf("ApplyGoMemoryLimit() = %d, %q; want %d, %q", limit, source, operatorLimit, GoMemoryLimitEnvironment)
			}
			if got := debug.SetMemoryLimit(-1); got != operatorLimit {
				t.Fatalf("runtime memory limit = %d; want operator value %d", got, operatorLimit)
			}
		})
	}

	// .env 在运行时读取 GOMEMLIMIT 之后才写入环境变量，运行时不会应用它，不能当作运营者已设置。
	t.Run("GOMEMLIMIT written after startup is ignored", func(t *testing.T) {
		setStartupGOMEMLIMIT(t, "")
		t.Setenv("GOMEMLIMIT", "3GiB")
		restoreGoMemoryLimit(t, math.MaxInt64)
		const want = int64(429496730) // 512MiB 的 80%
		limit, source := ApplyGoMemoryLimit(Memory{CgroupLimit: 512 << 20, Total: 16 << 30})
		if limit != want || source != GoMemoryLimitCgroup {
			t.Fatalf("ApplyGoMemoryLimit() = %d, %q; want %d, %q", limit, source, want, GoMemoryLimitCgroup)
		}
		if got := debug.SetMemoryLimit(-1); got != want {
			t.Fatalf("runtime memory limit = %d; want %d", got, want)
		}
	})

	// 没有 cgroup 限额的主机（如未设内存限额的容器、裸机）按 MemTotal 设置。
	t.Run("host memory without a cgroup limit", func(t *testing.T) {
		setStartupGOMEMLIMIT(t, "")
		restoreGoMemoryLimit(t, math.MaxInt64)
		const want = int64(429496730) // 512MiB 的 80%
		limit, source := ApplyGoMemoryLimit(Memory{Total: 512 << 20})
		if limit != want || source != GoMemoryLimitMemTotal {
			t.Fatalf("ApplyGoMemoryLimit() = %d, %q; want %d, %q", limit, source, want, GoMemoryLimitMemTotal)
		}
		if got := debug.SetMemoryLimit(-1); got != want {
			t.Fatalf("runtime memory limit = %d; want %d", got, want)
		}
	})

	t.Run("operator GOMEMLIMIT wins without a cgroup limit", func(t *testing.T) {
		setStartupGOMEMLIMIT(t, "off")
		restoreGoMemoryLimit(t, math.MaxInt64)
		limit, source := ApplyGoMemoryLimit(Memory{Total: 512 << 20})
		if limit != math.MaxInt64 || source != GoMemoryLimitEnvironment {
			t.Fatalf("ApplyGoMemoryLimit() = %d, %q; want unlimited, %q", limit, source, GoMemoryLimitEnvironment)
		}
		if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
			t.Fatalf("runtime memory limit = %d; want the operator's unlimited setting", got)
		}
	})

	t.Run("nothing detected", func(t *testing.T) {
		setStartupGOMEMLIMIT(t, "")
		restoreGoMemoryLimit(t, math.MaxInt64)
		limit, source := ApplyGoMemoryLimit(Memory{})
		if limit != math.MaxInt64 || source != GoMemoryLimitNone {
			t.Fatalf("ApplyGoMemoryLimit() = %d, %q; want unlimited, %q", limit, source, GoMemoryLimitNone)
		}
		if got := debug.SetMemoryLimit(-1); got != math.MaxInt64 {
			t.Fatalf("runtime memory limit = %d; want unlimited", got)
		}
	})
}
