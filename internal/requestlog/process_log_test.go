package requestlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/sirupsen/logrus"

	"gpt-load/internal/platform/redact"
	"gpt-load/internal/platform/utils"
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

func TestProjectProcessLogUsesSparseFieldsAndAggregatesCacheWrite(t *testing.T) {
	table := compileRequestLogTestPriceTable(t, "actual-model", pricing.Prices{
		UncachedInput: pricing.Price{NanoUSDPerMillion: 1_000_000_000, Set: true},
		CacheRead:     pricing.Price{NanoUSDPerMillion: 2_000_000_000, Set: true},
		CacheWrite5M:  pricing.Price{NanoUSDPerMillion: 3_000_000_000, Set: true},
		CacheWrite1H:  pricing.Price{NanoUSDPerMillion: 4_000_000_000, Set: true},
		Output:        pricing.Price{NanoUSDPerMillion: 5_000_000_000, Set: true},
	})
	event := testEvent("00000000-0000-4000-8000-000000000501")
	event.Protocol = protocol.OpenAICompletions
	event.UpstreamModel = "actual-model"
	event.Status = telemetry.RequestStatusError
	event.StatusCode = http.StatusBadGateway
	event.ErrorCode = "upstream_error"
	event.ErrorSummary = "provider failed"
	event.Attempts = []telemetry.Attempt{
		{
			Sequence: 1, GroupID: 7, GroupName: "first", KeyID: 8,
			WillRetry: true,
		},
		{
			Sequence: 2, GroupID: 9, GroupName: "billed", KeyID: 10,
		},
	}
	event.Usage = telemetry.UsageObservation{
		GroupID: 9, KeyID: 10, AttemptSequence: 2,
		Result: usage.Result{
			State: usage.StateComplete,
			Tokens: usage.Tokens{
				UncachedInput: 1,
				CacheRead:     2,
				CacheWrite5M:  3,
				CacheWrite1H:  4,
				Output:        5,
			},
		},
	}

	level, fields, ok := projectProcessLog(redact.New(), event, table)
	if !ok || level != logrus.WarnLevel {
		t.Fatalf("projection = %t/%s, want true/warning", ok, level)
	}
	want := logrus.Fields{
		"event":                   "data_plane_request_completed",
		"request_id":              event.RequestID,
		"status":                  "error",
		"status_code":             http.StatusBadGateway,
		"protocol":                string(protocol.OpenAICompletions),
		"access_key_id":           uint(42),
		"client_model":            "client-model",
		"upstream_model":          "actual-model",
		"group_id":                uint(9),
		"key_id":                  uint(10),
		"duration_ms":             int64(25),
		"attempt_count":           2,
		"uncached_input_tokens":   int64(1),
		"cache_read_tokens":       int64(2),
		"cache_write_tokens":      int64(7),
		"output_tokens":           int64(5),
		"estimated_cost_nano_usd": int64(55_000),
		"error_code":              "upstream_error",
		"error_summary":           "provider failed",
	}
	assertProcessFields(t, fields, want)
}

func TestProjectProcessLogUsesStatusLevelAndConditionalDiagnostics(t *testing.T) {
	tests := []struct {
		name        string
		status      telemetry.RequestStatus
		wantLevel   logrus.Level
		wantCode    string
		wantSummary string
	}{
		{
			name: "success", status: telemetry.RequestStatusSuccess,
			wantLevel: logrus.InfoLevel,
		},
		{
			name: "canceled", status: telemetry.RequestStatusCanceled,
			wantLevel: logrus.InfoLevel, wantCode: "client_canceled",
			wantSummary: "client disconnected",
		},
		{
			name: "error", status: telemetry.RequestStatusError,
			wantLevel: logrus.WarnLevel, wantCode: "upstream_error",
			wantSummary: "provider failed",
		},
		{
			name: "incomplete", status: telemetry.RequestStatusIncomplete,
			wantLevel: logrus.WarnLevel, wantCode: "stream_incomplete",
			wantSummary: "stream ended",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := testEvent("status-" + test.name)
			event.Status = test.status
			event.ErrorCode = test.wantCode
			event.ErrorSummary = test.wantSummary

			level, fields, ok := projectProcessLog(redact.New(), event, nil)
			if !ok || level != test.wantLevel {
				t.Fatalf("projection = %t/%s, want true/%s", ok, level, test.wantLevel)
			}
			if test.wantCode == "" {
				if _, exists := fields["error_code"]; exists {
					t.Fatalf("success error_code = %#v, want omitted", fields["error_code"])
				}
				if _, exists := fields["error_summary"]; exists {
					t.Fatalf(
						"success error_summary = %#v, want omitted",
						fields["error_summary"],
					)
				}
			} else {
				if got := fields["error_code"]; got != test.wantCode {
					t.Fatalf("error_code = %#v, want %q", got, test.wantCode)
				}
				if got := fields["error_summary"]; got != test.wantSummary {
					t.Fatalf("error_summary = %#v, want %q", got, test.wantSummary)
				}
			}
		})
	}
}

