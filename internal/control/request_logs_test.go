package control

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"gpt-load/internal/platform/config"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

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
		{name: "empty model", query: "model="},
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

func TestRequestLogEndpointReturnsOpaqueCursorAndSafeDTO(t *testing.T) {
	completedAt := time.Date(2026, time.July, 24, 12, 0, 0, 123456789, time.UTC)
	completedAtMS := completedAt.UnixMilli()
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
					Protocol:      protocol.OpenAICompletions,
					ClientModel:   "client-model",
					UpstreamModel: "upstream-model",
					Status:        telemetry.RequestStatusSuccess,
					StatusCode:    200,
					DurationMs:    1234,
					AffinityHit:   true,
					UsageState:    usage.StateNotApplicable,
					CostState:     pricing.CostStateNotApplicable,
					Attempts: []requestlog.Attempt{{
						Sequence:        1,
						GroupID:         12,
						GroupName:       "Primary",
						KeyID:           11,
						UpstreamModel:   "upstream-model",
						StatusCode:      200,
						DurationMs:      1234,
						FailureCategory: telemetry.FailureCategoryOK,
						Action:          telemetry.ActionTerminate,
						Committed:       true,
					}},
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
		"model=client-model",
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
	attempts, ok := envelope.Data.Items[0]["attempts"].([]any)
	if !ok || len(attempts) != 1 {
		t.Fatalf("attempts = %#v, want one", envelope.Data.Items[0]["attempts"])
	}
	attempt, ok := attempts[0].(map[string]any)
	if !ok || attempt["key_id"] != float64(11) {
		t.Fatalf("attempt = %#v, want key_id 11", attempts[0])
	}
	if _, exists := attempt["key_mask"]; exists {
		t.Fatalf("attempt exposes key_mask: %#v", attempt)
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

func TestRequestLogEndpointProjectsUsageCostAndNullGroupZero(t *testing.T) {
	reader := &recordingRequestLogReader{pages: []requestlog.Page{{Items: []requestlog.Record{
		{
			RequestID:            "00000000-0000-4000-8000-000000000603",
			CompletedAtMS:        time.Date(2026, time.July, 27, 0, 0, 0, 0, time.UTC).UnixMilli(),
			Protocol:             protocol.OpenAICompletions,
			Status:               telemetry.RequestStatusSuccess,
			GroupID:              0,
			UsageState:           usage.StateComplete,
			CostState:            pricing.CostStatePriced,
			UncachedInputTokens:  1,
			CacheReadTokens:      2,
			CacheWrite5MTokens:   3,
			CacheWrite1HTokens:   4,
			OutputTokens:         5,
			EstimatedCostNanoUSD: 123_456_789_012,
			Attempts:             []requestlog.Attempt{},
		},
	}}}}
	recorder := performRequestLogRequest(newRequestLogTestEngine(t, reader), "test-auth-key", "")
	if recorder.Code != http.StatusOK {
		t.Fatalf("response = %d %s", recorder.Code, recorder.Body.String())
	}
	var envelope struct {
		Data struct {
			Items []struct {
				GroupID              *uint  `json:"group_id"`
				UsageState           string `json:"usage_state"`
				CostState            string `json:"cost_state"`
				UncachedInputTokens  int64  `json:"uncached_input_tokens"`
				CacheReadTokens      int64  `json:"cache_read_tokens"`
				CacheWrite5MTokens   int64  `json:"cache_write_5m_tokens"`
				CacheWrite1HTokens   int64  `json:"cache_write_1h_tokens"`
				OutputTokens         int64  `json:"output_tokens"`
				EstimatedCostNanoUSD string `json:"estimated_cost_nano_usd"`
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
	if item.GroupID != nil || item.UsageState != "complete" || item.CostState != "priced" ||
		item.UncachedInputTokens != 1 || item.CacheReadTokens != 2 ||
		item.CacheWrite5MTokens != 3 || item.CacheWrite1HTokens != 4 ||
		item.OutputTokens != 5 || item.EstimatedCostNanoUSD != "123456789012" {
		t.Fatalf("usage/cost projection = %#v", item)
	}
}

func TestRequestLogResponseUsesNullModelsForProtocolOnlyResponsesResources(t *testing.T) {
	result, err := mapRequestLogListResponse(requestlog.Page{
		Items: []requestlog.Record{{
			RequestID:   "00000000-0000-4000-8000-000000000503",
			Protocol:    protocol.OpenAIResponses,
			ClientModel: "",
			Status:      telemetry.RequestStatusSuccess,
			UsageState:  usage.StateNotApplicable,
			CostState:   pricing.CostStateNotApplicable,
			Attempts: []requestlog.Attempt{{
				Sequence:        1,
				GroupID:         12,
				GroupName:       "Primary",
				KeyID:           11,
				UpstreamModel:   "",
				StatusCode:      http.StatusOK,
				FailureCategory: telemetry.FailureCategoryOK,
				Action:          telemetry.ActionTerminate,
				Committed:       true,
			}},
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
	for _, field := range []string{`"client_model":null`, `"upstream_model":null`} {
		if !strings.Contains(body, field) {
			t.Fatalf("response = %s, want %s", body, field)
		}
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
		{name: "unsafe token", mutate: func(record *requestlog.Record) { record.OutputTokens = maxSafeInteger + 1 }},
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
		CostState: pricing.CostStatePriced,
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
				Attempts:      []requestlog.Attempt{},
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

type recordingRequestLogReader struct {
	pages   []requestlog.Page
	queries []requestlog.ListQuery
	err     error
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

func Example_requestLogOpaqueCursor() {
	payload := `{"v":2,"completed_at_ms":1784894400000,"request_id":"00000000-0000-4000-8000-000000000001"}`
	fmt.Println(encodeTestCursorPayload(payload))
	// Output: eyJ2IjoyLCJjb21wbGV0ZWRfYXRfbXMiOjE3ODQ4OTQ0MDAwMDAsInJlcXVlc3RfaWQiOiIwMDAwMDAwMC0wMDAwLTQwMDAtODAwMC0wMDAwMDAwMDAwMDEifQ
}
