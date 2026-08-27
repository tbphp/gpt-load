package dialect

import "gpt-load/internal/usage"

// UsageExtractor optionally exposes provider-specific usage extraction.
type UsageExtractor interface {
	// ExtractUsage consumes a borrowed body valid only for this call.
	// Implementations must not mutate or retain it.
	ExtractUsage(body []byte) (usage.Result, error)
	NewUsageStreamExtractor() UsageStreamExtractor
}

// UsageStreamExtractor extracts usage from one provider response stream.
type UsageStreamExtractor interface {
	// Observe consumes a borrowed payload valid only for this call.
	// Implementations must not mutate or retain it.
	Observe(payload []byte) error
	Finalize() (usage.Result, bool)
}

// StreamUsageInjector optionally derives an OpenAI streaming request that asks
// the upstream to include terminal usage in the response stream.
type StreamUsageInjector interface {
	InjectStreamUsage(req *ParsedRequest) (*ParsedRequest, error)
}

var _ UsageExtractor = (*OpenAI)(nil)
var _ UsageExtractor = (*OpenAIResponses)(nil)
var _ UsageExtractor = (*OpenAIImages)(nil)
var _ UsageExtractor = (*Anthropic)(nil)
var _ UsageExtractor = (*Gemini)(nil)
var _ StreamUsageInjector = (*OpenAI)(nil)
