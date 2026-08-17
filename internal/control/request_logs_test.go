package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestRequestLogReadsPreserveRequestCancellation(t *testing.T) {
	fixture := newServiceFixture(t)
	reader := &recordingRequestLogReader{err: context.Canceled, getErr: context.Canceled}
	fixture.service.requestLogs = reader
	requestCtx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := fixture.service.ListRequestLogs(requestCtx, requestlog.ListQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListRequestLogs() error = %v, want context.Canceled", err)
	}
	if _, err := fixture.service.GetRequestLog(requestCtx, "request-id"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetRequestLog() error = %v, want context.Canceled", err)
	}
}

func TestListRequestLogsSuppressesOnlyCanceledHTTPRequestErrors(t *testing.T) {
	initControlI18n(t)
	tests := []struct {
		name       string
		requestCtx func() context.Context
		readerErr  error
		wantError  bool
	}{
		{
			name: "canceled request",
			requestCtx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			readerErr: context.Canceled,
		},
		{
			name: "deadline exceeded",
			requestCtx: func() context.Context {
				ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				cancel()
				return ctx
			},
			readerErr: context.DeadlineExceeded,
			wantError: true,
		},
		{
			name: "database failure after request cancellation",
			requestCtx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			readerErr: errors.New("database unavailable"),
			wantError: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newServiceFixture(t)
			fixture.service.requestLogs = &recordingRequestLogReader{err: test.readerErr}
			server := NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service)
			recorder := httptest.NewRecorder()
			ginContext, _ := gin.CreateTestContext(recorder)
			ginContext.Request = httptest.NewRequest(http.MethodGet, "/api/logs", nil).
				WithContext(test.requestCtx())

			var logs bytes.Buffer
			logger := logrus.StandardLogger()
			previousOutput, previousFormatter := logger.Out, logger.Formatter
			logrus.SetOutput(&logs)
			logrus.SetFormatter(&logrus.JSONFormatter{DisableTimestamp: true})
			t.Cleanup(func() {
				logrus.SetOutput(previousOutput)
				logrus.SetFormatter(previousFormatter)
			})

			server.handleListRequestLogs(ginContext)
			loggedFailure := strings.Contains(logs.String(), "[CONTROL] Operation failed")
			if test.wantError {
				if recorder.Body.Len() == 0 || !loggedFailure {
					t.Fatalf("error response/log = %q / %q", recorder.Body.String(), logs.String())
				}
				return
			}
			if recorder.Body.Len() != 0 || loggedFailure {
				t.Fatalf("canceled response/log = %q / %q", recorder.Body.String(), logs.String())
			}
		})
	}
}

func TestRequestLogEndpointRejectsUnknownDuplicateAndMalformedQueries(t *testing.T) {
	validCursor := encodeTestCursorPayload(
		`{"v":2,"completed_at_ms":1784894400000,"request_id":"00000000-0000-4000-8000-000000000001"}`,
	)
	tests := []struct {
		name  string
		query string
	}{
		{name: "unknown", query: "unknown=value"},
		{name: "duplicate", query: "limit=1&limit=2"},
		{name: "number", query: "limit=not-a-number"},
		{name: "old time query", query: "from=1784894400000"},
		{name: "ambiguous legacy model", query: "model=client-model"},
		{name: "time", query: "from_ms=not-a-time"},
		{name: "time leading zero", query: "from_ms=01784894400000"},
		{name: "time plus", query: "from_ms=%2B1784894400000"},
		{name: "time negative", query: "from_ms=-1"},
		{name: "time unsafe", query: "from_ms=9007199254740992"},
		{name: "group", query: "group_id=-1"},
		{name: "group leading zero", query: "group_id=01"},
		{name: "group plus", query: "group_id=%2B1"},
		{name: "group whitespace", query: "group_id=%201"},
		{name: "group duplicate", query: "group_id=1&group_id=2"},
		{name: "access key", query: "access_key_id=1.5"},
		{name: "access key leading zero", query: "access_key_id=01"},
		{name: "access key plus", query: "access_key_id=%2B1"},
		{name: "access key whitespace", query: "access_key_id=%201"},
		{name: "access key duplicate", query: "access_key_id=1&access_key_id=2"},
		{name: "legacy upstream key", query: "upstream_key_id=9"},
		{name: "legacy key", query: "key_id=9"},
		{name: "limit leading zero", query: "limit=01"},
		{name: "limit plus", query: "limit=%2B1"},
		{name: "limit whitespace", query: "limit=%201"},
		{name: "request UUID uppercase", query: "request_id=00000000-0000-4000-8000-00000000ABCD"},
		{name: "request UUID version", query: "request_id=00000000-0000-3000-8000-000000000001"},
		{name: "cursor base64", query: "cursor=%25%25%25"},
		{name: "cursor percent encoded newline", query: "cursor=" + validCursor + "%0A"},
		{name: "cursor JSON", query: "cursor=" + encodeTestCursorPayload(`{"v":1`)},
		{name: "cursor unknown field", query: "cursor=" + encodeTestCursorPayload(
			`{"v":2,"completed_at_ms":1784894400000,"request_id":"00000000-0000-4000-8000-000000000001","extra":true}`,
		)},
		{name: "cursor old version", query: "cursor=" + encodeTestCursorPayload(
			`{"v":1,"completed_at":"2026-07-24T12:00:00Z","request_id":"00000000-0000-4000-8000-000000000001"}`,
		)},
		{name: "cursor unsafe timestamp", query: "cursor=" + encodeTestCursorPayload(
			`{"v":2,"completed_at_ms":9007199254740992,"request_id":"00000000-0000-4000-8000-000000000001"}`,
		)},
		{name: "cursor UUID", query: "cursor=" + encodeTestCursorPayload(
			`{"v":2,"completed_at_ms":1784894400000,"request_id":"00000000-0000-3000-8000-000000000001"}`,
		)},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &recordingRequestLogReader{}
			engine := newRequestLogTestEngine(t, reader)
			recorder := performRequestLogRequest(engine, "test-auth-key", test.query)
			assertRequestLogErrorCode(t, recorder, "BAD_REQUEST")
			if len(reader.queries) != 0 {
				t.Fatalf("Reader calls = %d, want zero", len(reader.queries))
			}
		})
	}
}

