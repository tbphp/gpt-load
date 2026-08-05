package requestlog

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func newRequestLogJSONLogger(output io.Writer) *logrus.Logger {
	logger := logrus.New()
	logger.SetOutput(output)
	logger.SetLevel(logrus.DebugLevel)
	logger.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
	return logger
}

func processLogEntries(t *testing.T, output []byte) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range bytes.Split(bytes.TrimSpace(output), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		if entry["event"] == "data_plane_request_completed" {
			entries = append(entries, entry)
		}
	}
	return entries
}

func TestProjectProcessLogUsesFrozenPricingAndInputOutputTotals(t *testing.T) {
	event := testEvent("00000000-0000-4000-8000-000000000501")
	event.Protocol = protocol.OpenAICompletions
	event.Status = telemetry.RequestStatusError
	event.StatusCode = http.StatusBadGateway
	event.ErrorCode = "upstream_error"
	event.ErrorSummary = "provider failed"
	event.DurationMs = 58_773
	event.Usage.AttemptSequence = 1
	event.Usage.KeyID = 8
	event.Usage.Result = usage.Result{
		State: usage.StatePartial,
		Tokens: usage.Tokens{
			UncachedInput: 1, CacheRead: 2, CacheWrite5M: 3,
			CacheWrite1H: 4, CacheWriteUnknown: 5, Output: 6,
		},
	}
	event.Usage.Pricing = telemetry.PricingObservation{
		PriceScopeKey: "group:7", UpstreamModel: event.UpstreamModel,
		CostState: string(pricing.CostStatePriced), PricingCompleteness: string(pricing.CompletenessPartial),
		EstimatedCostNanoUSD: 55_000,
	}

	level, fields, ok := projectProcessLog(redact.New(), event)
	if !ok || level != logrus.WarnLevel {
		t.Fatalf("projection = %t/%s", ok, level)
	}
	want := logrus.Fields{
		"event":  "data_plane_request_completed",
		"status": "error", "http": http.StatusBadGateway,
		"proto": string(protocol.OpenAICompletions), "ak_id": uint(42),
		"model": "client-model", "up_model": "upstream-model",
		"group": uint(7), "kid": uint(8), "duration": "58.8s",
		"in_tokens": int64(15), "out_tokens": int64(6),
		"usage": "partial", "cost_usd": "0.000055",
		"err": "upstream_error", "err_msg": "provider failed",
	}
	assertProcessFields(t, fields, want)
}

func TestProjectProcessLogUsesStatusLevelAndConditionalDiagnostics(t *testing.T) {
	tests := []struct {
		status telemetry.RequestStatus
		level  logrus.Level
	}{
		{telemetry.RequestStatusSuccess, logrus.InfoLevel},
		{telemetry.RequestStatusCanceled, logrus.InfoLevel},
		{telemetry.RequestStatusError, logrus.WarnLevel},
		{telemetry.RequestStatusIncomplete, logrus.WarnLevel},
	}
	for _, test := range tests {
		event := testEvent("status-" + string(test.status))
		event.Status = test.status
		if test.status != telemetry.RequestStatusSuccess {
			event.ErrorCode = "failure"
			event.ErrorSummary = "failed"
		}
		level, fields, ok := projectProcessLog(redact.New(), event)
		if !ok || level != test.level {
			t.Fatalf("status %q projection = %t/%s", test.status, ok, level)
		}
		_, hasCode := fields["err"]
		if hasCode != (test.status != telemetry.RequestStatusSuccess) {
			t.Fatalf("status %q error_code presence = %t", test.status, hasCode)
		}
	}
}

func TestProjectProcessLogOmitsDefaultAndZeroValueFields(t *testing.T) {
	event := testEvent("sparse-defaults")
	event.Protocol = protocol.Anthropic
	event.UpstreamModel = event.ClientModel
	event.Attempts[0].UpstreamModel = event.ClientModel
	event.Usage.Pricing.UpstreamModel = event.ClientModel
	level, fields, ok := projectProcessLog(redact.New(), event)
	if !ok || level != logrus.InfoLevel {
		t.Fatalf("projection = %t/%s", ok, level)
	}
	want := logrus.Fields{
		"event":  "data_plane_request_completed",
		"status": "success", "proto": "anthropic", "ak_id": uint(42),
		"model": "client-model", "group": uint(7), "kid": uint(8),
		"duration": "25ms",
	}
	assertProcessFields(t, fields, want)
}

