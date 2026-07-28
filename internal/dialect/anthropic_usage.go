package dialect

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"

	"gpt-load/internal/usage"
)

func (d *Anthropic) ExtractUsage(body []byte) (usage.Result, error) {
	root, err := decodeJSONObject(body)
	if err != nil {
		return usage.Result{}, fmt.Errorf("decode Anthropic usage response")
	}

	usageObject, diagnostics := usageOptionalObject(root, "usage")
	var accumulator usage.Accumulator
	if usageObject == nil {
		if err := accumulator.MergePatch(usage.Patch{Diagnostics: diagnostics}); err != nil {
			return usage.Result{}, fmt.Errorf("normalize Anthropic usage response")
		}
	} else {
		patch, _ := anthropicUsagePatch(usageObject, true, true)
		patch.Diagnostics.Merge(diagnostics)
		if err := accumulator.MergePatch(patch); err != nil {
			return usage.Result{}, fmt.Errorf("normalize Anthropic usage response")
		}
	}
	result, _ := accumulator.Finalize(true)
	return result, nil
}

func (d *Anthropic) NewUsageStreamExtractor() UsageStreamExtractor {
	return &anthropicUsageStreamExtractor{}
}

type anthropicUsageFields uint8

const (
	anthropicUsageUncachedInput anthropicUsageFields = 1 << iota
	anthropicUsageCacheRead
	anthropicUsageCacheWrite5M
	anthropicUsageCacheWrite1H
	anthropicUsageOutput
)

type anthropicCumulativeUsage struct {
	tokens           usage.Tokens
	present          anthropicUsageFields
	detailPresent    anthropicUsageFields
	detailReported   bool
	detailObjectSeen bool
	aggregate        int64
	aggregatePresent bool
}

type anthropicUsageStreamExtractor struct {
	accumulator        usage.Accumulator
	validStart         bool
	stopped            bool
	cacheWriteFallback bool
	trusted            anthropicCumulativeUsage
}

func (e *anthropicUsageStreamExtractor) Observe(payload []byte) error {
	root, err := decodeJSONObject(payload)
	if err != nil {
		if mergeErr := e.mergeDiagnostics(usageDiagnostic(usage.DiagnosticInvalidPayload)); mergeErr != nil {
			return mergeErr
		}
		return fmt.Errorf("decode Anthropic usage stream payload")
	}

	eventType := anthropicEventType(root)
	if e.stopped {
		return e.mergeDiagnostics(usageDiagnostic(usage.DiagnosticInvalidEventSequence))
	}

	switch eventType {
	case "message_start":
		return e.observeStart(root)
	case "message_delta":
		return e.observeDelta(root)
	case "message_stop":
		return e.observeStop()
	default:
		return nil
	}
}

func (e *anthropicUsageStreamExtractor) Finalize() (usage.Result, bool) {
	return e.accumulator.Finalize(true)
}

func (e *anthropicUsageStreamExtractor) observeStart(root map[string]json.RawMessage) error {
	message, diagnostics := usageOptionalObject(root, "message")
	if message == nil {
		if !diagnostics.Has(usage.DiagnosticInvalidNumber) {
			diagnostics.Add(usage.DiagnosticMissingRequiredField)
		}
		return e.mergeDiagnostics(diagnostics)
	}

	usageObject, usageDiagnostics := usageOptionalObject(message, "usage")
	diagnostics.Merge(usageDiagnostics)
	if usageObject == nil {
		if !diagnostics.Has(usage.DiagnosticInvalidNumber) {
			diagnostics.Add(usage.DiagnosticMissingRequiredField)
		}
		return e.mergeDiagnostics(diagnostics)
	}

	next, usageDiagnostics, validInput := anthropicCumulativeUsageFromObject(usageObject, true, false)
	diagnostics.Merge(usageDiagnostics)
	if e.validStart {
		diagnostics.Add(usage.DiagnosticInvalidEventSequence)
		return e.mergeDiagnostics(diagnostics)
	}
	if validInput {
		e.validStart = true
		return e.mergeCumulative(next, diagnostics)
	}
	return e.mergeDiagnostics(diagnostics)
}

