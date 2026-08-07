package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const (
	defaultRequestLogLimit = 50
	maxRequestLogLimit     = 200
	requestLogCursorV2     = 2
)

var canonicalLowercaseUUIDv4 = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

type RequestLogReader interface {
	List(context.Context, requestlog.ListQuery) (requestlog.Page, error)
	Get(context.Context, string) (requestlog.Record, error)
}

type requestLogCursorPayload struct {
	Version       int    `json:"v"`
	CompletedAtMS int64  `json:"completed_at_ms"`
	RequestID     string `json:"request_id"`
}

type requestLogAccessKeyResponse struct {
	ID      uint    `json:"id"`
	Name    *string `json:"name"`
	Deleted bool    `json:"deleted"`
}

type requestLogReasoningResponse struct {
	Mode         *string `json:"mode"`
	Effort       *string `json:"effort"`
	BudgetTokens *string `json:"budget_tokens"`
}

type requestLogAttemptResponse struct {
	Sequence        int                               `json:"sequence"`
	GroupID         uint                              `json:"group_id"`
	GroupName       string                            `json:"group_name"`
	KeyID           uint                              `json:"key_id"`
	UpstreamModel   *string                           `json:"upstream_model"`
	StatusCode      int                               `json:"status_code"`
	DurationMs      int64                             `json:"duration_ms"`
	FailureCategory telemetry.FailureCategory         `json:"failure_category"`
	Action          telemetry.Action                  `json:"action"`
	WillRetry       bool                              `json:"will_retry"`
	ErrorCode       string                            `json:"error_code"`
	ErrorSummary    string                            `json:"error_summary"`
	PricingReceipt  *requestLogPricingReceiptResponse `json:"pricing_receipt"`
}

type requestLogPricingIdentityResponse struct {
	ScopeKey *string `json:"scope_key,omitempty"`
	ModelID  string  `json:"model_id"`
}

type requestLogPricingMultiplierResponse struct {
	Numerator   string `json:"numerator"`
	Denominator string `json:"denominator"`
}

type requestLogPricingLineResponse struct {
	Code                  string                              `json:"code"`
	Quantity              string                              `json:"quantity"`
	RateNanoUSDPerMillion *string                             `json:"rate_nano_usd_per_million"`
	Multiplier            requestLogPricingMultiplierResponse `json:"multiplier"`
	State                 pricing.ReceiptLineState            `json:"state"`
	AmountNanoUSD         *string                             `json:"amount_nano_usd"`
}

type requestLogPricingReceiptResponse struct {
	SchemaVersion          int                               `json:"schema_version"`
	Method                 string                            `json:"method"`
	MethodVersion          int                               `json:"method_version"`
	Currency               string                            `json:"currency"`
	Rule                   requestLogPricingIdentityResponse `json:"rule"`
	ContextThresholdTokens *string                           `json:"context_threshold_tokens"`
	LineItems              []requestLogPricingLineResponse   `json:"line_items"`
	TotalNanoUSD           string                            `json:"total_nano_usd"`
}

type requestLogItemResponse struct {
	RequestID               string                       `json:"request_id"`
	CompletedAtMS           int64                        `json:"completed_at_ms"`
	AccessKey               requestLogAccessKeyResponse  `json:"access_key"`
	Protocol                string                       `json:"protocol"`
	ClientModel             *string                      `json:"client_model"`
	UpstreamModel           *string                      `json:"upstream_model"`
	Reasoning               *requestLogReasoningResponse `json:"reasoning"`
	Status                  telemetry.RequestStatus      `json:"status"`
	StatusCode              int                          `json:"status_code"`
	Stream                  bool                         `json:"stream"`
	FirstResponseMs         *int64                       `json:"first_response_ms"`
	DurationMs              int64                        `json:"duration_ms"`
	AttemptCount            int                          `json:"attempt_count"`
	ErrorCode               string                       `json:"error_code"`
	ErrorSummary            string                       `json:"error_summary"`
	AffinityHit             bool                         `json:"affinity_hit"`
	GroupID                 *uint                        `json:"group_id"`
	UsageState              usage.State                  `json:"usage_state"`
	CostState               pricing.CostState            `json:"cost_state"`
	PricingCompleteness     pricing.Completeness         `json:"pricing_completeness"`
	InputTokens             string                       `json:"input_tokens"`
	CacheReadTokens         string                       `json:"cache_read_tokens"`
	CacheWrite5MTokens      string                       `json:"cache_write_5m_tokens"`
	CacheWrite1HTokens      string                       `json:"cache_write_1h_tokens"`
	CacheWriteUnknownTokens string                       `json:"cache_write_unknown_tokens"`
	OutputTokens            string                       `json:"output_tokens"`
	EstimatedCostNanoUSD    string                       `json:"estimated_cost_nano_usd"`
}