func TestRequestLogEndpointRejectsInvalidDomainValues(t *testing.T) {
	equalTime := "1784894400000"
	tests := []struct {
		name  string
		query string
	}{
		{name: "limit zero", query: "limit=0"},
		{name: "limit above maximum", query: "limit=201"},
		{name: "group zero", query: "group_id=0"},
		{name: "group unsafe", query: "group_id=9007199254740992"},
		{name: "access key zero", query: "access_key_id=0"},
		{name: "access key unsafe", query: "access_key_id=9007199254740992"},
		{name: "unknown channel", query: "channel_id=unknown"},
		{name: "credential zero", query: "credential_id=0"},
		{name: "credential unsafe", query: "credential_id=9007199254740992"},
		{name: "empty client model", query: "client_model="},
		{name: "empty upstream model", query: "upstream_model="},
		{name: "equal range", query: "from_ms=" + equalTime + "&to_ms=" + equalTime},
		{name: "reversed range", query: "from_ms=1784898000000&to_ms=1784894400000"},
		{name: "unknown status", query: "status=unknown"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader := &recordingRequestLogReader{}
			engine := newRequestLogTestEngine(t, reader)
			recorder := performRequestLogRequest(engine, "test-auth-key", test.query)
			assertRequestLogErrorCode(t, recorder, "VALIDATION_FAILED")
			if len(reader.queries) != 0 {
				t.Fatalf("Reader calls = %d, want zero", len(reader.queries))
			}
		})
	}
}

func TestRequestLogEndpointAcceptsCanonicalNumericBoundaries(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("maximum JavaScript safe integer does not fit uint on this architecture")
	}
	reader := &recordingRequestLogReader{}
	engine := newRequestLogTestEngine(t, reader)
	recorder := performRequestLogRequest(
		engine,
		"test-auth-key",
		"group_id=9007199254740991&access_key_id=9007199254740991&limit=200",
	)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 || reader.queries[0].GroupID == nil ||
		reader.queries[0].AccessKeyID == nil ||
		uint64(*reader.queries[0].GroupID) != uint64(maxSafeInteger) ||
		uint64(*reader.queries[0].AccessKeyID) != uint64(maxSafeInteger) ||
		reader.queries[0].Limit != maxRequestLogLimit {
		t.Fatalf("List() queries = %#v", reader.queries)
	}
}

func TestRequestLogEndpointParsesAdvancedFilters(t *testing.T) {
	reader := &recordingRequestLogReader{}
	engine := newRequestLogTestEngine(t, reader)
	query := strings.Join([]string{
		"protocol=anthropic",
		"stream=true",
		"final_status_code=529",
		"usage_state=partial",
		"cost_state=priced",
		"pricing_completeness=partial",
		"cache_present=true",
		"channel_id=openai",
		"credential_id=9",
		"attempt_status_code=429",
		"failure_category=rate_limited",
		"error_code=provider_rate_limit",
		"retry_state=retried",
		"retry_count_min=1",
		"retry_count_max=3",
		"first_response_min_ms=10",
		"first_response_max_ms=900",
		"duration_min_ms=20",
		"duration_max_ms=2000",
		"input_tokens_min=100",
		"input_tokens_max=2000",
		"output_tokens_min=10",
		"output_tokens_max=500",
		"cost_min_nano_usd=1000",
		"cost_max_nano_usd=9000000",
	}, "&")
	recorder := performRequestLogRequest(engine, "test-auth-key", query)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 {
		t.Fatalf("Reader calls = %d, want one", len(reader.queries))
	}
	got := reader.queries[0]
	if got.Protocol != protocol.Anthropic || got.Stream == nil || !*got.Stream ||
		got.FinalStatusCode == nil || *got.FinalStatusCode != 529 ||
		got.UsageState != usage.StatePartial || got.CostState != pricing.CostStatePriced ||
		got.PricingCompleteness != pricing.CompletenessPartial ||
		got.CachePresent == nil || !*got.CachePresent ||
		got.ChannelID != channel.OpenAI ||
		got.CredentialID == nil || *got.CredentialID != 9 ||
		got.AttemptStatusCode == nil || *got.AttemptStatusCode != 429 ||
		got.FailureCategory != telemetry.FailureCategoryRateLimited ||
		got.AttemptErrorCode != "provider_rate_limit" || got.RetryState != requestlog.RetryStateRetried ||
		got.RetryCountMin == nil || *got.RetryCountMin != 1 ||
		got.RetryCountMax == nil || *got.RetryCountMax != 3 ||
		got.FirstResponseMinMS == nil || *got.FirstResponseMinMS != 10 ||
		got.FirstResponseMaxMS == nil || *got.FirstResponseMaxMS != 900 ||
		got.DurationMinMS == nil || *got.DurationMinMS != 20 ||
		got.DurationMaxMS == nil || *got.DurationMaxMS != 2000 ||
		got.InputTokensMin == nil || *got.InputTokensMin != 100 ||
		got.InputTokensMax == nil || *got.InputTokensMax != 2000 ||
		got.OutputTokensMin == nil || *got.OutputTokensMin != 10 ||
		got.OutputTokensMax == nil || *got.OutputTokensMax != 500 ||
		got.CostMinNanoUSD == nil || *got.CostMinNanoUSD != 1000 ||
		got.CostMaxNanoUSD == nil || *got.CostMaxNanoUSD != 9000000 {
		t.Fatalf("parsed advanced query = %#v", got)
	}
}

func TestRequestLogEndpointRejectsInvalidAdvancedFilters(t *testing.T) {
	tests := []string{
		"protocol=openai",
		"stream=1",
		"final_status_code=1000",
		"usage_state=unknown",
		"cost_state=unknown",
		"pricing_completeness=unknown",
		"cache_present=yes",
		"channel_id=unknown",
		"credential_id=0",
		"attempt_status_code=-1",
		"failure_category=unknown",
		"error_code=",
		"retry_state=unknown",
		"retry_count_min=4&retry_count_max=3",
		"first_response_min_ms=20&first_response_max_ms=10",
		"duration_min_ms=20&duration_max_ms=10",
		"input_tokens_min=20&input_tokens_max=10",
		"output_tokens_min=20&output_tokens_max=10",
		"cost_min_nano_usd=20&cost_max_nano_usd=10",
	}
	for _, query := range tests {
		t.Run(query, func(t *testing.T) {
			reader := &recordingRequestLogReader{}
			recorder := performRequestLogRequest(
				newRequestLogTestEngine(t, reader),
				"test-auth-key",
				query,
			)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("response = %d %s, want 400", recorder.Code, recorder.Body.String())
			}
			if len(reader.queries) != 0 {
				t.Fatalf("Reader calls = %d, want zero", len(reader.queries))
			}
		})
	}
}

