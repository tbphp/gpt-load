package execution

import "gpt-load/internal/protocol"

const (
	DefaultUnaryResponseBodyLimitBytes      = int64(32 << 20)
	OpenAIImagesUnaryResponseBodyLimitBytes = int64(64 << 20)
	DefaultSSEEventLimitBytes               = 10 << 20
	OpenAIImagesSSEEventLimitBytes          = 32 << 20
)

// UnaryResponseBodyLimit returns the internal buffered success-response limit
// for one client wire protocol.
func UnaryResponseBodyLimit(clientProtocol protocol.Protocol) int64 {
	if clientProtocol == protocol.OpenAIImages {
		return OpenAIImagesUnaryResponseBodyLimitBytes
	}
	return DefaultUnaryResponseBodyLimitBytes
}

// SSEEventLimit returns the internal maximum size of one complete SSE event
// for one client wire protocol.
func SSEEventLimit(clientProtocol protocol.Protocol) int {
	if clientProtocol == protocol.OpenAIImages {
		return OpenAIImagesSSEEventLimitBytes
	}
	return DefaultSSEEventLimitBytes
}