func TestProjectProcessLogPreservesNoCandidateDiagnostics(t *testing.T) {
	event := telemetry.RequestEvent{
		RequestID: "no-candidate", CompletedAt: testEvent("x").CompletedAt,
		Protocol: protocol.OpenAICompletions, AccessKeyID: 42, ClientModel: "client-model",
		Status: telemetry.RequestStatusError, StatusCode: http.StatusServiceUnavailable,
		ErrorCode: "no_available_candidate", ErrorSummary: "No available upstream candidate.", DurationMs: 25,
		Usage: telemetry.UsageObservation{
			Result:  usage.Result{State: usage.StateNotApplicable},
			Pricing: telemetry.PricingObservation{CostState: string(pricing.CostStateNotApplicable), PricingCompleteness: string(pricing.CompletenessNotApplicable)},
		},
	}
	level, fields, ok := projectProcessLog(redact.New(), event)
	if !ok || level != logrus.WarnLevel || fields["attempts"] != 0 ||
		fields["err"] != "no_available_candidate" {
		t.Fatalf("no-candidate fields = %#v", fields)
	}
}

func TestProjectProcessLogSkipsInvalidProjection(t *testing.T) {
	if _, _, ok := projectProcessLog(redact.New(), telemetry.RequestEvent{}); ok {
		t.Fatal("empty request ID produced a process event")
	}
	event := testEvent("overflow")
	event.Usage.Result.Tokens.CacheWrite5M = math.MaxInt64
	event.Usage.Result.Tokens.CacheWrite1H = 1
	if _, _, ok := projectProcessLog(redact.New(), event); ok {
		t.Fatal("overflowing token observation produced a process event")
	}
}

func TestProjectProcessLogOmitsMissingAttribution(t *testing.T) {
	event := telemetry.RequestEvent{
		RequestID: "resource", CompletedAt: testEvent("x").CompletedAt,
		Status: telemetry.RequestStatusSuccess,
		Usage: telemetry.UsageObservation{
			Result:  usage.Result{State: usage.StateNotApplicable},
			Pricing: telemetry.PricingObservation{CostState: string(pricing.CostStateNotApplicable), PricingCompleteness: string(pricing.CompletenessNotApplicable)},
		},
	}
	_, fields, ok := projectProcessLog(redact.New(), event)
	if !ok {
		t.Fatal("projection skipped")
	}
	for _, name := range []string{"group", "kid", "group_name", "up_model"} {
		if value, exists := fields[name]; exists {
			t.Fatalf("%s = %#v, want omitted", name, value)
		}
	}
}

func TestProjectProcessLogKeepsKeyIDVisibleAfterRedaction(t *testing.T) {
	event := testEvent("key-id-visible")
	_, fields, ok := projectProcessLog(redact.New(), event)
	if !ok {
		t.Fatal("projection skipped")
	}

	entry := logrus.NewEntry(logrus.New())
	entry.Data = fields
	if err := redact.NewHook(redact.New()).Fire(entry); err != nil {
		t.Fatalf("redaction hook error = %v", err)
	}
	if got := entry.Data["kid"]; got != uint(8) {
		t.Fatalf("kid = %#v, want 8", got)
	}
}

func TestProjectProcessLogRedactsIdentityAndSummary(t *testing.T) {
	const secret = "sk-this-is-a-secret-value"
	event := testEvent("redacted")
	event.ClientModel = secret
	event.ErrorSummary = secret + strings.Repeat("x", maxProcessSummaryBytes)
	event.Status = telemetry.RequestStatusError
	_, fields, ok := projectProcessLog(redact.New(), event)
	if !ok {
		t.Fatal("projection skipped")
	}
	encoded, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encoded, []byte(secret)) {
		t.Fatalf("process fields contain secret: %s", encoded)
	}
}

func assertProcessFields(t *testing.T, got, want logrus.Fields) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.Marshal(got)
		wantJSON, _ := json.Marshal(want)
		t.Fatalf("fields = %s\nwant = %s", gotJSON, wantJSON)
	}
}