func (e *anthropicUsageStreamExtractor) observeDelta(root map[string]json.RawMessage) error {
	usageObject, diagnostics := usageOptionalObject(root, "usage")
	if usageObject == nil {
		if !diagnostics.Has(usage.DiagnosticInvalidNumber) {
			diagnostics.Add(usage.DiagnosticMissingRequiredField)
		}
		return e.mergeDiagnostics(diagnostics)
	}

	next, usageDiagnostics, _ := anthropicCumulativeUsageFromObject(usageObject, false, false)
	diagnostics.Merge(usageDiagnostics)
	if !e.validStart {
		diagnostics.Add(usage.DiagnosticInvalidEventSequence)
		return e.mergeDiagnostics(diagnostics)
	}
	return e.mergeCumulative(next, diagnostics)
}

func (e *anthropicUsageStreamExtractor) observeStop() error {
	e.stopped = true
	if !e.validStart {
		return e.mergeDiagnostics(usageDiagnostic(usage.DiagnosticInvalidEventSequence))
	}
	return e.accumulator.MergePatch(usage.Patch{Final: true})
}

func (e *anthropicUsageStreamExtractor) mergeCumulative(
	next anthropicCumulativeUsage,
	diagnostics usage.Diagnostics,
) error {
	patch := usage.Patch{}
	mergeAnthropicCumulativeField(
		anthropicUsageUncachedInput,
		next.present,
		next.tokens.UncachedInput,
		&e.trusted.present,
		&e.trusted.tokens.UncachedInput,
		&patch.UncachedInput,
		&diagnostics,
	)
	mergeAnthropicCumulativeField(
		anthropicUsageCacheRead,
		next.present,
		next.tokens.CacheRead,
		&e.trusted.present,
		&e.trusted.tokens.CacheRead,
		&patch.CacheRead,
		&diagnostics,
	)

	nextDetails := next.detailPresent & (anthropicUsageCacheWrite5M | anthropicUsageCacheWrite1H)
	if e.cacheWriteFallback && next.detailObjectSeen {
		zero := int64(0)
		e.trusted.tokens.CacheWrite5M = 0
		e.trusted.present &^= anthropicUsageCacheWrite5M
		e.cacheWriteFallback = false
		patch.CacheWrite5M = &zero
	}
	e.trusted.detailReported = e.trusted.detailReported || next.detailReported
	e.trusted.detailObjectSeen = e.trusted.detailObjectSeen || next.detailObjectSeen

	mergeAnthropicCumulativeField(
		anthropicUsageCacheWrite5M,
		next.present,
		next.tokens.CacheWrite5M,
		&e.trusted.present,
		&e.trusted.tokens.CacheWrite5M,
		&patch.CacheWrite5M,
		&diagnostics,
	)
	if nextDetails&anthropicUsageCacheWrite5M != 0 && patch.CacheWrite5M != nil {
		e.trusted.detailPresent |= anthropicUsageCacheWrite5M
	}
	mergeAnthropicCumulativeField(
		anthropicUsageCacheWrite1H,
		next.present,
		next.tokens.CacheWrite1H,
		&e.trusted.present,
		&e.trusted.tokens.CacheWrite1H,
		&patch.CacheWrite1H,
		&diagnostics,
	)
	if nextDetails&anthropicUsageCacheWrite1H != 0 && patch.CacheWrite1H != nil {
		e.trusted.detailPresent |= anthropicUsageCacheWrite1H
	}

	if next.aggregatePresent {
		if e.trusted.aggregatePresent && next.aggregate < e.trusted.aggregate {
			diagnostics.Add(usage.DiagnosticInvalidEventSequence)
		} else {
			e.trusted.aggregate = next.aggregate
			e.trusted.aggregatePresent = true
		}
	}

	if e.trusted.aggregatePresent {
		if !e.trusted.detailReported {
			e.trusted.tokens.CacheWrite5M = e.trusted.aggregate
			e.trusted.present |= anthropicUsageCacheWrite5M
			e.cacheWriteFallback = true
			value := e.trusted.aggregate
			patch.CacheWrite5M = &value
			diagnostics.Add(usage.DiagnosticCacheWriteDefaulted5M)
		} else if e.trusted.detailObjectSeen {
			detailTotal, ok := usage.CheckedAdd(
				anthropicTrustedDetailValue(e.trusted, anthropicUsageCacheWrite5M),
				anthropicTrustedDetailValue(e.trusted, anthropicUsageCacheWrite1H),
			)
			if !ok {
				diagnostics.Add(usage.DiagnosticInvalidNumber)
			} else if e.trusted.aggregate != detailTotal {
				diagnostics.SetTotalDelta(e.trusted.aggregate - detailTotal)
			}
		}
	}

	mergeAnthropicCumulativeField(
		anthropicUsageOutput,
		next.present,
		next.tokens.Output,
		&e.trusted.present,
		&e.trusted.tokens.Output,
		&patch.Output,
		&diagnostics,
	)
	patch.Diagnostics = diagnostics
	return e.accumulator.MergePatch(patch)
}