func TestProjectProcessLogOmitsDefaultAndZeroValueFields(t *testing.T) {
	event := testEvent("sparse-defaults")
	event.Protocol = protocol.Anthropic
	event.UpstreamModel = event.ClientModel

	level, fields, ok := projectProcessLog(redact.New(), event, nil)
	if !ok || level != logrus.InfoLevel {
		t.Fatalf("projection = %t/%s, want true/info", ok, level)
	}
	want := logrus.Fields{
		"event":         "data_plane_request_completed",
		"request_id":    event.RequestID,
		"status":        "success",
		"protocol":      "anthropic",
		"access_key_id": uint(42),
		"client_model":  "client-model",
		"duration_ms":   int64(25),
	}
	assertProcessFields(t, fields, want)
}

func TestProjectProcessLogPreservesNoCandidateDiagnosticsAndOmitsNoise(t *testing.T) {
	event := testEvent("no-candidate")
	event.Protocol = protocol.OpenAICompletions
	event.UpstreamModel = ""
	event.Status = telemetry.RequestStatusError
	event.StatusCode = http.StatusServiceUnavailable
	event.ErrorCode = "no_available_candidate"
	event.ErrorSummary = "No available upstream candidate."
	event.Attempts = nil
	event.Usage = telemetry.UsageObservation{
		Result: usage.Result{State: usage.StateNotApplicable},
	}

	level, fields, ok := projectProcessLog(redact.New(), event, nil)
	if !ok || level != logrus.WarnLevel {
		t.Fatalf("projection = %t/%s, want true/warning", ok, level)
	}
	want := logrus.Fields{
		"event":         "data_plane_request_completed",
		"request_id":    event.RequestID,
		"status":        "error",
		"status_code":   http.StatusServiceUnavailable,
		"protocol":      string(protocol.OpenAICompletions),
		"access_key_id": uint(42),
		"client_model":  "client-model",
		"duration_ms":   int64(25),
		"attempt_count": 0,
		"error_code":    "no_available_candidate",
		"error_summary": "No available upstream candidate.",
	}
	assertProcessFields(t, fields, want)
}

func TestProjectProcessLogSkipsEmptyRequestID(t *testing.T) {
	_, _, ok := projectProcessLog(redact.New(), telemetry.RequestEvent{}, nil)
	if ok {
		t.Fatal("empty request ID produced a process event")
	}
}

func TestProjectProcessLogDoesNotGuessMissingUsageAttribution(t *testing.T) {
	event := testEvent("no-attribution")
	event.Usage = telemetry.UsageObservation{
		GroupID: 7,
		KeyID:   8,
		Result:  usage.Result{State: usage.StateNotApplicable},
	}

	level, fields, ok := projectProcessLog(redact.New(), event, nil)
	if !ok || level != logrus.InfoLevel {
		t.Fatalf("projection = %t/%s, want true/info", ok, level)
	}
	for _, name := range []string{"group_id", "key_id", "group_name"} {
		if value, exists := fields[name]; exists {
			t.Fatalf("%s = %#v, want omitted", name, value)
		}
	}
}

func TestProjectProcessLogAggregatesCacheWriteBuckets(t *testing.T) {
	tests := []struct {
		name       string
		write5M    int64
		write1H    int64
		want       int64
		wantExists bool
	}{
		{name: "none"},
		{name: "five minute", write5M: 3, want: 3, wantExists: true},
		{name: "one hour", write1H: 4, want: 4, wantExists: true},
		{
			name:       "both",
			write5M:    3,
			write1H:    4,
			want:       7,
			wantExists: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := testEvent("cache-write-" + test.name)
			event.Usage.Result = usage.Result{
				State: usage.StateComplete,
				Tokens: usage.Tokens{
					CacheWrite5M: test.write5M,
					CacheWrite1H: test.write1H,
				},
			}

			_, fields, ok := projectProcessLog(redact.New(), event, nil)
			if !ok {
				t.Fatal("projection skipped")
			}
			got, exists := fields["cache_write_tokens"]
			if exists != test.wantExists {
				t.Fatalf(
					"cache_write_tokens exists = %t, want %t: %#v",
					exists,
					test.wantExists,
					fields,
				)
			}
			if test.wantExists && got != test.want {
				t.Fatalf("cache_write_tokens = %#v, want %d", got, test.want)
			}
			for _, legacy := range []string{
				"cache_write_5m_tokens",
				"cache_write_1h_tokens",
			} {
				if value, exists := fields[legacy]; exists {
					t.Fatalf("%s = %#v, want omitted", legacy, value)
				}
			}
		})
	}
}

