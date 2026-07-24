package control

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	app_errors "gpt-load/internal/platform/errors"
	"gpt-load/internal/platform/response"
	"gpt-load/internal/requestlog"
	"gpt-load/internal/telemetry"
)

const (
	defaultRequestLogLimit = 50
	maxRequestLogLimit     = 200
	requestLogCursorV1     = 1
)

var canonicalLowercaseUUIDv4 = regexp.MustCompile(
	`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`,
)

type RequestLogReader interface {
	List(context.Context, requestlog.ListQuery) (requestlog.Page, error)
}

type requestLogCursorPayload struct {
	Version     int    `json:"v"`
	CompletedAt string `json:"completed_at"`
	RequestID   string `json:"request_id"`
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
	KeyMask         string                    `json:"key_mask"`
	UpstreamModel   string                    `json:"upstream_model"`
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
	RequestID     string                      `json:"request_id"`
	CompletedAt   string                      `json:"completed_at"`
	AccessKey     requestLogAccessKeyResponse `json:"access_key"`
	Protocol      string                      `json:"protocol"`
	ClientModel   string                      `json:"client_model"`
	UpstreamModel string                      `json:"upstream_model"`
	Status        telemetry.RequestStatus     `json:"status"`
	StatusCode    int                         `json:"status_code"`
	DurationMs    int64                       `json:"duration_ms"`
	ErrorCode     string                      `json:"error_code"`
	ErrorSummary  string                      `json:"error_summary"`
	AffinityHit   bool                        `json:"affinity_hit"`
	Attempts      []requestLogAttemptResponse `json:"attempts"`
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
		"from": {}, "to": {}, "group_id": {}, "model": {}, "access_key_id": {},
		"status": {}, "request_id": {}, "limit": {}, "cursor": {},
	}
	for key, value := range values {
		if _, ok := allowed[key]; !ok || len(value) != 1 {
			return requestlog.ListQuery{}, app_errors.ErrBadRequest
		}
	}

	query := requestlog.ListQuery{Limit: defaultRequestLogLimit}
	if value, ok := singleQueryValue(values, "from"); ok {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return requestlog.ListQuery{}, app_errors.ErrBadRequest
		}
		parsed = parsed.UTC()
		query.From = &parsed
	}
	if value, ok := singleQueryValue(values, "to"); ok {
		parsed, err := time.Parse(time.RFC3339Nano, value)
		if err != nil {
			return requestlog.ListQuery{}, app_errors.ErrBadRequest
		}
		parsed = parsed.UTC()
		query.To = &parsed
	}
	if query.From != nil && query.To != nil && !query.From.Before(*query.To) {
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
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return requestlog.ListQuery{}, app_errors.ErrBadRequest
		}
		if parsed < 1 || parsed > maxRequestLogLimit {
			return requestlog.ListQuery{}, app_errors.ErrValidation
		}
		query.Limit = parsed
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
	parsed, err := strconv.ParseUint(value, 10, strconv.IntSize)
	if err != nil {
		return 0, app_errors.ErrBadRequest
	}
	if parsed == 0 {
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
	if payload.Version != requestLogCursorV1 {
		return nil, fmt.Errorf("unsupported request log cursor version")
	}
	completedAt, err := time.Parse(time.RFC3339Nano, payload.CompletedAt)
	if err != nil || payload.CompletedAt != completedAt.UTC().Format(time.RFC3339Nano) {
		return nil, fmt.Errorf("invalid request log cursor completed_at")
	}
	if !canonicalLowercaseUUIDv4.MatchString(payload.RequestID) {
		return nil, fmt.Errorf("invalid request log cursor request_id")
	}
	return &requestlog.Cursor{
		CompletedAt: completedAt.UTC(),
		RequestID:   payload.RequestID,
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
	completedAt := cursor.CompletedAt.UTC()
	if !canonicalLowercaseUUIDv4.MatchString(cursor.RequestID) {
		return "", fmt.Errorf("encode request log cursor: invalid request ID")
	}
	payload := requestLogCursorPayload{
		Version:     requestLogCursorV1,
		CompletedAt: completedAt.Format(time.RFC3339Nano),
		RequestID:   cursor.RequestID,
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
		attempts := make([]requestLogAttemptResponse, 0, len(record.Attempts))
		for _, attempt := range record.Attempts {
			attempts = append(attempts, requestLogAttemptResponse{
				Sequence:        attempt.Sequence,
				GroupID:         attempt.GroupID,
				GroupName:       attempt.GroupName,
				KeyID:           attempt.KeyID,
				KeyMask:         attempt.KeyMask,
				UpstreamModel:   attempt.UpstreamModel,
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
			RequestID:   record.RequestID,
			CompletedAt: record.CompletedAt.UTC().Format(time.RFC3339Nano),
			AccessKey: requestLogAccessKeyResponse{
				ID:      record.AccessKey.ID,
				Name:    record.AccessKey.Name,
				Deleted: record.AccessKey.Deleted,
			},
			Protocol:      string(record.Protocol),
			ClientModel:   record.ClientModel,
			UpstreamModel: record.UpstreamModel,
			Status:        record.Status,
			StatusCode:    record.StatusCode,
			DurationMs:    record.DurationMs,
			ErrorCode:     record.ErrorCode,
			ErrorSummary:  record.ErrorSummary,
			AffinityHit:   record.AffinityHit,
			Attempts:      attempts,
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
