package gateway

import (
	"bytes"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/dialect"
	"gpt-load/internal/platform/utils"
	"gpt-load/internal/protocol"
	"gpt-load/internal/usage"
)

const usageCaptureWarningInterval = time.Minute

type usageCaptureBoundary struct {
	failureTotal atomic.Uint64
	warningMu    sync.Mutex
	lastWarning  time.Time
	now          func() time.Time
	logger       *logrus.Logger
}

type streamUsageCapture struct {
	boundary  *usageCaptureBoundary
	protocol  protocol.Protocol
	extractor dialect.UsageStreamExtractor
	active    bool
	finalized bool
	result    usage.Result
}

func newUsageCaptureBoundary() *usageCaptureBoundary {
	return &usageCaptureBoundary{now: time.Now, logger: logrus.StandardLogger()}
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

func (boundary *usageCaptureBoundary) recordFailure(phase string, value protocol.Protocol) {
	if boundary == nil {
		return
	}
	total := boundary.failureTotal.Add(1)
	now := boundary.now()
	boundary.warningMu.Lock()
	if !boundary.lastWarning.IsZero() && now.Before(boundary.lastWarning.Add(usageCaptureWarningInterval)) {
		boundary.warningMu.Unlock()
		return
	}
	boundary.lastWarning = now
	boundary.warningMu.Unlock()
	boundary.logger.WithFields(logrus.Fields{
		"phase":    phase,
		"protocol": value,
		"total":    total,
	}).Warn("Usage capture failure")
}

func safeNewUsageStreamExtractor(
	extractor dialect.UsageExtractor,
) (result dialect.UsageStreamExtractor, panicked bool) {
	defer func() {
		if recover() != nil {
			result = nil
			panicked = true
		}
	}()
	return extractor.NewUsageStreamExtractor(), false
}

func safeObserveUsage(
	extractor dialect.UsageStreamExtractor,
	payload []byte,
) (err error, panicked bool) {
	defer func() {
		if recover() != nil {
			err = nil
			panicked = true
		}
	}()
	err = extractor.Observe(payload)
	return err, false
}

func safeFinalizeUsage(
	extractor dialect.UsageStreamExtractor,
) (result usage.Result, finalized bool, panicked bool) {
	defer func() {
		if recover() != nil {
			result = usage.Result{}
			finalized = false
			panicked = true
		}
	}()
	result, finalized = extractor.Finalize()
	return result, finalized, false
}

func safeInjectStreamUsage(
	injector dialect.StreamUsageInjector,
	request *dialect.ParsedRequest,
) (result *dialect.ParsedRequest, err error, panicked bool) {
	defer func() {
		if recover() != nil {
			result = nil
			err = nil
			panicked = true
		}
	}()
	result, err = injector.InjectStreamUsage(request)
	return result, err, false
}

func (boundary *usageCaptureBoundary) injectStreamUsage(
	selected dialect.Dialect,
	request *dialect.ParsedRequest,
) *dialect.ParsedRequest {
	if boundary == nil || selected == nil || request == nil {
		return request
	}
	injector, ok := selected.(dialect.StreamUsageInjector)
	if !ok {
		return request
	}
	fallback := cloneParsedRequest(request)
	working := cloneParsedRequest(fallback)
	derived, err, panicked := safeInjectStreamUsage(injector, working)
	if panicked || err != nil || !isValidInjectedRequest(request, fallback, working, derived) ||
		int64(len(derived.Body)) > maxRequestBodyBytes {
		boundary.recordFailure("inject", selected.Protocol())
		return fallback
	}
	return cloneParsedRequest(derived)
}

func cloneParsedRequest(request *dialect.ParsedRequest) *dialect.ParsedRequest {
	if request == nil {
		return nil
	}
	return &dialect.ParsedRequest{
		Method:   request.Method,
		Path:     request.Path,
		RawQuery: request.RawQuery,
		Header:   request.Header.Clone(),
		Body:     bytes.Clone(request.Body),
	}
}

func isValidInjectedRequest(
	request, fallback, working, derived *dialect.ParsedRequest,
) bool {
	if request == nil || fallback == nil || working == nil || derived == nil ||
		derived == request || derived == working ||
		!reflect.DeepEqual(request, fallback) ||
		!reflect.DeepEqual(working, fallback) ||
		derived.Method != fallback.Method || derived.Path != fallback.Path ||
		derived.RawQuery != fallback.RawQuery ||
		!reflect.DeepEqual(derived.Header, fallback.Header) {
		return false
	}
	return true
}

func (boundary *usageCaptureBoundary) newStream(selected dialect.Dialect) *streamUsageCapture {
	capture := &streamUsageCapture{boundary: boundary, result: missingUsage(false)}
	if selected == nil {
		return capture
	}
	capture.protocol = selected.Protocol()
	extractor, ok := selected.(dialect.UsageExtractor)
	if !ok {
		return capture
	}
	streamExtractor, panicked := safeNewUsageStreamExtractor(extractor)
	if panicked || streamExtractor == nil {
		boundary.recordFailure("stream_constructor", capture.protocol)
		return capture
	}
	capture.extractor = streamExtractor
	capture.active = true
	return capture
}

func (capture *streamUsageCapture) observe(payload []byte) {
	if capture == nil || !capture.active || capture.finalized {
		return
	}
	err, panicked := safeObserveUsage(capture.extractor, bytes.Clone(payload))
	if panicked {
		capture.boundary.recordFailure("stream_observe", capture.protocol)
		capture.active = false
		return
	}
	if err != nil {
		capture.boundary.recordFailure("stream_observe", capture.protocol)
	}
}

func (capture *streamUsageCapture) finalize() usage.Result {
	if capture == nil {
		return missingUsage(false)
	}
	if capture.finalized {
		return capture.result
	}
	capture.finalized = true
	if !capture.active || capture.extractor == nil {
		return capture.result
	}
	result, finalized, panicked := safeFinalizeUsage(capture.extractor)
	if panicked || !finalized || !validCapturedUsage(result) {
		capture.boundary.recordFailure("stream_finalize", capture.protocol)
		capture.result = missingUsage(false)
		return capture.result
	}
	capture.result = result
	return capture.result
}

func (boundary *usageCaptureBoundary) extractNonStreaming(
	selected dialect.Dialect,
	headers http.Header,
	wire []byte,
) usage.Result {
	_, ok := selected.(dialect.UsageExtractor)
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
	return boundary.extractNonStreamingPlain(selected, body)
}

func (boundary *usageCaptureBoundary) extractNonStreamingPlain(
	selected dialect.Dialect,
	plain []byte,
) usage.Result {
	extractor, ok := selected.(dialect.UsageExtractor)
	if !ok {
		return missingUsage(false)
	}
	result, err, panicked := safeExtractUsage(extractor, bytes.Clone(plain))
	if err != nil || panicked || !validCapturedUsage(result) {
		boundary.recordFailure("extract", selected.Protocol())
		return missingUsage(true)
	}
	return result
}
