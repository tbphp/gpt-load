package gateway

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"gpt-load/internal/platform/contentcoding"
)

// pausedUpload 构造一个发出 prefix 后暂停上传的请求；返回的函数结束上传。
func pausedUpload(contentLength int64, prefix string) (*http.Request, func()) {
	reader, writer := io.Pipe()
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", reader)
	request.ContentLength = contentLength
	go func() { _, _ = writer.Write([]byte(prefix)) }()
	return request, func() { _ = writer.Close() }
}

func waitBodyMemoryCharged(t *testing.T, budget *bodyMemoryBudget) int64 {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		budget.mu.Lock()
		used := budget.used
		budget.mu.Unlock()
		if used > 0 {
			return used
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("paused upload never charged the budget")
	return 0
}

// TestAcquireDecodedRequestBodyPausedUploadHoldsOnlyReceivedBytes 回归：暂停或慢速的上传
// 只占用它已经发来的字节，不能占住整个预算让其他请求返回 503。
func TestAcquireDecodedRequestBodyPausedUploadHoldsOnlyReceivedBytes(t *testing.T) {
	handler := &Handler{bodyMemory: newBodyMemoryBudget(128 << 20)}
	const prefix = `{"model":"x","messages":[`
	paused, finish := pausedUpload(65, prefix)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, release, err := handler.acquireDecodedRequestBody(paused, contentcoding.Identity)
		if err == nil {
			release()
		}
	}()

	if used := waitBodyMemoryCharged(t, handler.bodyMemory); used != int64(len(prefix)) {
		t.Errorf("paused upload holds %d bytes; want only the %d bytes received", used, len(prefix))
	}

	normal := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"x"}`))
	normal.ContentLength = 13
	body, release, err := handler.acquireDecodedRequestBody(normal, contentcoding.Identity)
	if err != nil {
		t.Errorf("normal request while another upload is paused: err = %v; want admitted", err)
	} else {
		if string(body) != `{"model":"x"}` {
			t.Errorf("body = %q", body)
		}
		release()
	}

	finish()
	<-done
	if handler.bodyMemory.used != 0 {
		t.Fatalf("used = %d after all requests finished; want 0", handler.bodyMemory.used)
	}
}

// TestAcquireDecodedRequestBodyOversizedContentLengthIsTooLarge 回归：超过单请求上限的
// 请求无论预算是否紧张都返回 413，不能变成可重试的 503。
func TestAcquireDecodedRequestBodyOversizedContentLengthIsTooLarge(t *testing.T) {
	handler := &Handler{bodyMemory: newBodyMemoryBudget(128 << 20)}
	handler.bodyMemory.used = 1 // 其他在途请求已占用 1 字节
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("x"))
	request.ContentLength = maxRequestBodyBytes + 1
	_, release, err := handler.acquireDecodedRequestBody(request, contentcoding.Identity)
	if !errors.Is(err, errRequestTooLarge) || release != nil {
		t.Fatalf("err = %v, release!=nil=%t; want errRequestTooLarge", err, release != nil)
	}
	if handler.bodyMemory.used != 1 {
		t.Fatalf("used = %d; a rejected request must not keep a charge", handler.bodyMemory.used)
	}
}

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
	if !budget.tryCharge(1 << 62) {
		t.Fatal("nil budget must accept any charge")
	}
	budget.release(5) // must not panic
}

func TestBodyMemoryBudgetChargeAndRelease(t *testing.T) {
	budget := newBodyMemoryBudget(100)
	if !budget.tryCharge(60) {
		t.Fatal("tryCharge(60) should fit")
	}
	if budget.tryCharge(41) {
		t.Fatal("tryCharge(41) must not exceed the budget")
	}
	if !budget.tryCharge(40) {
		t.Fatal("tryCharge(40) should exactly fill the budget")
	}
	if budget.tryCharge(1) {
		t.Fatal("tryCharge(1) must fail when full")
	}
	budget.release(50)
	if !budget.tryCharge(50) {
		t.Fatal("released bytes should be chargeable again")
	}
	// Over-release must clamp at zero, never go negative.
	budget.release(1 << 30)
	if budget.used != 0 {
		t.Fatalf("used = %d after over-release; want 0", budget.used)
	}
}

func TestBodyMemoryMeterReleasesEverythingItCharged(t *testing.T) {
	budget := newBodyMemoryBudget(100)
	meter := &bodyMemoryMeter{budget: budget}
	if err := meter.charge(30); err != nil {
		t.Fatal(err)
	}
	if err := meter.charge(30); err != nil {
		t.Fatal(err)
	}
	if err := meter.charge(41); !errors.Is(err, errServerBusy) {
		t.Fatalf("charge past the budget = %v; want errServerBusy", err)
	}
	meter.release()
	meter.release() // 重复释放不能多退
	if budget.used != 0 {
		t.Fatalf("used = %d after release; want 0", budget.used)
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
				if !budget.tryCharge(4 << 20) {
					continue
				}
				budget.mu.Lock()
				if budget.used > max {
					budget.mu.Unlock()
					t.Errorf("used %d exceeded max %d", budget.used, max)
					return
				}
				budget.mu.Unlock()
				budget.release(4 << 20)
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

// TestDetectMemoryLimitBytesResolvesProcessCgroup 回归：内存上限要按进程实际所在的
// cgroup 及其上级组读取，不能只看挂载根目录（host cgroupns、systemd MemoryMax 等场景）。
func TestDetectMemoryLimitBytesResolvesProcessCgroup(t *testing.T) {
	const hostMemInfo = "MemTotal:       16777216 kB\n"
	const host = int64(16 << 30)
	tests := []struct {
		name  string
		files map[string]string
		want  int64
	}{
		{
			name: "v2 private namespace",
			files: map[string]string{
				"proc/self/cgroup":         "0::/",
				"sys/fs/cgroup/memory.max": "536870912",
			},
			want: 512 << 20,
		},
		{
			name: "v2 host namespace",
			files: map[string]string{
				"proc/self/cgroup":                    "0::/docker/abc",
				"sys/fs/cgroup/memory.max":            "max",
				"sys/fs/cgroup/docker/memory.max":     "max",
				"sys/fs/cgroup/docker/abc/memory.max": "536870912",
			},
			want: 512 << 20,
		},
		{
			name: "v2 limit on an ancestor",
			files: map[string]string{
				"proc/self/cgroup": "0::/system.slice/gpt-load.service",
				"sys/fs/cgroup/system.slice/gpt-load.service/memory.max": "max",
				"sys/fs/cgroup/system.slice/memory.max":                  "1073741824",
			},
			want: 1 << 30,
		},
		{
			name: "v1 container subtree mounted at the controller root",
			files: map[string]string{
				"proc/self/cgroup":                           "11:memory:/docker/abc\n1:name=systemd:/docker/abc",
				"sys/fs/cgroup/memory/memory.limit_in_bytes": "536870912",
			},
			want: 512 << 20,
		},
		{
			name: "v1 native service",
			files: map[string]string{
				"proc/self/cgroup":                                                  "11:memory:/system.slice/x.service",
				"sys/fs/cgroup/memory/memory.limit_in_bytes":                        "9223372036854771712",
				"sys/fs/cgroup/memory/system.slice/x.service/memory.limit_in_bytes": "268435456",
			},
			want: 256 << 20,
		},
		{
			name: "group outside the namespace only consults the mount root",
			files: map[string]string{
				"proc/self/cgroup":                 "0::/../escaped",
				"sys/fs/cgroup/memory.max":         "max",
				"sys/fs/cgroup/escaped/memory.max": "1048576",
			},
			want: host,
		},
		{
			name: "no cgroup limit uses host memory",
			files: map[string]string{
				"proc/self/cgroup":         "0::/",
				"sys/fs/cgroup/memory.max": "max",
			},
			want: host,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			writeFakeFile(t, root, "proc/meminfo", hostMemInfo)
			for name, content := range tt.files {
				writeFakeFile(t, root, name, content)
			}
			if got := detectMemoryLimitBytesAt(root); got != tt.want {
				t.Fatalf("detectMemoryLimitBytesAt() = %d MiB; want %d MiB", got>>20, tt.want>>20)
			}
		})
	}

	t.Run("nothing detectable", func(t *testing.T) {
		if got := detectMemoryLimitBytesAt(t.TempDir()); got != 0 {
			t.Fatalf("detectMemoryLimitBytesAt() = %d; want 0", got)
		}
	})
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

	// About 16MB held, 4MB left: an 8MB body cannot fit and must be refused as
	// busy without pushing aggregate memory past the budget.
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

// TestAcquireDecodedRequestBodyStopsDecompressionBomb 回归：解码出的数据边解边计费，
// 一个压缩体膨胀超过剩余预算时立即停止解码并返回 503，且不留下任何计费。
func TestAcquireDecodedRequestBodyStopsDecompressionBomb(t *testing.T) {
	handler := &Handler{bodyMemory: newBodyMemoryBudget(20 << 20)}
	_, release, err := handler.acquireDecodedRequestBody(gzipRequest(t, 100<<20), contentcoding.Gzip)
	if !errors.Is(err, errServerBusy) || release != nil {
		t.Fatalf("err = %v, release!=nil=%t; want errServerBusy", err, release != nil)
	}
	if handler.bodyMemory.used != 0 {
		t.Fatalf("used = %d after a refused request; want 0", handler.bodyMemory.used)
	}
}

// TestAcquireDecodedRequestBodyReleasesEncodedChargeAfterDecoding 回归：解压完成后压缩前的
// 数据不再使用，只保留解压体的计费，压缩比低的请求不能一直白占预算。
func TestAcquireDecodedRequestBodyReleasesEncodedChargeAfterDecoding(t *testing.T) {
	plain := make([]byte, 5<<20)
	if _, err := rand.Read(plain); err != nil { // 难压缩，压缩后体积接近原文
		t.Fatal(err)
	}
	encoded := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, plain)
	newRequest := func() *http.Request {
		request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(encoded))
		request.ContentLength = int64(len(encoded))
		return request
	}
	handler := &Handler{bodyMemory: newBodyMemoryBudget(16 << 20)}

	body1, release1, err := handler.acquireDecodedRequestBody(newRequest(), contentcoding.Gzip)
	if err != nil || len(body1) != len(plain) {
		t.Fatalf("first acquire = len %d, %v; want %d, nil", len(body1), err, len(plain))
	}
	if handler.bodyMemory.used != int64(len(body1)) {
		t.Fatalf("used = %d after decoding; want only the decoded body (%d)", handler.bodyMemory.used, len(body1))
	}
	_, release2, err := handler.acquireDecodedRequestBody(newRequest(), contentcoding.Gzip)
	if err != nil {
		t.Fatalf("second request: err = %v; want admitted", err)
	}
	release1()
	release2()
	if handler.bodyMemory.used != 0 {
		t.Fatalf("used = %d after releasing all; want 0", handler.bodyMemory.used)
	}
}

// TestAcquireDecodedRequestBodyChargesIdentityOnce 回归：identity 请求体只计一份。
func TestAcquireDecodedRequestBodyChargesIdentityOnce(t *testing.T) {
	const size = 1 << 20
	handler := &Handler{bodyMemory: newBodyMemoryBudget(128 << 20)}
	request := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(bytes.Repeat([]byte("A"), size)))
	request.ContentLength = size
	body, release, err := handler.acquireDecodedRequestBody(request, contentcoding.Identity)
	if err != nil || len(body) != size {
		t.Fatalf("acquire = len %d, %v; want %d, nil", len(body), err, size)
	}
	if handler.bodyMemory.used != size {
		t.Fatalf("used = %d; want %d (one copy)", handler.bodyMemory.used, size)
	}
	release()
}

// TestReadDecodedRequestBodyMeteredOverflowIsTooLarge 回归：为判断超限而多读的字节不计费，
// 预算恰好等于单请求上限时，超限请求返回 413 而不是 503。
func TestReadDecodedRequestBodyMeteredOverflowIsTooLarge(t *testing.T) {
	t.Run("identity without content length", func(t *testing.T) {
		meter := &bodyMemoryMeter{budget: newBodyMemoryBudget(4)}
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345"))
		request.ContentLength = -1
		_, err := readDecodedRequestBodyMetered(request, contentcoding.Identity, 4, 4, meter.charge)
		if !errors.Is(err, errRequestTooLarge) {
			t.Fatalf("err = %v; want errRequestTooLarge", err)
		}
	})
	t.Run("gzip decoded overflow", func(t *testing.T) {
		encoded := encodeContentCodingForGatewayTest(t, contentcoding.Gzip, []byte("12345"))
		meter := &bodyMemoryMeter{budget: newBodyMemoryBudget(int64(len(encoded)) + 4)}
		request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(encoded))
		_, err := readDecodedRequestBodyMetered(request, contentcoding.Gzip, int64(len(encoded)), 4, meter.charge)
		if !errors.Is(err, errRequestTooLarge) {
			t.Fatalf("err = %v; want errRequestTooLarge", err)
		}
	})
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