func TestRequestLogEndpointReturnsOpaqueCursorAndSafeDTO(t *testing.T) {
	completedAt := time.Date(2026, time.July, 24, 12, 0, 0, 123456789, time.UTC)
	completedAtMS := completedAt.UnixMilli()
	contextThreshold := int64(272_000)
	nextCursor := &requestlog.Cursor{
		CompletedAtMS: completedAtMS,
		RequestID:     "00000000-0000-4000-8000-000000000502",
	}
	currentName := "Renamed Access Key"
	reader := &recordingRequestLogReader{
		pages: []requestlog.Page{
			{
				Items: []requestlog.Record{{
					RequestID:     "00000000-0000-4000-8000-000000000501",
					CompletedAtMS: completedAtMS,
					AccessKey: requestlog.AccessKeyRef{
						ID: 41, Name: &currentName,
					},
					Protocol:               protocol.OpenAICompletions,
					Operation:              execution.OperationChatCompletion,
					UpstreamProtocol:       protocol.OpenAICompletions,
					ClientModel:            "client-model",
					UpstreamModel:          "upstream-model",
					UpstreamReportedModel:  "reported-model",
					ModelConsistency:       telemetry.ModelConsistencyMismatch,
					Status:                 telemetry.RequestStatusSuccess,
					StatusCode:             200,
					DurationMs:             1234,
					AffinityHit:            true,
					GroupID:                12,
					ChannelID:              channel.OpenAI,
					CredentialID:           99,
					RouteMode:              channel.RouteNative,
					UsageState:             usage.StateComplete,
					CostState:              pricing.CostStatePriced,
					PricingCompleteness:    pricing.CompletenessComplete,
					PricingMode:            pricing.ModeStandard,
					ContextThresholdTokens: &contextThreshold,
					UncachedInputTokens:    300_000,
					EstimatedCostNanoUSD:   1_000_000,
					AttemptCount:           1,
				}},
				NextCursor: nextCursor,
			},
			{Items: []requestlog.Record{}},
		},
	}
	engine := newRequestLogTestEngine(t, reader)

	query := strings.Join([]string{
		"from_ms=1784890800000",
		"to_ms=1784898000000",
		"group_id=12",
		"client_model=client-model",
		"upstream_model=upstream-model",
		"access_key_id=41",
		"status=success",
		"request_id=00000000-0000-4000-8000-000000000501",
	}, "&")
	recorder := performRequestLogRequest(engine, "test-auth-key", query)
	if recorder.Code != http.StatusOK {
		t.Fatalf("first response = %d %s", recorder.Code, recorder.Body.String())
	}
	if len(reader.queries) != 1 {
		t.Fatalf("Reader calls = %d, want one", len(reader.queries))
	}
	gotQuery := reader.queries[0]
	if gotQuery.Limit != 50 || gotQuery.FromMS == nil || gotQuery.ToMS == nil ||
		*gotQuery.FromMS != 1784890800000 ||
		*gotQuery.ToMS != 1784898000000 ||
		gotQuery.GroupID == nil || *gotQuery.GroupID != 12 ||
		gotQuery.AccessKeyID == nil || *gotQuery.AccessKeyID != 41 ||
		gotQuery.ClientModel != "client-model" ||
		gotQuery.UpstreamModel != "upstream-model" ||
		gotQuery.Status != telemetry.RequestStatusSuccess ||
		gotQuery.RequestID != "00000000-0000-4000-8000-000000000501" {
		t.Fatalf("parsed ListQuery = %#v", gotQuery)
	}

	var envelope struct {
		Code int `json:"code"`
		Data struct {
			Items      []map[string]any `json:"items"`
			NextCursor *string          `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if envelope.Code != 0 || len(envelope.Data.Items) != 1 ||
		envelope.Data.NextCursor == nil || *envelope.Data.NextCursor == "" {
		t.Fatalf("first response envelope = %#v", envelope)
	}
	if _, exists := envelope.Data.Items[0]["attempts"]; exists {
		t.Fatalf("list item unexpectedly exposes attempts: %#v", envelope.Data.Items[0])
	}
	if envelope.Data.Items[0]["upstream_reported_model"] != "reported-model" ||
		envelope.Data.Items[0]["model_consistency"] != string(telemetry.ModelConsistencyMismatch) ||
		envelope.Data.Items[0]["route_mode"] != string(channel.RouteNative) ||
		envelope.Data.Items[0]["operation"] != string(execution.OperationChatCompletion) ||
		envelope.Data.Items[0]["upstream_protocol"] != string(protocol.OpenAICompletions) ||
		envelope.Data.Items[0]["pricing_mode"] != string(pricing.ModeStandard) ||
		envelope.Data.Items[0]["context_threshold_tokens"] != "272000" {
		t.Fatalf("list item model observation = %#v", envelope.Data.Items[0])
	}
	for _, forbidden := range []string{"headers", "body", "url"} {
		if strings.Contains(strings.ToLower(recorder.Body.String()), forbidden) {
			t.Fatalf("response exposes forbidden field %q: %s", forbidden, recorder.Body.String())
		}
	}

	decodedCursor, err := base64.RawURLEncoding.DecodeString(*envelope.Data.NextCursor)
	if err != nil {
		t.Fatalf("next_cursor is not raw URL base64: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(decodedCursor, &payload); err != nil {
		t.Fatalf("next_cursor JSON: %v", err)
	}
	if len(payload) != 3 || payload["v"] != float64(2) ||
		payload["completed_at_ms"] != float64(completedAtMS) ||
		payload["request_id"] != nextCursor.RequestID {
		t.Fatalf("next_cursor payload = %#v", payload)
	}

	second := performRequestLogRequest(
		engine,
		"test-auth-key",
		"cursor="+url.QueryEscape(*envelope.Data.NextCursor),
	)
	if second.Code != http.StatusOK {
		t.Fatalf("second response = %d %s", second.Code, second.Body.String())
	}
	if len(reader.queries) != 2 || reader.queries[1].Cursor == nil ||
		reader.queries[1].Cursor.CompletedAtMS != completedAtMS ||
		reader.queries[1].Cursor.RequestID != nextCursor.RequestID {
		t.Fatalf("decoded cursor query = %#v", reader.queries)
	}
	var emptyEnvelope struct {
		Data struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor *string           `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &emptyEnvelope); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if emptyEnvelope.Data.Items == nil || len(emptyEnvelope.Data.Items) != 0 ||
		emptyEnvelope.Data.NextCursor != nil {
		t.Fatalf("empty response = %#v, want items [] and next_cursor null", emptyEnvelope.Data)
	}
}

func TestRequestLogDetailEndpointReturnsAttemptsAndFrozenPricingReceipt(t *testing.T) {
	requestID := "00000000-0000-4000-8000-000000000611"
	reasoningBudget := int64(4096)
	rate := int64(1_000_000_000)
	amount := int64(1_000_000)
	contextThreshold := int64(272_000)
	receipt := &pricing.Receipt{
		SchemaVersion:          4,
		Method:                 pricing.ReceiptMethodUnitRateSum,
		MethodVersion:          1,
		Currency:               "USD",
		PricingMode:            pricing.ModeStandard,
		ContextThresholdTokens: &contextThreshold,
		Rule: pricing.ReceiptRule{
			ChannelID: string(channel.OpenAI),
			ModelID:   "gpt-4.1",
		},
		LineItems: []pricing.ReceiptLine{{
			Code:                  "input",
			Quantity:              1000,
			RateNanoUSDPerMillion: &rate,
			Multiplier:            pricing.Multiplier{Numerator: 1, Denominator: 1},
			State:                 pricing.ReceiptLinePriced,
			AmountNanoUSD:         &amount,
		}},
		TotalNanoUSD: amount,
	}
	reader := &recordingRequestLogReader{details: map[string]requestlog.Record{
		requestID: {
			RequestID:              requestID,
			CompletedAtMS:          1_784_894_400_000,
			GroupID:                12,
			ChannelID:              channel.OpenAI,
			CredentialID:           99,
			Protocol:               protocol.OpenAICompletions,
			Operation:              execution.OperationChatCompletion,
			UpstreamProtocol:       protocol.OpenAICompletions,
			Status:                 telemetry.RequestStatusSuccess,
			StatusCode:             http.StatusOK,
			AttemptCount:           1,
			UsageState:             usage.StateComplete,
			CostState:              pricing.CostStatePriced,
			PricingCompleteness:    pricing.CompletenessComplete,
			PricingMode:            pricing.ModeStandard,
			ContextThresholdTokens: &contextThreshold,
			UncachedInputTokens:    1000,
			EstimatedCostNanoUSD:   amount,
			Attempts: []requestlog.Attempt{{
				Sequence:          1,
				GroupID:           12,
				GroupName:         "Primary",
				ChannelID:         channel.OpenAI,
				CredentialID:      99,
				Operation:         execution.OperationChatCompletion,
				RouteMode:         channel.RouteNative,
				UpstreamModel:     "gpt-4.1",
				UpstreamRequestID: "upstream-request-1",
				DispatchState:     execution.DispatchMaybeSent,
				ResponseStarted:   true,
				UpstreamProtocol:  protocol.OpenAICompletions,
				Reasoning: reasoning.Config{
					Effort: "high", BudgetTokens: &reasoningBudget,
				},
				StatusCode:      http.StatusOK,
				DurationMs:      1200,
				FailureCategory: telemetry.FailureCategoryOK,
				Action:          telemetry.ActionTerminate,
				Committed:       true,
				PricingReceipt:  receipt,
			}},
		},
	}}
	engine := newRequestLogTestEngine(t, reader)
	request := httptest.NewRequest(http.MethodGet, "/api/logs/"+requestID, nil)
	request.Header.Set("Authorization", "Bearer test-auth-key")
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s, want 200", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			RequestID        string `json:"request_id"`
			Operation        string `json:"operation"`
			UpstreamProtocol string `json:"upstream_protocol"`
			PricingMode      string `json:"pricing_mode"`
			ContextThreshold string `json:"context_threshold_tokens"`
			ChannelID        string `json:"channel_id"`
			CredentialID     uint   `json:"credential_id"`
			Attempts         []struct {
				ChannelID         string `json:"channel_id"`
				CredentialID      uint   `json:"credential_id"`
				Operation         string `json:"operation"`
				RouteMode         string `json:"route_mode"`
				UpstreamRequestID string `json:"upstream_request_id"`
				DispatchState     string `json:"dispatch_state"`
				ResponseStarted   bool   `json:"response_started"`
				UpstreamProtocol  string `json:"upstream_protocol"`
				Reasoning         struct {
					Effort       string `json:"effort"`
					BudgetTokens string `json:"budget_tokens"`
				} `json:"reasoning"`
				Committed      bool `json:"committed"`
				PricingReceipt struct {
					Method           string `json:"method"`
					PricingMode      string `json:"pricing_mode"`
					ContextThreshold string `json:"context_threshold_tokens"`
					TotalNanoUSD     string `json:"total_nano_usd"`
					Rule             struct {
						ChannelID string `json:"channel_id"`
						ModelID   string `json:"model_id"`
					} `json:"rule"`
					LineItems []struct {
						Quantity      string `json:"quantity"`
						AmountNanoUSD string `json:"amount_nano_usd"`
					} `json:"line_items"`
				} `json:"pricing_receipt"`
			} `json:"attempts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if envelope.Data.RequestID != requestID || envelope.Data.ChannelID != string(channel.OpenAI) ||
		envelope.Data.Operation != string(execution.OperationChatCompletion) ||
		envelope.Data.UpstreamProtocol != string(protocol.OpenAICompletions) ||
		envelope.Data.PricingMode != string(pricing.ModeStandard) ||
		envelope.Data.ContextThreshold != "272000" ||
		envelope.Data.CredentialID != 99 || len(envelope.Data.Attempts) != 1 ||
		envelope.Data.Attempts[0].ChannelID != string(channel.OpenAI) ||
		envelope.Data.Attempts[0].CredentialID != 99 ||
		envelope.Data.Attempts[0].Operation != string(execution.OperationChatCompletion) ||
		envelope.Data.Attempts[0].RouteMode != string(channel.RouteNative) ||
		envelope.Data.Attempts[0].UpstreamRequestID != "upstream-request-1" ||
		envelope.Data.Attempts[0].DispatchState != string(execution.DispatchMaybeSent) ||
		envelope.Data.Attempts[0].UpstreamProtocol != string(protocol.OpenAICompletions) ||
		envelope.Data.Attempts[0].Reasoning.Effort != "high" ||
		envelope.Data.Attempts[0].Reasoning.BudgetTokens != "4096" ||
		!envelope.Data.Attempts[0].ResponseStarted || !envelope.Data.Attempts[0].Committed ||
		envelope.Data.Attempts[0].PricingReceipt.Method != pricing.ReceiptMethodUnitRateSum ||
		envelope.Data.Attempts[0].PricingReceipt.PricingMode != string(pricing.ModeStandard) ||
		envelope.Data.Attempts[0].PricingReceipt.ContextThreshold != "272000" ||
		envelope.Data.Attempts[0].PricingReceipt.Rule.ChannelID != string(channel.OpenAI) ||
		envelope.Data.Attempts[0].PricingReceipt.Rule.ModelID != "gpt-4.1" ||
		envelope.Data.Attempts[0].PricingReceipt.TotalNanoUSD != "1000000" ||
		len(envelope.Data.Attempts[0].PricingReceipt.LineItems) != 1 ||
		envelope.Data.Attempts[0].PricingReceipt.LineItems[0].Quantity != "1000" ||
		envelope.Data.Attempts[0].PricingReceipt.LineItems[0].AmountNanoUSD != "1000000" {
		t.Fatalf("detail projection = %#v", envelope.Data)
	}
	for _, legacyField := range []string{`"key_id"`, `"upstream_key_id"`} {
		if strings.Contains(recorder.Body.String(), legacyField) {
			t.Fatalf("detail exposes legacy field %s: %s", legacyField, recorder.Body.String())
		}
	}
}

func TestRequestLogPricingReceiptKeepsHistoricalSchemasReadable(t *testing.T) {
	tests := []struct {
		name          string
		receipt       pricing.Receipt
		wantScopeKey  string
		wantChannelID string
	}{
		{
			name: "v1 scoped identity",
			receipt: pricing.Receipt{
				SchemaVersion: 1,
				Method:        pricing.ReceiptMethodUnitRateSum,
				MethodVersion: 1,
				Currency:      "USD",
				Rule: pricing.ReceiptRule{
					ScopeKey: "provider:openai",
					ModelID:  "gpt-4.1",
				},
			},
			wantScopeKey: "provider:openai",
		},
		{
			name: "v2 global identity",
			receipt: pricing.Receipt{
				SchemaVersion: 2,
				Method:        pricing.ReceiptMethodUnitRateSum,
				MethodVersion: 1,
				Currency:      "USD",
				Rule:          pricing.ReceiptRule{ModelID: "gpt-4.1"},
			},
		},
		{
			name: "v3 channel identity",
			receipt: pricing.Receipt{
				SchemaVersion: 3,
				Method:        pricing.ReceiptMethodUnitRateSum,
				MethodVersion: 1,
				Currency:      "USD",
				Rule: pricing.ReceiptRule{
					ChannelID: string(channel.OpenAI),
					ModelID:   "gpt-4.1",
				},
			},
			wantChannelID: string(channel.OpenAI),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mapped, err := mapRequestLogPricingReceipt(&test.receipt)
			if err != nil {
				t.Fatalf("mapRequestLogPricingReceipt() error = %v", err)
			}
			gotScopeKey := ""
			if mapped.Rule.ScopeKey != nil {
				gotScopeKey = *mapped.Rule.ScopeKey
			}
			gotChannelID := ""
			if mapped.Rule.ChannelID != nil {
				gotChannelID = *mapped.Rule.ChannelID
			}
			if mapped.SchemaVersion != test.receipt.SchemaVersion ||
				mapped.PricingMode != pricing.ModeStandard ||
				mapped.Rule.ModelID != test.receipt.Rule.ModelID ||
				gotScopeKey != test.wantScopeKey || gotChannelID != test.wantChannelID {
				t.Fatalf("mapped receipt = %#v", mapped)
			}
		})
	}
}

func TestRequestLogEndpointProjectsUsageCostAndNullGroupZero(t *testing.T) {
	reasoningBudget := int64(-1)
	reader := &recordingRequestLogReader{pages: []requestlog.Page{{Items: []requestlog.Record{
		{
			RequestID:     "00000000-0000-4000-8000-000000000603",
			CompletedAtMS: time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC).UnixMilli(),
			Protocol:      protocol.OpenAICompletions,
			Status:        telemetry.RequestStatusSuccess,
			Reasoning: reasoning.Config{
				Mode:         "adaptive",
				Effort:       "high",
				BudgetTokens: &reasoningBudget,
			},
			GroupID:                 0,
			UsageState:              usage.StateComplete,
			CostState:               pricing.CostStatePriced,
			PricingCompleteness:     pricing.CompletenessComplete,
			PricingMode:             pricing.ModeFast,
			UncachedInputTokens:     1,
			CacheReadTokens:         2,
			CacheWrite5MTokens:      3,
			CacheWrite1HTokens:      4,
			CacheWriteUnknownTokens: 6,
			OutputTokens:            maxSafeInteger + 1,
			EstimatedCostNanoUSD:    123_456_789_012,
		},
	}}}}
	recorder := performRequestLogRequest(newRequestLogTestEngine(t, reader), "test-auth-key", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Items []struct {
				GroupID                 *uint   `json:"group_id"`
				ChannelID               *string `json:"channel_id"`
				CredentialID            *uint   `json:"credential_id"`
				UsageState              string  `json:"usage_state"`
				CostState               string  `json:"cost_state"`
				PricingCompleteness     string  `json:"pricing_completeness"`
				PricingMode             *string `json:"pricing_mode"`
				InputTokens             string  `json:"input_tokens"`
				CacheReadTokens         string  `json:"cache_read_tokens"`
				CacheWrite5MTokens      string  `json:"cache_write_5m_tokens"`
				CacheWrite1HTokens      string  `json:"cache_write_1h_tokens"`
				CacheWriteUnknownTokens string  `json:"cache_write_unknown_tokens"`
				OutputTokens            string  `json:"output_tokens"`
				EstimatedCostNanoUSD    string  `json:"estimated_cost_nano_usd"`
				Reasoning               *struct {
					Mode         *string `json:"mode"`
					Effort       *string `json:"effort"`
					BudgetTokens *string `json:"budget_tokens"`
				} `json:"reasoning"`
			} `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(envelope.Data.Items) != 1 {
		t.Fatalf("items = %#v", envelope.Data.Items)
	}
	item := envelope.Data.Items[0]
	if item.GroupID != nil || item.ChannelID != nil || item.CredentialID != nil ||
		item.UsageState != "complete" || item.CostState != "priced" ||
		item.PricingCompleteness != "complete" ||
		item.PricingMode == nil || *item.PricingMode != string(pricing.ModeFast) ||
		item.InputTokens != "16" || item.CacheReadTokens != "2" ||
		item.CacheWrite5MTokens != "3" || item.CacheWrite1HTokens != "4" ||
		item.CacheWriteUnknownTokens != "6" ||
		item.OutputTokens != "9007199254740992" || item.EstimatedCostNanoUSD != "123456789012" ||
		item.Reasoning == nil || item.Reasoning.Mode == nil || *item.Reasoning.Mode != "adaptive" ||
		item.Reasoning.Effort == nil || *item.Reasoning.Effort != "high" ||
		item.Reasoning.BudgetTokens == nil || *item.Reasoning.BudgetTokens != "-1" {
		t.Fatalf("usage/cost projection = %#v", item)
	}
}

func TestRequestLogResponseUsesNullModelsForProtocolOnlyResponsesResources(t *testing.T) {
	result, err := mapRequestLogListResponse(requestlog.Page{
		Items: []requestlog.Record{{
			RequestID:           "00000000-0000-4000-8000-000000000503",
			Protocol:            protocol.OpenAIResponses,
			ClientModel:         "",
			ModelConsistency:    telemetry.ModelConsistencyNotApplicable,
			Status:              telemetry.RequestStatusSuccess,
			UsageState:          usage.StateNotApplicable,
			CostState:           pricing.CostStateNotApplicable,
			PricingCompleteness: pricing.CompletenessNotApplicable,
		}},
	})
	if err != nil {
		t.Fatalf("mapRequestLogListResponse() error = %v", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	body := string(raw)
	for _, field := range []string{
		`"client_model":null`,
		`"upstream_model":null`,
		`"upstream_reported_model":null`,
		`"upstream_protocol":null`,
		`"pricing_mode":null`,
		`"model_consistency":"not_applicable"`,
		`"reasoning":null`,
	} {
		if !strings.Contains(body, field) {
			t.Fatalf("response = %s, want %s", body, field)
		}
	}
	if strings.Contains(body, `"upstream_api"`) {
		t.Fatalf("response = %s, contains legacy upstream_api", body)
	}
}

func TestRequestLogDetailProjectsEmptyAttemptReasoningAsNull(t *testing.T) {
	result, err := mapRequestLogDetailResponse(requestlog.Record{
		RequestID:           "00000000-0000-4000-8000-000000000504",
		Protocol:            protocol.OpenAIResponses,
		ModelConsistency:    telemetry.ModelConsistencyNotApplicable,
		Status:              telemetry.RequestStatusError,
		UsageState:          usage.StateNotApplicable,
		CostState:           pricing.CostStateNotApplicable,
		PricingCompleteness: pricing.CompletenessNotApplicable,
		Attempts: []requestlog.Attempt{{
			Sequence:        1,
			GroupID:         1,
			GroupName:       "group",
			FailureCategory: telemetry.FailureCategoryClientError,
			Action:          telemetry.ActionTerminate,
		}},
	})
	if err != nil {
		t.Fatalf("mapRequestLogDetailResponse() error = %v", err)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal response: %v", err)
	}
	if !strings.Contains(string(raw), `"attempts":[{"sequence":1`) ||
		strings.Count(string(raw), `"upstream_protocol":null`) != 2 ||
		!strings.Contains(string(raw), `"reasoning":null`) ||
		strings.Contains(string(raw), `"upstream_api"`) {
		t.Fatalf("response = %s, want explicit null upstream protocols without legacy field", raw)
	}
}

func TestRequestLogUsageCostProjectionRejectsUnsafeValues(t *testing.T) {
	base := requestlog.Record{
		UsageState: usage.StateComplete, CostState: pricing.CostStatePriced,
	}
	tests := []struct {
		name   string
		mutate func(*requestlog.Record)
	}{
		{name: "usage state", mutate: func(record *requestlog.Record) { record.UsageState = "invalid" }},
		{name: "cost state", mutate: func(record *requestlog.Record) { record.CostState = "invalid" }},
		{name: "negative token", mutate: func(record *requestlog.Record) { record.OutputTokens = -1 }},
		{name: "negative cost", mutate: func(record *requestlog.Record) { record.EstimatedCostNanoUSD = -1 }},
		{
			name: "missing usage cannot be priced",
			mutate: func(record *requestlog.Record) {
				record.UsageState = usage.StateMissing
			},
		},
		{
			name: "not applicable usage cannot be unpriced",
			mutate: func(record *requestlog.Record) {
				record.UsageState = usage.StateNotApplicable
				record.CostState = pricing.CostStateUnpriced
			},
		},
		{
			name: "unpriced cost must be zero",
			mutate: func(record *requestlog.Record) {
				record.CostState = pricing.CostStateUnpriced
				record.EstimatedCostNanoUSD = 1
			},
		},
	}
	if strconv.IntSize == 64 {
		tests = append(tests, struct {
			name   string
			mutate func(*requestlog.Record)
		}{
			name: "unsafe final Group ID",
			mutate: func(record *requestlog.Record) {
				unsafeGroupID := uint64(maxSafeInteger) + 1
				record.GroupID = uint(unsafeGroupID)
			},
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := base
			test.mutate(&record)
			if _, err := mapRequestLogListResponse(requestlog.Page{Items: []requestlog.Record{record}}); err == nil {
				t.Fatal("mapRequestLogListResponse() error = nil, want rejection")
			}
		})
	}
}

func TestRequestLogUsageCostProjectionAcceptsMaximumSafeFinalGroupID(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("maximum JavaScript safe integer does not fit uint on this architecture")
	}
	safeGroupID := uint64(maxSafeInteger)
	record := requestlog.Record{
		GroupID: uint(safeGroupID), UsageState: usage.StateComplete,
		CostState: pricing.CostStatePriced, PricingCompleteness: pricing.CompletenessComplete,
	}
	result, err := mapRequestLogListResponse(requestlog.Page{Items: []requestlog.Record{record}})
	if err != nil {
		t.Fatalf("mapRequestLogListResponse() error = %v", err)
	}
	if len(result.Items) != 1 || result.Items[0].GroupID == nil ||
		uint64(*result.Items[0].GroupID) != safeGroupID {
		t.Fatalf("projected final Group ID = %#v", result.Items)
	}
}

func TestRequestLogEndpointFailsClosedOnCorruptReaderUsageCost(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*requestlog.Record)
	}{
		{
			name: "invalid state matrix",
			mutate: func(record *requestlog.Record) {
				record.UsageState = usage.StateMissing
				record.CostState = pricing.CostStatePriced
			},
		},
		{
			name: "unpriced nonzero cost",
			mutate: func(record *requestlog.Record) {
				record.CostState = pricing.CostStateUnpriced
				record.EstimatedCostNanoUSD = 1
			},
		},
	}
	if strconv.IntSize == 64 {
		tests = append(tests, struct {
			name   string
			mutate func(*requestlog.Record)
		}{
			name: "unsafe final Group ID",
			mutate: func(record *requestlog.Record) {
				unsafeGroupID := uint64(maxSafeInteger) + 1
				record.GroupID = uint(unsafeGroupID)
			},
		})
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			record := requestlog.Record{
				RequestID:     "00000000-0000-4000-8000-000000000605",
				CompletedAtMS: time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC).UnixMilli(),
				UsageState:    usage.StateComplete,
				CostState:     pricing.CostStatePriced,
			}
			test.mutate(&record)
			reader := &recordingRequestLogReader{
				pages: []requestlog.Page{{Items: []requestlog.Record{record}}},
			}
			recorder := performRequestLogRequest(
				newRequestLogTestEngine(t, reader),
				"test-auth-key",
				"",
			)
			if recorder.Code != http.StatusInternalServerError ||
				!strings.Contains(recorder.Body.String(), "INTERNAL_SERVER_ERROR") ||
				strings.Contains(strings.ToLower(recorder.Body.String()), "unsafe") {
				t.Fatalf("corrupt response = %d %s", recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestRequestLogEndpointRequiresManagementAuthentication(t *testing.T) {
	reader := &recordingRequestLogReader{}
	engine := newRequestLogTestEngine(t, reader)
	recorder := performRequestLogRequest(engine, "", "")
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("response = %d %s, want 401", recorder.Code, recorder.Body.String())
	}
	assertRequestLogErrorCode(t, recorder, "UNAUTHORIZED")
	if len(reader.queries) != 0 {
		t.Fatalf("Reader calls = %d, want zero", len(reader.queries))
	}
}

func TestRequestLogEndpointsBindAccessKeyScopeAndRedactRoutingInternals(t *testing.T) {
	initControlI18n(t)
	fixture := newServiceFixture(t)
	current, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "log viewer",
	})
	if err != nil {
		t.Fatalf("CreateAccessKey(current) error = %v", err)
	}
	other, err := fixture.service.CreateAccessKey(t.Context(), AccessKeyCreateRequest{
		Name: "other private key",
	})
	if err != nil {
		t.Fatalf("CreateAccessKey(other) error = %v", err)
	}
	requestID := "00000000-0000-4000-8000-000000000701"
	record := requestlog.Record{
		RequestID:             requestID,
		CompletedAtMS:         time.Date(2026, time.August, 8, 19, 0, 0, 0, time.UTC).UnixMilli(),
		AccessKey:             requestlog.AccessKeyRef{ID: current.ID, Name: &current.Name},
		Protocol:              protocol.OpenAICompletions,
		Operation:             execution.OperationChatCompletion,
		UpstreamProtocol:      protocol.Anthropic,
		ClientModel:           "client-model",
		UpstreamModel:         "private-upstream-model",
		UpstreamReportedModel: "private-reported-model",
		ModelConsistency:      telemetry.ModelConsistencyMismatch,
		Status:                telemetry.RequestStatusSuccess,
		StatusCode:            http.StatusOK,
		DurationMs:            120,
		AttemptCount:          2,
		AffinityHit:           true,
		GroupID:               99,
		ChannelID:             channel.OpenAI,
		CredentialID:          101,
		RouteMode:             channel.RouteConverted,
		UsageState:            usage.StateComplete,
		CostState:             pricing.CostStatePriced,
		PricingCompleteness:   pricing.CompletenessComplete,
		PricingMode:           pricing.ModeFast,
		UncachedInputTokens:   10,
		OutputTokens:          2,
		EstimatedCostNanoUSD:  50,
		Attempts: []requestlog.Attempt{{
			Sequence: 1, GroupID: 99, GroupName: "private group",
			ChannelID: channel.OpenAI, CredentialID: 101,
			UpstreamModel: "private-upstream-model",
		}},
	}
	reader := &recordingRequestLogReader{pages: []requestlog.Page{
		{Items: []requestlog.Record{record}},
		{Items: []requestlog.Record{record}},
		{Items: []requestlog.Record{}},
	}}
	fixture.service.requestLogs = reader
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)

	list := performRequestLogRequest(engine, current.Key, "client_model=client-model")
	if list.Code != http.StatusOK {
		t.Fatalf("AccessKey logs = %d %s, want 200", list.Code, list.Body.String())
	}
	if len(reader.queries) != 1 || reader.queries[0].AccessKeyID == nil ||
		*reader.queries[0].AccessKeyID != current.ID {
		t.Fatalf("AccessKey list query = %#v", reader.queries)
	}
	assertAccessKeyLogRedaction(t, list.Body.Bytes(), false)
	if strings.Contains(list.Body.String(), other.Name) {
		t.Fatalf("AccessKey logs expose another key: %s", list.Body.String())
	}

	for _, query := range []string{
		"access_key_id=" + strconv.FormatUint(uint64(other.ID), 10),
		"group_id=99",
		"channel_id=openai",
		"credential_id=101",
		"upstream_model=private-upstream-model",
		"retry_state=retried",
	} {
		recorder := performRequestLogRequest(engine, current.Key, query)
		if recorder.Code != http.StatusBadRequest || len(reader.queries) != 1 {
			t.Fatalf("AccessKey log filter %q = %d %s, calls=%d", query, recorder.Code, recorder.Body.String(), len(reader.queries))
		}
	}

	detailRequest := httptest.NewRequest(http.MethodGet, "/api/logs/"+requestID, nil)
	detailRequest.Header.Set("Authorization", "Bearer "+current.Key)
	detail := httptest.NewRecorder()
	engine.ServeHTTP(detail, detailRequest)
	if detail.Code != http.StatusOK {
		t.Fatalf("AccessKey log detail = %d %s, want 200", detail.Code, detail.Body.String())
	}
	if len(reader.queries) != 2 || reader.queries[1].RequestID != requestID ||
		reader.queries[1].AccessKeyID == nil || *reader.queries[1].AccessKeyID != current.ID ||
		len(reader.getRequests) != 0 {
		t.Fatalf("AccessKey detail lookup = queries %#v, gets %#v", reader.queries, reader.getRequests)
	}
	assertAccessKeyLogRedaction(t, detail.Body.Bytes(), true)

	missingRequest := httptest.NewRequest(
		http.MethodGet,
		"/api/logs/00000000-0000-4000-8000-000000000702",
		nil,
	)
	missingRequest.Header.Set("Authorization", "Bearer "+current.Key)
	missing := httptest.NewRecorder()
	engine.ServeHTTP(missing, missingRequest)
	if missing.Code != http.StatusNotFound || len(reader.queries) != 3 ||
		len(reader.getRequests) != 0 {
		t.Fatalf("cross-key detail = %d %s, queries=%#v gets=%#v", missing.Code, missing.Body.String(), reader.queries, reader.getRequests)
	}
}