func anthropicTrustedDetailValue(
	trusted anthropicCumulativeUsage,
	field anthropicUsageFields,
) int64 {
	if trusted.detailPresent&field == 0 {
		return 0
	}
	switch field {
	case anthropicUsageCacheWrite5M:
		return trusted.tokens.CacheWrite5M
	case anthropicUsageCacheWrite1H:
		return trusted.tokens.CacheWrite1H
	default:
		return 0
	}
}

func mergeAnthropicCumulativeField(
	field anthropicUsageFields,
	nextPresent anthropicUsageFields,
	nextValue int64,
	trustedPresent *anthropicUsageFields,
	trustedValue *int64,
	patchValue **int64,
	diagnostics *usage.Diagnostics,
) {
	if nextPresent&field == 0 {
		return
	}
	if *trustedPresent&field != 0 && nextValue < *trustedValue {
		diagnostics.Add(usage.DiagnosticInvalidEventSequence)
		return
	}
	*trustedValue = nextValue
	*trustedPresent |= field
	value := nextValue
	*patchValue = &value
}

func (e *anthropicUsageStreamExtractor) mergeDiagnostics(diagnostics usage.Diagnostics) error {
	return e.accumulator.MergePatch(usage.Patch{Diagnostics: diagnostics})
}

func anthropicUsagePatch(usageObject map[string]json.RawMessage, includeOutput, final bool) (usage.Patch, bool) {
	input, diagnostics := usageInteger(usageObject, "input_tokens", true)
	validInput := input != nil && !diagnostics.Has(usage.DiagnosticInvalidNumber) && !diagnostics.Has(usage.DiagnosticNegativeValue)

	patch := usage.Patch{Final: final, Diagnostics: diagnostics}
	if input != nil {
		patch.UncachedInput = input
	}

	cacheRead, cacheReadDiagnostics := usageInteger(usageObject, "cache_read_input_tokens", false)
	patch.Diagnostics.Merge(cacheReadDiagnostics)
	if cacheRead != nil {
		patch.CacheRead = cacheRead
	}

	aggregate, aggregateDiagnostics := usageInteger(usageObject, "cache_creation_input_tokens", false)
	patch.Diagnostics.Merge(aggregateDiagnostics)
	aggregateValid := aggregate != nil && !aggregateDiagnostics.Has(usage.DiagnosticInvalidNumber) && !aggregateDiagnostics.Has(usage.DiagnosticNegativeValue)
	detailPresent := usageFieldPresent(usageObject, "cache_creation")
	detail, detailDiagnostics := usageOptionalObject(usageObject, "cache_creation")
	patch.Diagnostics.Merge(detailDiagnostics)
	if detail == nil {
		if !detailPresent && aggregateValid {
			patch.CacheWrite5M = aggregate
			patch.CacheWrite1H = usageValueOrZero(nil)
			patch.Diagnostics.Add(usage.DiagnosticCacheWriteDefaulted5M)
		}
	} else {
		write5M, write5MDiagnostics := usageInteger(detail, "ephemeral_5m_input_tokens", false)
		patch.Diagnostics.Merge(write5MDiagnostics)
		write1H, write1HDiagnostics := usageInteger(detail, "ephemeral_1h_input_tokens", false)
		patch.Diagnostics.Merge(write1HDiagnostics)
		if write5M != nil {
			patch.CacheWrite5M = write5M
		}
		if write1H != nil {
			patch.CacheWrite1H = write1H
		}
		if aggregateValid && usageIntegerUsable(write5MDiagnostics) && usageIntegerUsable(write1HDiagnostics) {
			detailTotal, ok := usage.CheckedAdd(usageIntegerValue(write5M), usageIntegerValue(write1H))
			if !ok {
				patch.Diagnostics.Add(usage.DiagnosticInvalidNumber)
			} else if *aggregate != detailTotal {
				patch.Diagnostics.SetTotalDelta(*aggregate - detailTotal)
			}
		}
	}

	if includeOutput {
		output, outputDiagnostics := usageInteger(usageObject, "output_tokens", true)
		patch.Diagnostics.Merge(outputDiagnostics)
		if output != nil {
			patch.Output = output
		}
	}
	patch.Diagnostics.Merge(anthropicUnsupportedBillableDetailDiagnostics(usageObject))
	return patch, validInput
}

