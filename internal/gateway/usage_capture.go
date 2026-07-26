package gateway

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

type usageCaptureBoundary struct {
	failureTotal atomic.Uint64
	warningMu    sync.Mutex
	lastWarning  time.Time
	now          func() time.Time
	logger       *logrus.Logger
}

func newUsageCaptureBoundary() *usageCaptureBoundary {
	return &usageCaptureBoundary{now: time.Now, logger: logrus.New()}
}

func missingUsage(invalidPayload bool) usage.Result {
	result := usage.Result{State: usage.StateMissing}
	if invalidPayload {
		result.Diagnostics.Add(usage.DiagnosticInvalidPayload)
	}
	return result
}

func validCapturedUsage(result usage.Result) bool {
	if result.Tokens.UncachedInput < 0 ||
		result.Tokens.CacheRead < 0 ||
		result.Tokens.CacheWrite5M < 0 ||
		result.Tokens.CacheWrite1H < 0 ||
		result.Tokens.Output < 0 {
		return false
	}
	switch result.State {
	case usage.StateComplete, usage.StatePartial:
		return true
	case usage.StateMissing:
		return result.Tokens == (usage.Tokens{})
	default:
		return false
	}
}

func safeExtractUsage(
	extractor dialect.UsageExtractor,
	body []byte,
) (result usage.Result, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			result = usage.Result{}
			err = nil
			panicked = true
		}
	}()
	result, err = extractor.ExtractUsage(body)
	return result, err, false
}

func (boundary *usageCaptureBoundary) recordFailure(_ string, _ protocol.Protocol) {
	if boundary != nil {
		boundary.failureTotal.Add(1)
	}
}

func (boundary *usageCaptureBoundary) extractNonStreaming(
	selected dialect.Dialect,
	headers http.Header,
	wire []byte,
) usage.Result {
	extractor, ok := selected.(dialect.UsageExtractor)
	if !ok {
		return missingUsage(false)
	}
	encoding, ok := inspectableErrorBodyEncoding(headers, wire)
	if !ok {
		boundary.recordFailure("decompress", selected.Protocol())
		return missingUsage(false)
	}
	body, err := utils.DecompressResponseLimited(
		encoding,
		wire,
		maxNonStreamingResponseBodyBytes,
	)
	if err != nil {
		boundary.recordFailure("decompress", selected.Protocol())
		return missingUsage(false)
	}
	result, err, panicked := safeExtractUsage(extractor, body)
	if err != nil || panicked || !validCapturedUsage(result) {
		boundary.recordFailure("extract", selected.Protocol())
		return missingUsage(true)
	}
	return result
}