func assertAccessKeyLogRedaction(t *testing.T, body []byte, detail bool) {
	t.Helper()
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("decode AccessKey log envelope: %v", err)
	}
	var item map[string]json.RawMessage
	if detail {
		if err := json.Unmarshal(envelope.Data, &item); err != nil {
			t.Fatalf("decode AccessKey log detail: %v", err)
		}
	} else {
		var page struct {
			Items []map[string]json.RawMessage `json:"items"`
		}
		if err := json.Unmarshal(envelope.Data, &page); err != nil || len(page.Items) != 1 {
			t.Fatalf("decode AccessKey log page = %#v/%v", page, err)
		}
		item = page.Items[0]
	}
	for field, want := range map[string]string{
		"upstream_model":          "null",
		"upstream_reported_model": "null",
		"model_consistency":       `"not_applicable"`,
		"affinity_hit":            "false",
		"group_id":                "null",
		"channel_id":              "null",
		"credential_id":           "null",
		"route_mode":              "null",
		"upstream_protocol":       "null",
		"pricing_mode":            `"fast"`,
		"attempt_count":           "0",
	} {
		if string(item[field]) != want {
			t.Fatalf("AccessKey log %s = %s, want %s; body=%s", field, item[field], want, body)
		}
	}
	if detail && string(item["attempts"]) != "[]" {
		t.Fatalf("AccessKey attempts = %s, want []; body=%s", item["attempts"], body)
	}
	for _, secret := range []string{
		"private-upstream-model", "private-reported-model", "private group",
	} {
		if bytes.Contains(body, []byte(secret)) {
			t.Fatalf("AccessKey log exposes %q: %s", secret, body)
		}
	}
}