func anthropicCumulativeUsageFromObject(
	usageObject map[string]json.RawMessage,
	requireInput bool,
	requireOutput bool,
) (anthropicCumulativeUsage, usage.Diagnostics, bool) {
	var cumulative anthropicCumulativeUsage

	input, diagnostics := usageInteger(usageObject, "input_tokens", requireInput)
	validInput := input != nil && usageIntegerUsable(diagnostics)
	if validInput {
		cumulative.tokens.UncachedInput = *input
		cumulative.present |= anthropicUsageUncachedInput
	}

	cacheRead, cacheReadDiagnostics := usageInteger(usageObject, "cache_read_input_tokens", false)
	diagnostics.Merge(cacheReadDiagnostics)
	if cacheRead != nil && usageIntegerUsable(cacheReadDiagnostics) {
		cumulative.tokens.CacheRead = *cacheRead
		cumulative.present |= anthropicUsageCacheRead
	}

	aggregate, aggregateDiagnostics := usageInteger(usageObject, "cache_creation_input_tokens", false)
	diagnostics.Merge(aggregateDiagnostics)
	aggregateValid := aggregate != nil && usageIntegerUsable(aggregateDiagnostics)
	if aggregateValid {
		cumulative.aggregate = *aggregate
		cumulative.aggregatePresent = true
	}
	cumulative.detailReported = usageFieldPresent(usageObject, "cache_creation")
	detail, detailDiagnostics := usageOptionalObject(usageObject, "cache_creation")
	diagnostics.Merge(detailDiagnostics)
	if detail != nil {
		cumulative.detailObjectSeen = true
		write5M, write5MDiagnostics := usageInteger(detail, "ephemeral_5m_input_tokens", false)
		diagnostics.Merge(write5MDiagnostics)
		write1H, write1HDiagnostics := usageInteger(detail, "ephemeral_1h_input_tokens", false)
		diagnostics.Merge(write1HDiagnostics)
		if write5M != nil && usageIntegerUsable(write5MDiagnostics) {
			cumulative.tokens.CacheWrite5M = *write5M
			cumulative.present |= anthropicUsageCacheWrite5M
			cumulative.detailPresent |= anthropicUsageCacheWrite5M
		}
		if write1H != nil && usageIntegerUsable(write1HDiagnostics) {
			cumulative.tokens.CacheWrite1H = *write1H
			cumulative.present |= anthropicUsageCacheWrite1H
			cumulative.detailPresent |= anthropicUsageCacheWrite1H
		}
	}

	output, outputDiagnostics := usageInteger(usageObject, "output_tokens", requireOutput)
	diagnostics.Merge(outputDiagnostics)
	if output != nil && usageIntegerUsable(outputDiagnostics) {
		cumulative.tokens.Output = *output
		cumulative.present |= anthropicUsageOutput
	}

	diagnostics.Merge(anthropicUnsupportedBillableDetailDiagnostics(usageObject))

	return cumulative, diagnostics, validInput
}