func TestProjectProcessLogOmitsOverflowedCacheWriteAggregate(t *testing.T) {
	table := compileRequestLogTestPriceTable(t, "overflow-model", pricing.Prices{
		UncachedInput: pricing.Price{Set: true},
		CacheRead:     pricing.Price{Set: true},
		CacheWrite5M:  pricing.Price{Set: true},
		CacheWrite1H:  pricing.Price{Set: true},
		Output:        pricing.Price{Set: true},
	})

	t.Run("adjacent safe total remains priced", func(t *testing.T) {
		event := testEvent("cache-write-safe-boundary")
		event.UpstreamModel = "overflow-model"
		event.Usage.Result = usage.Result{
			State: usage.StateComplete,
			Tokens: usage.Tokens{
				CacheWrite5M: math.MaxInt64,
			},
		}

		_, fields, ok := projectProcessLog(redact.New(), event, table)
		if !ok {
			t.Fatal("projection skipped")
		}
		if fields["cache_write_tokens"] != int64(math.MaxInt64) {
			t.Fatalf(
				"cache_write_tokens = %#v, want %d",
				fields["cache_write_tokens"],
				int64(math.MaxInt64),
			)
		}
		if value, exists := fields["cost_state"]; exists {
			t.Fatalf("cost_state = %#v, want omitted for priced usage", value)
		}
		if fields["estimated_cost_nano_usd"] != int64(0) {
			t.Fatalf(
				"estimated_cost_nano_usd = %#v, want 0",
				fields["estimated_cost_nano_usd"],
			)
		}
	})

	t.Run("overflow omits aggregate and becomes unpriced", func(t *testing.T) {
		event := testEvent("cache-write-overflow")
		event.UpstreamModel = "overflow-model"
		event.Usage.Result = usage.Result{
			State: usage.StateComplete,
			Tokens: usage.Tokens{
				CacheWrite5M: math.MaxInt64,
				CacheWrite1H: 1,
			},
		}

		_, fields, ok := projectProcessLog(redact.New(), event, table)
		if !ok {
			t.Fatal("projection skipped")
		}
		if value, exists := fields["cache_write_tokens"]; exists {
			t.Fatalf("cache_write_tokens = %#v, want omitted", value)
		}
		if fields["cost_state"] != "unpriced" {
			t.Fatalf("cost_state = %#v, want unpriced", fields["cost_state"])
		}
		if value, exists := fields["estimated_cost_nano_usd"]; exists {
			t.Fatalf("estimated_cost_nano_usd = %#v, want omitted", value)
		}
	})
}

func TestProjectProcessLogUsesConditionalModelAttemptAndStateFields(t *testing.T) {
	t.Run("same model and normal attempt are omitted", func(t *testing.T) {
		event := testEvent("normal-model-attempt")
		event.UpstreamModel = event.ClientModel
		event.Usage.Result.State = usage.StateNotApplicable

		_, fields, ok := projectProcessLog(redact.New(), event, nil)
		if !ok {
			t.Fatal("projection skipped")
		}
		for _, name := range []string{
			"upstream_model",
			"attempt_count",
			"usage_state",
			"cost_state",
		} {
			if value, exists := fields[name]; exists {
				t.Fatalf("%s = %#v, want omitted", name, value)
			}
		}
	})

	t.Run("zero attempts and abnormal states remain visible", func(t *testing.T) {
		event := testEvent("abnormal-state")
		event.Attempts = nil
		event.Usage.Result.State = usage.StatePartial

		_, fields, ok := projectProcessLog(redact.New(), event, nil)
		if !ok {
			t.Fatal("projection skipped")
		}
		if fields["upstream_model"] != "upstream-model" ||
			fields["attempt_count"] != 0 ||
			fields["usage_state"] != "partial" ||
			fields["cost_state"] != "unpriced" {
			t.Fatalf("conditional fields = %#v", fields)
		}
	})
}

