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