func anthropicUnsupportedBillableDetailDiagnostics(
	usageObject map[string]json.RawMessage,
) usage.Diagnostics {
	var diagnostics usage.Diagnostics
	raw, exists := usageObject["server_tool_use"]
	if !exists {
		return diagnostics
	}
	unsupported, detailDiagnostics := anthropicBillableDetailNonZero(raw)
	diagnostics.Merge(detailDiagnostics)
	if unsupported {
		diagnostics.Add(usage.DiagnosticUnsupportedBillableDetail)
	}
	return diagnostics
}

func anthropicBillableDetailNonZero(raw json.RawMessage) (bool, usage.Diagnostics) {
	if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false, usage.Diagnostics{}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return false, usageDiagnostic(usage.DiagnosticInvalidNumber)
	}
	return anthropicJSONValueNonZero(value)
}

func anthropicJSONValueNonZero(value any) (bool, usage.Diagnostics) {
	var diagnostics usage.Diagnostics
	switch typed := value.(type) {
	case nil:
		return false, diagnostics
	case json.Number:
		number, err := strconv.ParseInt(string(typed), 10, 64)
		if err != nil {
			diagnostics.Add(usage.DiagnosticInvalidNumber)
			return false, diagnostics
		}
		if number < 0 {
			diagnostics.Add(usage.DiagnosticNegativeValue)
			return false, diagnostics
		}
		return number > 0, diagnostics
	case map[string]any:
		nonZero := false
		for _, nested := range typed {
			nestedNonZero, nestedDiagnostics := anthropicJSONValueNonZero(nested)
			diagnostics.Merge(nestedDiagnostics)
			nonZero = nonZero || nestedNonZero
		}
		return nonZero, diagnostics
	case []any:
		nonZero := false
		for _, nested := range typed {
			nestedNonZero, nestedDiagnostics := anthropicJSONValueNonZero(nested)
			diagnostics.Merge(nestedDiagnostics)
			nonZero = nonZero || nestedNonZero
		}
		return nonZero, diagnostics
	default:
		diagnostics.Add(usage.DiagnosticInvalidNumber)
		return false, diagnostics
	}
}

func anthropicEventType(root map[string]json.RawMessage) string {
	raw, exists := root["type"]
	if !exists {
		return ""
	}
	var eventType string
	if err := json.Unmarshal(raw, &eventType); err != nil {
		return ""
	}
	return eventType
}

func usageValueOrZero(value *int64) *int64 {
	zero := int64(0)
	if value != nil {
		zero = *value
	}
	return &zero
}

func usageIntegerUsable(diagnostics usage.Diagnostics) bool {
	return !diagnostics.Has(usage.DiagnosticInvalidNumber) && !diagnostics.Has(usage.DiagnosticNegativeValue)
}

func usageIntegerValue(value *int64) int64 {
	if value == nil {
		return 0
	}
	return *value
}