func TestProjectProcessLogDoesNotRoundTinyCostToZero(t *testing.T) {
	table := compileRequestLogTestPriceTable(t, "tiny", pricing.Prices{
		Output: pricing.Price{NanoUSDPerMillion: 1_000_000, Set: true},
	})
	event := testEvent("tiny-cost")
	event.UpstreamModel = "tiny"
	event.Usage.Result = usage.Result{
		State:  usage.StateComplete,
		Tokens: usage.Tokens{Output: 1},
	}

	_, fields, ok := projectProcessLog(redact.New(), event, table)
	if !ok {
		t.Fatal("projection skipped")
	}
	if fields["estimated_cost_nano_usd"] != int64(1) {
		t.Fatalf("tiny cost = %#v, want 1 nano USD", fields["estimated_cost_nano_usd"])
	}
}

func TestProjectProcessLogBoundsAndRedactsErrorSummary(t *testing.T) {
	event := testEvent("summary-contract")
	event.Status = telemetry.RequestStatusError
	event.ErrorCode = "upstream_error"
	event.ErrorSummary = string([]byte{0xff}) +
		"\n sk-process-summary-secret " +
		strings.Repeat("界", 100)

	_, fields, ok := projectProcessLog(redact.New(), event, nil)
	if !ok {
		t.Fatal("projection skipped")
	}
	summary, ok := fields["error_summary"].(string)
	if !ok {
		t.Fatalf("error_summary type = %T", fields["error_summary"])
	}
	if !utf8.ValidString(summary) || strings.Contains(summary, "\n") ||
		strings.Contains(summary, "sk-process-summary-secret") ||
		len(summary) > maxProcessSummaryBytes ||
		!strings.HasSuffix(summary, truncatedMarker) {
		t.Fatalf("unsafe bounded summary = %q (%d bytes)", summary, len(summary))
	}
}

func TestProcessLogFormatterSecretMatrix(t *testing.T) {
	const (
		accessKey      = "gl-client-access-secret-0002"
		headerSecret   = "sk-header-rule-secret-0008"
		requestSecret  = "sk-request-body-secret-0009"
		responseSecret = "sk-response-summary-secret-0010"
	)
	event := testEvent("00000000-0000-4000-8000-000000000509")
	event.Status = telemetry.RequestStatusError
	event.StatusCode = http.StatusBadGateway
	event.ClientModel = "safe-client/" + accessKey
	event.UpstreamModel = "safe-upstream/" + requestSecret
	event.ErrorCode = "upstream_error"
	event.ErrorSummary = "safe-summary " + responseSecret
	event.Attempts = []telemetry.Attempt{{
		Sequence:  1,
		GroupID:   71,
		GroupName: "safe-group/" + headerSecret,
		KeyID:     81,
		WillRetry: false,
	}}
	event.Usage = telemetry.UsageObservation{
		GroupID: 71, KeyID: 81, AttemptSequence: 1,
		Result: usage.Result{State: usage.StateNotApplicable},
	}

	redactor := redact.New()
	level, fields, ok := projectProcessLog(redactor, event, nil)
	if !ok || level != logrus.WarnLevel {
		t.Fatalf("projection = %t/%s, want true/warning", ok, level)
	}
	projected := fmt.Sprint(fields)
	for _, forbidden := range []string{
		accessKey,
		headerSecret,
		requestSecret,
		responseSecret,
	} {
		if strings.Contains(projected, forbidden) {
			t.Fatalf(
				"projection contains %q before runtime hook: %s",
				forbidden,
				projected,
			)
		}
	}

	formatters := map[string]logrus.Formatter{
		"text": &logrus.TextFormatter{
			DisableTimestamp: true,
			DisableColors:    true,
		},
		"json": &logrus.JSONFormatter{DisableTimestamp: true},
	}
	for name, formatter := range formatters {
		t.Run(name, func(t *testing.T) {
			var output bytes.Buffer
			logger := logrus.New()
			logger.SetOutput(&output)
			logger.SetFormatter(formatter)
			logger.AddHook(redact.NewHook(redactor))
			utils.LogPlaneBestEffort(
				logger,
				level,
				utils.LogPlaneData,
				fields,
				"Request completed",
			)
			logText := output.String()
			for _, forbidden := range []string{
				accessKey,
				headerSecret,
				requestSecret,
				responseSecret,
			} {
				if strings.Contains(logText, forbidden) {
					t.Fatalf(
						"%s log contains %q: %s",
						name,
						forbidden,
						logText,
					)
				}
			}
			planeMarker := "plane=data"
			if name == "json" {
				planeMarker = `"plane":"data"`
			}
			for _, allowed := range []string{
				"data_plane_request_completed",
				planeMarker,
				"[DATA] Request completed",
				event.RequestID,
				"safe-client",
				"safe-upstream",
			} {
				if !strings.Contains(logText, allowed) {
					t.Fatalf(
						"%s log missing %q: %s",
						name,
						allowed,
						logText,
					)
				}
			}
		})
	}
}

