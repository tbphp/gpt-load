package gateway

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"gpt-load/internal/dialect"
)

const (
	maxObservedResponseModelBytes = 255
	truncatedResponseModelMarker  = "...[truncated]"
)

type responseModelObservation struct {
	reported string
	observed bool
	mismatch bool
}

type responseModelTracker struct {
	inspector dialect.ResponseModelInspector
	expected  string
	result    responseModelObservation
}

func newResponseModelTracker(selected dialect.Dialect, expected string) *responseModelTracker {
	inspector, _ := selected.(dialect.ResponseModelInspector)
	return &responseModelTracker{inspector: inspector, expected: expected}
}

func (tracker *responseModelTracker) observe(payload []byte) {
	if tracker == nil || tracker.inspector == nil {
		return
	}
	for _, model := range safeInspectResponseModels(tracker.inspector, payload) {
		if strings.TrimSpace(model) == "" {
			continue
		}
		mismatch := model != tracker.expected
		reported := projectObservedResponseModel(model, tracker.expected, mismatch)
		tracker.result.observed = true
		if mismatch {
			tracker.result.mismatch = true
			tracker.result.reported = reported
			continue
		}
		if !tracker.result.mismatch {
			tracker.result.reported = reported
		}
	}
}

func projectObservedResponseModel(model string, expected string, mismatch bool) string {
	projected := strings.ToValidUTF8(model, "\uFFFD")
	if len(projected) > maxObservedResponseModelBytes {
		prefixBytes := maxObservedResponseModelBytes - len(truncatedResponseModelMarker)
		for prefixBytes > 0 && !utf8.ValidString(projected[:prefixBytes]) {
			prefixBytes--
		}
		projected = projected[:prefixBytes] + truncatedResponseModelMarker
	}

	// The consistency state is based on the full upstream values. Keep the
	// bounded display value distinct if truncation happens to equal expected.
	if mismatch && projected == expected {
		projected = truncatedResponseModelMarker
		if projected == expected {
			projected += "!"
		}
	}
	return projected
}

func (tracker *responseModelTracker) observation() responseModelObservation {
	if tracker == nil {
		return responseModelObservation{}
	}
	return tracker.result
}

func safeInspectResponseModels(
	inspector dialect.ResponseModelInspector,
	payload []byte,
) (models []string) {
	defer func() {
		if recover() != nil {
			models = nil
		}
	}()
	return inspector.InspectResponseModels(bytes.Clone(payload))
}

func applyResponseModelObservation(result *UpstreamResult, observation responseModelObservation) {
	if result == nil {
		return
	}
	result.UpstreamReportedModel = observation.reported
	result.ResponseModelObserved = observation.observed
	result.ResponseModelMismatch = observation.mismatch
}
