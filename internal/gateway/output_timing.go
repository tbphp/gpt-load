package gateway

import (
	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/execution"
	"gpt-load/internal/protocol"
)

// outputTimingSink 在成功写入并 flush 后观察实际交付字节，包含脱敏缓冲释放的内容。
// 独立于上游空回判定，观测失败只停止采样，不改变转发结果。
func outputTimingSink(value protocol.Protocol, notify func()) func([]byte) {
	if notify == nil {
		return func([]byte) {}
	}
	switch value {
	case protocol.OpenAICompletions, protocol.OpenAIResponses, protocol.Anthropic, protocol.Gemini:
	default:
		return func([]byte) {}
	}
	observer := dialect.OutputTimingObserver{Protocol: value}
	produced, failed := false, false
	buffer := newSSEEventObservationBuffer(execution.SSEEventLimit(value), func(event dialect.StreamEvent, _ bool) (bool, error) {
		output := observer.Observe(event)
		produced = produced || output
		return false, nil
	})
	return func(chunk []byte) {
		if failed {
			return
		}
		produced = false
		if _, _, err := buffer.push(chunk); err != nil {
			failed = true
			logrus.WithError(err).Debug("Output timing observation stopped")
			return
		}
		// 同一次交付中的多个事件共享时点，不能人为制造 token 间隔。
		if produced {
			notify()
		}
	}
}