func TestProjectProcessLogMatchesDurableProjection(t *testing.T) {
	table := compileRequestLogTestPriceTable(t, "consistent", pricing.Prices{
		UncachedInput: pricing.Price{NanoUSDPerMillion: 2_000_000_000, Set: true},
		CacheRead:     pricing.Price{NanoUSDPerMillion: 3_000_000_000, Set: true},
		CacheWrite5M:  pricing.Price{NanoUSDPerMillion: 4_000_000_000, Set: true},
		CacheWrite1H:  pricing.Price{NanoUSDPerMillion: 5_000_000_000, Set: true},
		Output:        pricing.Price{NanoUSDPerMillion: 6_000_000_000, Set: true},
	})
	event := testEvent("projection-consistency")
	event.Protocol = protocol.Anthropic
	event.ClientModel = strings.Repeat("客", 100)
	event.UpstreamModel = "consistent"
	event.Usage = telemetry.UsageObservation{
		GroupID: 7, KeyID: 8, AttemptSequence: 1,
		Result: usage.Result{
			State: usage.StateComplete,
			Tokens: usage.Tokens{
				UncachedInput: 11,
				CacheRead:     12,
				CacheWrite5M:  13,
				CacheWrite1H:  14,
				Output:        15,
			},
		},
	}
	redactor := redact.New()
	row := mustMapEvent(t, redactor, event, table)

	_, fields, ok := projectProcessLog(redactor, event, table)
	if !ok {
		t.Fatal("projection skipped")
	}
	want := map[string]any{
		"request_id":              row.ID,
		"access_key_id":           row.AccessKeyID,
		"client_model":            row.ClientModel,
		"upstream_model":          row.UpstreamModel,
		"duration_ms":             row.DurationMs,
		"uncached_input_tokens":   row.UncachedInputTokens,
		"cache_read_tokens":       row.CacheReadTokens,
		"cache_write_tokens":      int64(27),
		"output_tokens":           row.OutputTokens,
		"estimated_cost_nano_usd": row.EstimatedCostNanoUSD,
	}
	for field, wantValue := range want {
		if got := fields[field]; !reflect.DeepEqual(got, wantValue) {
			t.Errorf("%s = %#v, want %#v", field, got, wantValue)
		}
	}
}

func TestProcessAndDurableProjectionsRedactSensitiveIdentityFieldsConsistently(
	t *testing.T,
) {
	event := testEvent("projection-sensitive-identity")
	event.ClientModel = "client/sk-client-model-secret-0001"
	event.UpstreamModel = "upstream/sk-upstream-model-secret-0002"
	event.Attempts = []telemetry.Attempt{{
		Sequence:      3,
		GroupID:       11,
		GroupName:     "group/sk-group-name-secret-0003",
		KeyID:         12,
		UpstreamModel: "attempt/sk-attempt-model-secret-0004",
	}}
	event.Usage = telemetry.UsageObservation{
		GroupID: 11, KeyID: 12, AttemptSequence: 3,
		Result: usage.Result{State: usage.StateNotApplicable},
	}

	redactor := redact.New()
	row := mustMapEvent(t, redactor, event, nil)
	var attempts []Attempt
	if err := json.Unmarshal(row.Attempts, &attempts); err != nil {
		t.Fatalf("unmarshal durable attempts: %v", err)
	}
	if row.ClientModel != "client/[REDACTED]" ||
		row.UpstreamModel != "upstream/[REDACTED]" ||
		len(attempts) != 1 ||
		attempts[0].GroupName != "group/[REDACTED]" ||
		attempts[0].UpstreamModel != "attempt/[REDACTED]" {
		t.Fatalf(
			"durable sensitive identity projection = %q/%q/%+v",
			row.ClientModel,
			row.UpstreamModel,
			attempts,
		)
	}

	_, fields, ok := projectProcessLog(redactor, event, nil)
	if !ok {
		t.Fatal("process projection skipped")
	}
	if fields["client_model"] != row.ClientModel ||
		fields["upstream_model"] != row.UpstreamModel {
		t.Fatalf(
			"process/durable identity projection mismatch: fields=%#v row=%+v attempts=%+v",
			fields,
			row,
			attempts,
		)
	}
	if value, exists := fields["group_name"]; exists {
		t.Fatalf("group_name = %#v, want omitted", value)
	}
}

func assertProcessFields(
	t *testing.T,
	got logrus.Fields,
	want logrus.Fields,
) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields = %#v, want %#v", got, want)
	}
}
