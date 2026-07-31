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

type requestLogAttemptResponse struct {
	Sequence        int                       `json:"sequence"`
	GroupID         uint                      `json:"group_id"`
	GroupName       string                    `json:"group_name"`
	KeyID           uint                      `json:"key_id"`
	UpstreamModel   *string                   `json:"upstream_model"`
	StatusCode      int                       `json:"status_code"`
	DurationMs      int64                     `json:"duration_ms"`
	FailureCategory telemetry.FailureCategory `json:"failure_category"`
	Action          telemetry.Action          `json:"action"`
	WillRetry       bool                      `json:"will_retry"`
	ErrorCode       string                    `json:"error_code"`
	ErrorSummary    string                    `json:"error_summary"`
	Committed       bool                      `json:"committed"`
}

type requestLogItemResponse struct {
	RequestID            string                      `json:"request_id"`
	CompletedAtMS        int64                       `json:"completed_at_ms"`
	AccessKey            requestLogAccessKeyResponse `json:"access_key"`
	Protocol             string                      `json:"protocol"`
	ClientModel          *string                     `json:"client_model"`
	UpstreamModel        *string                     `json:"upstream_model"`
	Status               telemetry.RequestStatus     `json:"status"`
	StatusCode           int                         `json:"status_code"`
	DurationMs           int64                       `json:"duration_ms"`
	ErrorCode            string                      `json:"error_code"`
	ErrorSummary         string                      `json:"error_summary"`
	AffinityHit          bool                        `json:"affinity_hit"`
	Attempts             []requestLogAttemptResponse `json:"attempts"`
	GroupID              *uint                       `json:"group_id"`
	UsageState           usage.State                 `json:"usage_state"`
	CostState            pricing.CostState           `json:"cost_state"`
	UncachedInputTokens  int64                       `json:"uncached_input_tokens"`
	CacheReadTokens      int64                       `json:"cache_read_tokens"`
	CacheWrite5MTokens   int64                       `json:"cache_write_5m_tokens"`
	CacheWrite1HTokens   int64                       `json:"cache_write_1h_tokens"`
	OutputTokens         int64                       `json:"output_tokens"`
	EstimatedCostNanoUSD string                      `json:"estimated_cost_nano_usd"`
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

func parseRequestLogQuery(rawQuery string) (requestlog.ListQuery, *app_errors.APIError) {
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		return requestlog.ListQuery{}, app_errors.ErrBadRequest
	}
	allowed := map[string]struct{}{
		"from_ms": {}, "to_ms": {}, "group_id": {}, "model": {}, "access_key_id": {},
		"status": {}, "request_id": {}, "limit": {}, "cursor": {},
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
	if value, ok := singleQueryValue(values, "model"); ok {
		if value == "" {
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
		query.ClientModel = value
	}
	if value, ok := singleQueryValue(values, "access_key_id"); ok {
		parsed, apiErr := parseRequestLogID(value)
		if apiErr != nil {
			return requestlog.ListQuery{}, apiErr
		}
		query.AccessKeyID = &parsed
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
		attempts := make([]requestLogAttemptResponse, 0, len(record.Attempts))
		for _, attempt := range record.Attempts {
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
				Committed:       attempt.Committed,
			})
		}
		result.Items = append(result.Items, requestLogItemResponse{
			RequestID:     record.RequestID,
			CompletedAtMS: record.CompletedAtMS,
			AccessKey: requestLogAccessKeyResponse{
				ID:      record.AccessKey.ID,
				Name:    record.AccessKey.Name,
				Deleted: record.AccessKey.Deleted,
			},
			Protocol:             string(record.Protocol),
			ClientModel:          nullableRequestLogModel(record.ClientModel),
			UpstreamModel:        nullableRequestLogModel(record.UpstreamModel),
			Status:               record.Status,
			StatusCode:           record.StatusCode,
			DurationMs:           record.DurationMs,
			ErrorCode:            record.ErrorCode,
			ErrorSummary:         record.ErrorSummary,
			AffinityHit:          record.AffinityHit,
			Attempts:             attempts,
			GroupID:              usageCost.groupID,
			UsageState:           record.UsageState,
			CostState:            record.CostState,
			UncachedInputTokens:  record.UncachedInputTokens,
			CacheReadTokens:      record.CacheReadTokens,
			CacheWrite5MTokens:   record.CacheWrite5MTokens,
			CacheWrite1HTokens:   record.CacheWrite1HTokens,
			OutputTokens:         record.OutputTokens,
			EstimatedCostNanoUSD: usageCost.estimatedCostNanoUSD,
		})
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
		record.EstimatedCostNanoUSD,
	); err != nil {
		return requestLogUsageCostResponse{}, fmt.Errorf("map request log usage/cost: %w", err)
	}
	for _, value := range []int64{
		record.UncachedInputTokens,
		record.CacheReadTokens,
		record.CacheWrite5MTokens,
		record.CacheWrite1HTokens,
		record.OutputTokens,
	} {
		if value < 0 || value > maxSafeInteger {
			return requestLogUsageCostResponse{}, fmt.Errorf("map request log usage tokens: unsafe value")
		}
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