type recordingRequestLogReader struct {
	pages       []requestlog.Page
	queries     []requestlog.ListQuery
	err         error
	details     map[string]requestlog.Record
	getErr      error
	getRequests []string
}

func (reader *recordingRequestLogReader) List(
	_ context.Context,
	query requestlog.ListQuery,
) (requestlog.Page, error) {
	reader.queries = append(reader.queries, query)
	if reader.err != nil {
		return requestlog.Page{}, reader.err
	}
	index := len(reader.queries) - 1
	if index >= len(reader.pages) {
		return requestlog.Page{Items: []requestlog.Record{}}, nil
	}
	return reader.pages[index], nil
}

func (reader *recordingRequestLogReader) Get(
	_ context.Context,
	requestID string,
) (requestlog.Record, error) {
	reader.getRequests = append(reader.getRequests, requestID)
	if reader.getErr != nil {
		return requestlog.Record{}, reader.getErr
	}
	if record, ok := reader.details[requestID]; ok {
		return record, nil
	}
	return requestlog.Record{}, fmt.Errorf("request log %s not found", requestID)
}

func newRequestLogTestEngine(t *testing.T, reader RequestLogReader) *gin.Engine {
	t.Helper()
	initControlI18n(t)
	fixture := newServiceFixture(t)
	fixture.service.requestLogs = reader
	engine := gin.New()
	NewServer(&config.Config{AuthKey: "test-auth-key"}, fixture.service).RegisterRoutes(engine)
	return engine
}

