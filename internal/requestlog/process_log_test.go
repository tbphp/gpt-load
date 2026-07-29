package requestlog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
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

func TestProjectProcessLogUsesFixedFieldsAndUsageAttribution(t *testing.T) {
	table := compileRequestLogTestPriceTable(t, "actual-model", pricing.Prices{
		UncachedInput: pricing.Price{Value: 1, Set: true},
		CacheRead:     pricing.Price{Value: 2, Set: true},
		CacheWrite5M:  pricing.Price{Value: 3, Set: true},
		CacheWrite1H:  pricing.Price{Value: 4, Set: true},
		Output:        pricing.Price{Value: 5, Set: true},
	})
	event := testEvent("00000000-0000-4000-8000-000000000501")
	event.Protocol = protocol.OpenAI
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
		"event":                 "data_plane_request_completed",
		"request_id":            event.RequestID,
		"completed_at":          event.CompletedAt.UTC().Format(time.RFC3339Nano),
		"status":                "error",
		"status_code":           http.StatusBadGateway,
		"protocol":              "openai",
		"access_key_id":         uint(42),
		"client_model":          "client-model",
		"upstream_model":        "actual-model",
		"group_id":              uint(9),
		"key_id":                uint(10),
		"group_name":            "billed",
		"duration_ms":           int64(25),
		"attempt_count":         2,
		"retry_count":           1,
		"affinity_hit":          false,
		"uncached_input_tokens": int64(1),
		"cache_read_tokens":     int64(2),
		"cache_write_5m_tokens": int64(3),
		"cache_write_1h_tokens": int64(4),
		"output_tokens":         int64(5),
		"usage_state":           "complete",
		"cost_state":            "priced",
		"estimated_cost_usd":    "5.5e-05",
		"error_code":            "upstream_error",
		"error_summary":         "provider failed",
	}
	assertProcessFields(t, fields, want)
}

func TestProjectProcessLogUsesStatusLevelAndStableFieldSet(t *testing.T) {
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
			if got := fields["error_code"]; got != test.wantCode {
				t.Fatalf("error_code = %#v, want %q", got, test.wantCode)
			}
			if got := fields["error_summary"]; got != test.wantSummary {
				t.Fatalf("error_summary = %#v, want %q", got, test.wantSummary)
			}
			assertProcessFieldSet(t, fields)
		})
	}
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
	if fields["group_id"] != uint(0) || fields["key_id"] != uint(0) ||
		fields["group_name"] != "" {
		t.Fatalf(
			"attribution = %#v/%#v/%#v, want zero values",
			fields["group_id"],
			fields["key_id"],
			fields["group_name"],
		)
	}
}

func TestProjectProcessLogDoesNotRoundTinyCostToZero(t *testing.T) {
	table := compileRequestLogTestPriceTable(t, "tiny", pricing.Prices{
		Output: pricing.Price{Value: 0.001, Set: true},
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
	if fields["estimated_cost_usd"] == "" ||
		fields["estimated_cost_usd"] == "0" {
		t.Fatalf("tiny cost = %#v", fields["estimated_cost_usd"])
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
			utils.LogBestEffort(
				logger,
				level,
				fields,
				"Data plane request completed",
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
			for _, allowed := range []string{
				"data_plane_request_completed",
				event.RequestID,
				"safe-client",
				"safe-upstream",
				"safe-group",
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
		UncachedInput: pricing.Price{Value: 2, Set: true},
		CacheRead:     pricing.Price{Value: 3, Set: true},
		CacheWrite5M:  pricing.Price{Value: 4, Set: true},
		CacheWrite1H:  pricing.Price{Value: 5, Set: true},
		Output:        pricing.Price{Value: 6, Set: true},
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
	row := mapEvent(redactor, event, table)

	_, fields, ok := projectProcessLog(redactor, event, table)
	if !ok {
		t.Fatal("projection skipped")
	}
	want := map[string]any{
		"request_id":            row.ID,
		"completed_at":          row.CreatedAt.UTC().Format(time.RFC3339Nano),
		"access_key_id":         row.AccessKeyID,
		"client_model":          row.ClientModel,
		"upstream_model":        row.UpstreamModel,
		"duration_ms":           row.DurationMs,
		"uncached_input_tokens": row.InputTokens,
		"cache_read_tokens":     row.CacheReadTokens,
		"cache_write_5m_tokens": row.CacheWrite5MTokens,
		"cache_write_1h_tokens": row.CacheWrite1HTokens,
		"output_tokens":         row.OutputTokens,
		"usage_state":           row.UsageState,
		"cost_state":            row.CostState,
		"estimated_cost_usd":    "0.00027",
	}
	for field, wantValue := range want {
		if got := fields[field]; !reflect.DeepEqual(got, wantValue) {
			t.Errorf("%s = %#v, want %#v", field, got, wantValue)
		}
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

func assertProcessFieldSet(t *testing.T, fields logrus.Fields) {
	t.Helper()
	names := []string{
		"event",
		"request_id",
		"completed_at",
		"status",
		"status_code",
		"protocol",
		"access_key_id",
		"client_model",
		"upstream_model",
		"group_id",
		"key_id",
		"group_name",
		"duration_ms",
		"attempt_count",
		"retry_count",
		"affinity_hit",
		"uncached_input_tokens",
		"cache_read_tokens",
		"cache_write_5m_tokens",
		"cache_write_1h_tokens",
		"output_tokens",
		"usage_state",
		"cost_state",
		"estimated_cost_usd",
		"error_code",
		"error_summary",
	}
	if len(fields) != len(names) {
		t.Fatalf("field count = %d, want %d: %#v", len(fields), len(names), fields)
	}
	for _, name := range names {
		if _, ok := fields[name]; !ok {
			t.Errorf("missing field %q", name)
		}
	}
}
