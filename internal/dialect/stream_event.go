package dialect

// StreamEvent is the provider-neutral representation of one SSE data event.
// Name is empty when the upstream omitted the explicit SSE event field.
type StreamEvent struct {
	Name    string
	Payload []byte
}

type StreamEventDisposition uint8

const (
	StreamEventContinue StreamEventDisposition = iota
	StreamEventCompleted
	StreamEventIncomplete
	StreamEventFailed
)

type StreamEventClassification struct {
	Disposition StreamEventDisposition
}

func (classification StreamEventClassification) IsTerminal() bool {
	switch classification.Disposition {
	case StreamEventCompleted, StreamEventIncomplete, StreamEventFailed:
		return true
	default:
		return false
	}
}

func (classification StreamEventClassification) IsProviderError() bool {
	return classification.Disposition == StreamEventFailed
}

// StreamEventClassifier optionally exposes protocol-specific SSE lifecycle
// semantics without coupling the gateway to a concrete protocol.
type StreamEventClassifier interface {
	ClassifyStreamEvent(event StreamEvent) (StreamEventClassification, error)
	RequiresTerminalEvent() bool
}

// UsageStreamEventObserver optionally lets a usage extractor observe the
// explicit SSE event name in addition to its JSON payload.
type UsageStreamEventObserver interface {
	// ObserveStreamEvent consumes a borrowed payload valid only for this call.
	// Implementations must not mutate or retain it.
	ObserveStreamEvent(event StreamEvent) error
}

// StreamContent 描述单个流事件对响应产出的贡献，只服务于空回检测。
type StreamContent struct {
	// Produced 表示该事件为响应贡献了可见产出（文本、推理、工具调用或其他
	// 模态）。协议前导、保活与终态事件本身不算产出。
	Produced bool
	// ExplainedStop 表示该事件携带了自然结束以外的终止原因，例如输出预算
	// 耗尽、内容过滤或拒答。这类原因本身解释了为何没有产出，换候选重试无益。
	ExplainedStop bool
}

// StreamContentClassifier 可选地暴露协议级的产出判定。它独立于生命周期分类：
// 只在启用空回检测时调用，且不会因载荷形状而失败，不影响正常转发。
type StreamContentClassifier interface {
	ClassifyStreamContent(event StreamEvent) StreamContent
}
