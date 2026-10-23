package gateway

import "gpt-load/internal/platform/memlimit"

const (
	// maxRequestBodyBytes 是数据面请求体上限的最大值，也是探测不到内存时的取值。
	maxRequestBodyBytes = int64(128 << 20)
	minRequestBodyBytes = int64(16 << 20)
	// requestBodyMemoryDivisor 把单个请求体限制在可用内存的 1/14：转发和协议转换期间请求体会以多份
	// 解码副本的形式占用自身大小数倍的内存，这一比例以按可用内存自动设置 GOMEMLIMIT 为前提。
	requestBodyMemoryDivisor = 14
)

// requestBodyLimitFor 按进程可用内存推算单个数据面请求体的上限，结果限定在 16MiB 到 128MiB 之间；
// 探测不到内存时保持 128MiB。
func requestBodyLimitFor(memory memlimit.Memory) int64 {
	available := memory.Available()
	if available <= 0 {
		return maxRequestBodyBytes
	}
	return min(max(available/requestBodyMemoryDivisor, minRequestBodyBytes), maxRequestBodyBytes)
}

// ConfigureRequestBodyLimit 在依赖装配阶段、HTTP 流量到达前按进程可用内存设置请求体上限。
func (handler *Handler) ConfigureRequestBodyLimit(memory memlimit.Memory) {
	handler.requestBodyLimit = requestBodyLimitFor(memory)
}

// RequestBodyLimit 返回生效的数据面请求体上限，传输大小和解码后大小都受它约束；未配置时为 128MiB。
func (handler *Handler) RequestBodyLimit() int64 {
	if handler.requestBodyLimit <= 0 {
		return maxRequestBodyBytes
	}
	return handler.requestBodyLimit
}
