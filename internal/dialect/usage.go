package dialect

import "gpt-load/internal/usage"

// UsageExtractor optionally exposes provider-specific usage extraction.
type UsageExtractor interface {
	ExtractUsage(body []byte) (usage.Result, error)
	NewUsageStreamExtractor() UsageStreamExtractor
}

// UsageStreamExtractor extracts usage from one provider response stream.
type UsageStreamExtractor interface {
	Observe(payload []byte) error
	Finalize() (usage.Result, bool)
}

var _ UsageExtractor = (*OpenAI)(nil)
var _ UsageExtractor = (*Anthropic)(nil)