type requestLogDetailResponse struct {
	requestLogItemResponse
	Attempts []requestLogAttemptResponse `json:"attempts"`
}

type requestLogListResponse struct {
	Items      []requestLogItemResponse `json:"items"`
	NextCursor *string                  `json:"next_cursor"`
}

func (service *Service) ListRequestLogs(
	ctx context.Context,
	query requestlog.ListQuery,
) (requestlog.Page, error) {
	if service.requestLogs == nil {
		return requestlog.Page{}, app_errors.ErrInternalServer
	}
	page, err := service.requestLogs.List(ctx, query)
	if err != nil {
		return requestlog.Page{}, app_errors.ParseDBError(err)
	}
	return page, nil
}

func (service *Service) GetRequestLog(ctx context.Context, requestID string) (requestlog.Record, error) {
	if service.requestLogs == nil {
		return requestlog.Record{}, app_errors.ErrInternalServer
	}
	record, err := service.requestLogs.Get(ctx, requestID)
	if err != nil {
		return requestlog.Record{}, app_errors.ParseDBError(err)
	}
	return record, nil
}

func (s *Server) handleListRequestLogs(c *gin.Context) {
	query, apiErr := parseRequestLogQuery(c.Request.URL.RawQuery)
	if apiErr != nil {
		writeServiceError(c, "list_request_logs", apiErr)
		return
	}
	page, err := s.service.ListRequestLogs(c.Request.Context(), query)
	if err != nil {
		writeServiceError(c, "list_request_logs", err)
		return
	}
	result, err := mapRequestLogListResponse(page)
	if err != nil {
		writeServiceError(c, "list_request_logs", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func (s *Server) handleGetRequestLog(c *gin.Context) {
	requestID := c.Param("request_id")
	if !canonicalLowercaseUUIDv4.MatchString(requestID) {
		writeServiceError(c, "get_request_log", app_errors.ErrBadRequest)
		return
	}
	record, err := s.service.GetRequestLog(c.Request.Context(), requestID)
	if err != nil {
		writeServiceError(c, "get_request_log", err)
		return
	}
	result, err := mapRequestLogDetailResponse(record)
	if err != nil {
		writeServiceError(c, "get_request_log", err)
		return
	}
	response.SuccessI18n(c, "common.success", result)
}

func parseRequestLogQuery(rawQuery string) (requestlog.ListQuery, *app_errors.APIError) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return requestlog.ListQuery{}, app_errors.ErrBadRequest
	}
	allowed := map[string]struct{}{
		"from_ms": {}, "to_ms": {}, "group_id": {}, "client_model": {}, "upstream_model": {}, "access_key_id": {},
		"status": {}, "request_id": {}, "protocol": {}, "stream": {}, "final_status_code": {},
		"usage_state": {}, "cost_state": {}, "pricing_completeness": {}, "cache_present": {},
		"upstream_key_id": {}, "attempt_status_code": {}, "failure_category": {}, "error_code": {},
		"retry_state": {}, "retry_count_min": {}, "retry_count_max": {},
		"first_response_min_ms": {}, "first_response_max_ms": {},
		"duration_min_ms": {}, "duration_max_ms": {},
		"input_tokens_min": {}, "input_tokens_max": {},
		"output_tokens_min": {}, "output_tokens_max": {},
		"cost_min_nano_usd": {}, "cost_max_nano_usd": {},
		"limit": {}, "cursor": {},
	}
	for key, value := range values {
		if _, ok := allowed[key]; !ok || len(value) != 1 {
			return requestlog.ListQuery{}, app_errors.ErrBadRequest
		}
	}

	query := requestlog.ListQuery{Limit: defaultRequestLogLimit}
	if value, ok := singleQueryValue(values, "from_ms"); ok {
		parsed, err := parseCanonicalSafeMilliseconds(value)
		if err != nil {
			return requestlog.ListQuery{}, app_errors.ErrBadRequest
		}
		query.FromMS = &parsed
	}
	if value, ok := singleQueryValue(values, "to_ms"); ok {
		parsed, err := parseCanonicalSafeMilliseconds(value)
		if err != nil {
			return requestlog.ListQuery{}, app_errors.ErrBadRequest
		}
		query.ToMS = &parsed
	}
	if query.FromMS != nil && query.ToMS != nil && *query.FromMS >= *query.ToMS {
		return requestlog.ListQuery{}, app_errors.ErrValidation
	}
	if value, ok := singleQueryValue(values, "group_id"); ok {
		parsed, apiErr := parseRequestLogID(value)
		if apiErr != nil {
			return requestlog.ListQuery{}, apiErr
		}
		query.GroupID = &parsed
	}
	if value, ok := singleQueryValue(values, "client_model"); ok {
		if !validUsageModel(value) {
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
		query.ClientModel = value
	}
	if value, ok := singleQueryValue(values, "upstream_model"); ok {
		if !validUsageModel(value) {
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
		query.UpstreamModel = value
	}
	if value, ok := singleQueryValue(values, "access_key_id"); ok {
		parsed, apiErr := parseRequestLogID(value)
		if apiErr != nil {
			return requestlog.ListQuery{}, apiErr
		}
		query.AccessKeyID = &parsed
	}
	if value, ok := singleQueryValue(values, "protocol"); ok {
		parsed := protocol.Protocol(value)
		if !parsed.Valid() {
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
		query.Protocol = parsed
	}
	if value, ok := singleQueryValue(values, "stream"); ok {
		parsed, valid := parseRequestLogBool(value)
		if !valid {
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
		query.Stream = &parsed
	}
	if value, ok := singleQueryValue(values, "final_status_code"); ok {
		parsed, apiErr := parseRequestLogStatusCode(value)
		if apiErr != nil {
			return requestlog.ListQuery{}, apiErr
		}
		query.FinalStatusCode = &parsed
	}
	if value, ok := singleQueryValue(values, "status"); ok {
		status := telemetry.RequestStatus(value)
		switch status {
		case telemetry.RequestStatusSuccess,
			telemetry.RequestStatusError,
			telemetry.RequestStatusIncomplete,
			telemetry.RequestStatusCanceled:
			query.Status = status
		default:
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
	}
	if value, ok := singleQueryValue(values, "usage_state"); ok {
		state := usage.State(value)
		switch state {
		case usage.StateComplete, usage.StatePartial, usage.StateMissing, usage.StateNotApplicable:
			query.UsageState = state
		default:
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
	}
	if value, ok := singleQueryValue(values, "cost_state"); ok {
		state := pricing.CostState(value)
		switch state {
		case pricing.CostStatePriced, pricing.CostStateUnpriced, pricing.CostStateNotApplicable:
			query.CostState = state
		default:
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
	}
	if value, ok := singleQueryValue(values, "pricing_completeness"); ok {
		completeness := pricing.Completeness(value)
		switch completeness {
		case pricing.CompletenessComplete,
			pricing.CompletenessPartial,
			pricing.CompletenessUnavailable,
			pricing.CompletenessNotApplicable:
			query.PricingCompleteness = completeness
		default:
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
	}
	if value, ok := singleQueryValue(values, "cache_present"); ok {
		parsed, valid := parseRequestLogBool(value)
		if !valid {
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
		query.CachePresent = &parsed
	}
	if value, ok := singleQueryValue(values, "upstream_key_id"); ok {
		parsed, apiErr := parseRequestLogID(value)
		if apiErr != nil {
			return requestlog.ListQuery{}, apiErr
		}
		query.UpstreamKeyID = &parsed
	}
	if value, ok := singleQueryValue(values, "attempt_status_code"); ok {
		parsed, apiErr := parseRequestLogStatusCode(value)
		if apiErr != nil {
			return requestlog.ListQuery{}, apiErr
		}
		query.AttemptStatusCode = &parsed
	}
	if value, ok := singleQueryValue(values, "failure_category"); ok {
		category := telemetry.FailureCategory(value)
		switch category {
		case telemetry.FailureCategoryOK,
			telemetry.FailureCategoryRateLimited,
			telemetry.FailureCategoryModelUnavailable,
			telemetry.FailureCategoryInvalidKey,
			telemetry.FailureCategoryUpstreamHost,
			telemetry.FailureCategoryClientError,
			telemetry.FailureCategoryDownstreamCancel,
			telemetry.FailureCategoryAmbiguous:
			query.FailureCategory = category
		default:
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
	}
	if value, ok := singleQueryValue(values, "error_code"); ok {
		if !validUsageModel(value) {
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
		query.AttemptErrorCode = value
	}
	if value, ok := singleQueryValue(values, "retry_state"); ok {
		state := requestlog.RetryState(value)
		switch state {
		case requestlog.RetryStateRetried, requestlog.RetryStateNotRetried:
			query.RetryState = state
		default:
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
	}
	if apiErr := parseRequestLogIntRange(
		values,
		"retry_count_min",
		"retry_count_max",
		&query.RetryCountMin,
		&query.RetryCountMax,
	); apiErr != nil {
		return requestlog.ListQuery{}, apiErr
	}
	for _, target := range []struct {
		minimumKey string
		maximumKey string
		minimum    **int64
		maximum    **int64
	}{
		{"first_response_min_ms", "first_response_max_ms", &query.FirstResponseMinMS, &query.FirstResponseMaxMS},
		{"duration_min_ms", "duration_max_ms", &query.DurationMinMS, &query.DurationMaxMS},
		{"input_tokens_min", "input_tokens_max", &query.InputTokensMin, &query.InputTokensMax},
		{"output_tokens_min", "output_tokens_max", &query.OutputTokensMin, &query.OutputTokensMax},
		{"cost_min_nano_usd", "cost_max_nano_usd", &query.CostMinNanoUSD, &query.CostMaxNanoUSD},
	} {
		if apiErr := parseRequestLogInt64Range(
			values,
			target.minimumKey,
			target.maximumKey,
			target.minimum,
			target.maximum,
		); apiErr != nil {
			return requestlog.ListQuery{}, apiErr
		}
	}
	if value, ok := singleQueryValue(values, "request_id"); ok {
		if !canonicalLowercaseUUIDv4.MatchString(value) {
			return requestlog.ListQuery{}, app_errors.ErrBadRequest
		}
		query.RequestID = value
	}
	if value, ok := singleQueryValue(values, "limit"); ok {
		parsed, err := parseCanonicalSafeUint(value)
		if err != nil {
			if errors.Is(err, errUnsafeCanonicalUint) {
				return requestlog.ListQuery{}, app_errors.ErrValidation
			}
			return requestlog.ListQuery{}, app_errors.ErrBadRequest
		}
		if parsed < 1 || parsed > maxRequestLogLimit {
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
		query.Limit = int(parsed)
	}
	if value, ok := singleQueryValue(values, "cursor"); ok {
		cursor, err := decodeRequestLogCursor(value)
		if err != nil {
			return requestlog.ListQuery{}, app_errors.ErrBadRequest
		}
		query.Cursor = cursor
	}
	return query, nil
}

func singleQueryValue(values url.Values, key string) (string, bool) {
	value, ok := values[key]
	if !ok {
		return "", false
	}
	return value[0], true
}

func parseRequestLogID(value string) (uint, *app_errors.APIError) {
	parsed, err := parseCanonicalSafeUint(value)
	if err != nil {
		if errors.Is(err, errUnsafeCanonicalUint) {
			return 0, app_errors.ErrValidation
		}
		return 0, app_errors.ErrBadRequest
	}
	if parsed == 0 || uint64(uint(parsed)) != parsed {
		return 0, app_errors.ErrValidation
	}
	return uint(parsed), nil
}

func parseRequestLogBool(value string) (bool, bool) {
	switch value {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func parseRequestLogStatusCode(value string) (int, *app_errors.APIError) {
	parsed, err := parseCanonicalNonNegativeInt64(value)
	if err != nil {
		return 0, app_errors.ErrBadRequest
	}
	if parsed > 999 {
		return 0, app_errors.ErrValidation
	}
	return int(parsed), nil
}

func parseRequestLogIntRange(
	values url.Values,
	minimumKey string,
	maximumKey string,
	minimum **int,
	maximum **int,
) *app_errors.APIError {
	var parsedMinimum *int64
	var parsedMaximum *int64
	if apiErr := parseRequestLogInt64Range(
		values,
		minimumKey,
		maximumKey,
		&parsedMinimum,
		&parsedMaximum,
	); apiErr != nil {
		return apiErr
	}
	if parsedMinimum != nil {
		value := int(*parsedMinimum)
		if int64(value) != *parsedMinimum {
			return app_errors.ErrValidation
		}
		*minimum = &value
	}
	if parsedMaximum != nil {
		value := int(*parsedMaximum)
		if int64(value) != *parsedMaximum {
			return app_errors.ErrValidation
		}
		*maximum = &value
	}
	return nil
}

func parseRequestLogInt64Range(
	values url.Values,
	minimumKey string,
	maximumKey string,
	minimum **int64,
	maximum **int64,
) *app_errors.APIError {
	if value, ok := singleQueryValue(values, minimumKey); ok {
		parsed, err := parseCanonicalNonNegativeInt64(value)
		if err != nil {
			return app_errors.ErrBadRequest
		}
		*minimum = &parsed
	}
	if value, ok := singleQueryValue(values, maximumKey); ok {
		parsed, err := parseCanonicalNonNegativeInt64(value)
		if err != nil {
			return app_errors.ErrBadRequest
		}
		*maximum = &parsed
	}
	if *minimum != nil && *maximum != nil && **minimum > **maximum {
		return app_errors.ErrValidation
	}
	return nil
}

func parseCanonicalNonNegativeInt64(value string) (int64, error) {
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 || strconv.FormatInt(parsed, 10) != value {
		return 0, fmt.Errorf("value must be a canonical non-negative integer")
	}
	return parsed, nil
}

func decodeRequestLogCursor(encoded string) (*requestlog.Cursor, error) {
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("decode request log cursor base64: %w", err)
	}
	if encoded != base64.RawURLEncoding.EncodeToString(raw) {
		return nil, fmt.Errorf("decode request log cursor base64: non-canonical encoding")
	}
	payload, err := decodeRequestLogCursorPayload(raw)
	if err != nil {
		return nil, err
	}
	if payload.Version != requestLogCursorV2 {
		return nil, fmt.Errorf("unsupported request log cursor version")
	}
	if err := validateSafeMilliseconds(payload.CompletedAtMS); err != nil {
		return nil, fmt.Errorf("invalid request log cursor completed_at_ms: %w", err)
	}
	if !canonicalLowercaseUUIDv4.MatchString(payload.RequestID) {
		return nil, fmt.Errorf("invalid request log cursor request_id")
	}
	return &requestlog.Cursor{
		CompletedAtMS: payload.CompletedAtMS,
		RequestID:     payload.RequestID,
	}, nil
}

func decodeRequestLogCursorPayload(raw []byte) (requestLogCursorPayload, error) {
	if err := rejectDuplicateRequestLogCursorFields(raw); err != nil {
		return requestLogCursorPayload{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var payload requestLogCursorPayload
	if err := decoder.Decode(&payload); err != nil {
		return requestLogCursorPayload{}, fmt.Errorf("decode request log cursor JSON: %w", err)
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errorsIsEOF(err) {
		if err == nil {
			return requestLogCursorPayload{}, fmt.Errorf("decode request log cursor JSON: multiple values")
		}
		return requestLogCursorPayload{}, fmt.Errorf("decode request log cursor JSON: %w", err)
	}
	return payload, nil
}

func rejectDuplicateRequestLogCursorFields(raw []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil {
		return fmt.Errorf("decode request log cursor JSON: %w", err)
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return fmt.Errorf("request log cursor must be a JSON object")
	}
	seen := make(map[string]struct{}, 3)
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return fmt.Errorf("decode request log cursor field: %w", err)
		}
		key, ok := token.(string)
		if !ok {
			return fmt.Errorf("request log cursor field name is invalid")
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate request log cursor field %q", key)
		}
		seen[key] = struct{}{}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return fmt.Errorf("decode request log cursor field %q: %w", key, err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return fmt.Errorf("decode request log cursor JSON: %w", err)
	}
	return nil
}

func errorsIsEOF(err error) bool {
	return err == io.EOF
}

func encodeRequestLogCursor(cursor requestlog.Cursor) (string, error) {
	if err := validateSafeMilliseconds(cursor.CompletedAtMS); err != nil {
		return "", fmt.Errorf("encode request log cursor: invalid completed_at_ms: %w", err)
	}
	if !canonicalLowercaseUUIDv4.MatchString(cursor.RequestID) {
		return "", fmt.Errorf("encode request log cursor: invalid request ID")
	}
	payload := requestLogCursorPayload{
		Version:       requestLogCursorV2,
		CompletedAtMS: cursor.CompletedAtMS,
		RequestID:     cursor.RequestID,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("encode request log cursor JSON: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func mapRequestLogListResponse(page requestlog.Page) (requestLogListResponse, error) {
	result := requestLogListResponse{
		Items: make([]requestLogItemResponse, 0, len(page.Items)),
	}
	for _, record := range page.Items {
		if err := validateSafeMilliseconds(record.CompletedAtMS); err != nil {
			return requestLogListResponse{}, fmt.Errorf(
				"map request log completed_at_ms: %w",
				err,
			)
		}
		usageCost, err := mapRequestLogUsageCost(record)
		if err != nil {
			return requestLogListResponse{}, err
		}
		item, err := mapRequestLogItemResponse(record, usageCost)
		if err != nil {
			return requestLogListResponse{}, err
		}
		result.Items = append(result.Items, item)
	}
	if page.NextCursor != nil {
		encoded, err := encodeRequestLogCursor(*page.NextCursor)
		if err != nil {
			return requestLogListResponse{}, err
		}
		result.NextCursor = &encoded
	}
	return result, nil
}

func mapRequestLogItemResponse(
	record requestlog.Record,
	usageCost requestLogUsageCostResponse,
) (requestLogItemResponse, error) {
	inputTokens, ok := checkedRequestLogInputTokens(record)
	if !ok {
		return requestLogItemResponse{}, fmt.Errorf("map request log input tokens: overflow")
	}
	return requestLogItemResponse{
		RequestID:     record.RequestID,
		CompletedAtMS: record.CompletedAtMS,
		AccessKey: requestLogAccessKeyResponse{
			ID:      record.AccessKey.ID,
			Name:    record.AccessKey.Name,
			Deleted: record.AccessKey.Deleted,
		},
		Protocol:                string(record.Protocol),
		ClientModel:             nullableRequestLogModel(record.ClientModel),
		UpstreamModel:           nullableRequestLogModel(record.UpstreamModel),
		Reasoning:               mapRequestLogReasoning(record),
		Status:                  record.Status,
		StatusCode:              record.StatusCode,
		Stream:                  record.Stream,
		FirstResponseMs:         record.FirstResponseMs,
		DurationMs:              record.DurationMs,
		AttemptCount:            record.AttemptCount,
		ErrorCode:               record.ErrorCode,
		ErrorSummary:            record.ErrorSummary,
		AffinityHit:             record.AffinityHit,
		GroupID:                 usageCost.groupID,
		UsageState:              record.UsageState,
		CostState:               record.CostState,
		PricingCompleteness:     record.PricingCompleteness,
		InputTokens:             strconv.FormatInt(inputTokens, 10),
		CacheReadTokens:         strconv.FormatInt(record.CacheReadTokens, 10),
		CacheWrite5MTokens:      strconv.FormatInt(record.CacheWrite5MTokens, 10),
		CacheWrite1HTokens:      strconv.FormatInt(record.CacheWrite1HTokens, 10),
		CacheWriteUnknownTokens: strconv.FormatInt(record.CacheWriteUnknownTokens, 10),
		OutputTokens:            strconv.FormatInt(record.OutputTokens, 10),
		EstimatedCostNanoUSD:    usageCost.estimatedCostNanoUSD,
	}, nil
}

func mapRequestLogReasoning(record requestlog.Record) *requestLogReasoningResponse {
	if !record.Reasoning.Present() {
		return nil
	}
	result := &requestLogReasoningResponse{
		Mode:   nullableRequestLogReasoningValue(record.Reasoning.Mode),
		Effort: nullableRequestLogReasoningValue(record.Reasoning.Effort),
	}
	if record.Reasoning.BudgetTokens != nil {
		value := strconv.FormatInt(*record.Reasoning.BudgetTokens, 10)
		result.BudgetTokens = &value
	}
	return result
}

func nullableRequestLogReasoningValue(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func checkedRequestLogInputTokens(record requestlog.Record) (int64, bool) {
	total := int64(0)
	for _, value := range []int64{
		record.UncachedInputTokens,
		record.CacheReadTokens,
		record.CacheWrite5MTokens,
		record.CacheWrite1HTokens,
		record.CacheWriteUnknownTokens,
	} {
		var ok bool
		total, ok = usage.CheckedAdd(total, value)
		if !ok {
			return 0, false
		}
	}
	return total, true
}

func mapRequestLogDetailResponse(record requestlog.Record) (requestLogDetailResponse, error) {
	usageCost, err := mapRequestLogUsageCost(record)
	if err != nil {
		return requestLogDetailResponse{}, err
	}
	item, err := mapRequestLogItemResponse(record, usageCost)
	if err != nil {
		return requestLogDetailResponse{}, err
	}
	attempts := make([]requestLogAttemptResponse, 0, len(record.Attempts))
	for _, attempt := range record.Attempts {
		receipt, err := mapRequestLogPricingReceipt(attempt.PricingReceipt)
		if err != nil {
			return requestLogDetailResponse{}, err
		}
		attempts = append(attempts, requestLogAttemptResponse{
			Sequence:        attempt.Sequence,
			GroupID:         attempt.GroupID,
			GroupName:       attempt.GroupName,
			KeyID:           attempt.KeyID,
			UpstreamModel:   nullableRequestLogModel(attempt.UpstreamModel),
			StatusCode:      attempt.StatusCode,
			DurationMs:      attempt.DurationMs,
			FailureCategory: attempt.FailureCategory,
			Action:          attempt.Action,
			WillRetry:       attempt.WillRetry,
			ErrorCode:       attempt.ErrorCode,
			ErrorSummary:    attempt.ErrorSummary,
			PricingReceipt:  receipt,
		})
	}
	return requestLogDetailResponse{requestLogItemResponse: item, Attempts: attempts}, nil
}

func mapRequestLogPricingReceipt(
	receipt *pricing.Receipt,
) (*requestLogPricingReceiptResponse, error) {
	if receipt == nil {
		return nil, nil
	}
	if err := pricing.ValidateReceipt(*receipt); err != nil {
		return nil, fmt.Errorf("map request log pricing receipt: %w", err)
	}
	result := &requestLogPricingReceiptResponse{
		SchemaVersion: receipt.SchemaVersion,
		Method:        receipt.Method,
		MethodVersion: receipt.MethodVersion,
		Currency:      receipt.Currency,
		Rule:          requestLogPricingIdentityResponse{ModelID: receipt.Rule.ModelID},
		LineItems:     make([]requestLogPricingLineResponse, 0, len(receipt.LineItems)),
		TotalNanoUSD:  strconv.FormatInt(receipt.TotalNanoUSD, 10),
	}
	if receipt.SchemaVersion == 1 {
		scopeKey := receipt.Rule.ScopeKey
		result.Rule.ScopeKey = &scopeKey
	}
	if receipt.ContextThresholdTokens != nil {
		value := strconv.FormatInt(*receipt.ContextThresholdTokens, 10)
		result.ContextThresholdTokens = &value
	}
	for _, line := range receipt.LineItems {
		mapped := requestLogPricingLineResponse{
			Code:     line.Code,
			Quantity: strconv.FormatInt(line.Quantity, 10),
			Multiplier: requestLogPricingMultiplierResponse{
				Numerator:   strconv.FormatInt(line.Multiplier.Numerator, 10),
				Denominator: strconv.FormatInt(line.Multiplier.Denominator, 10),
			},
			State: line.State,
		}
		if line.RateNanoUSDPerMillion != nil {
			value := strconv.FormatInt(*line.RateNanoUSDPerMillion, 10)
			mapped.RateNanoUSDPerMillion = &value
		}
		if line.AmountNanoUSD != nil {
			value := strconv.FormatInt(*line.AmountNanoUSD, 10)
			mapped.AmountNanoUSD = &value
		}
		result.LineItems = append(result.LineItems, mapped)
	}
	return result, nil
}

func nullableRequestLogModel(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

type requestLogUsageCostResponse struct {
	groupID              *uint
	estimatedCostNanoUSD string
}

func mapRequestLogUsageCost(record requestlog.Record) (requestLogUsageCostResponse, error) {
	if err := requestlog.ValidateUsageCostState(
		record.UsageState,
		record.CostState,
		record.PricingCompleteness,
		record.EstimatedCostNanoUSD,
	); err != nil {
		return requestLogUsageCostResponse{}, fmt.Errorf("map request log usage/cost: %w", err)
	}
	for _, value := range []int64{
		record.UncachedInputTokens,
		record.CacheReadTokens,
		record.CacheWrite5MTokens,
		record.CacheWrite1HTokens,
		record.CacheWriteUnknownTokens,
		record.OutputTokens,
	} {
		if value < 0 {
			return requestLogUsageCostResponse{}, fmt.Errorf("map request log usage tokens: negative value")
		}
	}
	if _, ok := usage.CheckedTotal(usage.Tokens{
		UncachedInput:     record.UncachedInputTokens,
		CacheRead:         record.CacheReadTokens,
		CacheWrite5M:      record.CacheWrite5MTokens,
		CacheWrite1H:      record.CacheWrite1HTokens,
		CacheWriteUnknown: record.CacheWriteUnknownTokens,
		Output:            record.OutputTokens,
	}); !ok {
		return requestLogUsageCostResponse{}, fmt.Errorf("map request log usage tokens: unsafe total")
	}
	if uint64(record.GroupID) > uint64(maxSafeInteger) {
		return requestLogUsageCostResponse{}, fmt.Errorf("map request log final Group ID: unsafe value")
	}
	if record.EstimatedCostNanoUSD < 0 {
		return requestLogUsageCostResponse{}, fmt.Errorf(
			"map request log usage/cost: negative estimated cost",
		)
	}
	result := requestLogUsageCostResponse{
		estimatedCostNanoUSD: strconv.FormatInt(record.EstimatedCostNanoUSD, 10),
	}
	if record.GroupID != 0 {
		groupID := record.GroupID
		result.groupID = &groupID
	}
	return result, nil
}