func performRequestLogRequest(engine *gin.Engine, authKey, query string) *httptest.ResponseRecorder {
	target := "/api/logs"
	if query != "" {
		target += "?" + query
	}
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if authKey != "" {
		request.Header.Set("Authorization", "Bearer "+authKey)
	}
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func assertRequestLogErrorCode(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()
	var envelope struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error response: %v; body=%s", err, recorder.Body.String())
	}
	if recorder.Code != http.StatusBadRequest && want != "UNAUTHORIZED" {
		t.Fatalf("HTTP status = %d, want 400; body=%s", recorder.Code, recorder.Body.String())
	}
	if envelope.Code != want {
		t.Fatalf("error code = %q, want %q; body=%s", envelope.Code, want, recorder.Body.String())
	}
}

func encodeTestCursorPayload(payload string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(payload))
}

func TestRequestLogAttemptActionUsesCredentialTerms(t *testing.T) {
	for input, want := range map[telemetry.Action]string{
		telemetry.Action("cooldown_credential"): "cooldown_credential",
		telemetry.Action("fail_credential"):     "fail_credential",
		telemetry.ActionTerminate:               string(telemetry.ActionTerminate),
	} {
		mapped, err := mapRequestLogAttempt(requestlog.Attempt{Action: input})
		if err != nil {
			t.Fatalf("mapRequestLogAttempt(%q) error = %v", input, err)
		}
		if mapped.Action != want {
			t.Fatalf("mapRequestLogAttempt(%q).Action = %q, want %q", input, mapped.Action, want)
		}
		encoded, err := json.Marshal(mapped)
		if err != nil {
			t.Fatalf("json.Marshal(%q) error = %v", input, err)
		}
		if strings.Contains(string(encoded), `"action":"cooldown_key"`) ||
			strings.Contains(string(encoded), `"action":"fail_key"`) {
			t.Fatalf("attempt wire exposes legacy key action: %s", encoded)
		}
	}
}

func Example_requestLogOpaqueCursor() {
	payload := `{"v":2,"completed_at_ms":1784894400000,"request_id":"00000000-0000-4000-8000-000000000001"}`
	fmt.Println(encodeTestCursorPayload(payload))
	// Output: eyJ2IjoyLCJjb21wbGV0ZWRfYXRfbXMiOjE3ODQ4OTQ0MDAwMDAsInJlcXVlc3RfaWQiOiIwMDAwMDAwMC0wMDAwLTQwMDAtODAwMC0wMDAwMDAwMDAwMDEifQ
}
