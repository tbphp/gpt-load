package requestlog

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/reasoning"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

const defaultListLimit = 50

func (service *Service) List(ctx context.Context, input ListQuery) (Page, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}

	query := service.db.WithContext(ctx).
		Model(&models.RequestLog{}).
		Order("completed_at_ms DESC").
		Order("id DESC").
		Limit(limit + 1)
	if input.FromMS != nil {
		query = query.Where("completed_at_ms >= ?", *input.FromMS)
	}
	if input.ToMS != nil {
		query = query.Where("completed_at_ms < ?", *input.ToMS)
	}
	if input.ClientModel != "" {
		query = query.Where("client_model = ?", input.ClientModel)
	}
	if input.AccessKeyID != nil {
		query = query.Where("access_key_id = ?", *input.AccessKeyID)
	}
	if input.Status != "" {
		query = query.Where("status = ?", input.Status)
	}
	if input.RequestID != "" {
		query = query.Where("id = ?", input.RequestID)
	}
	if input.Protocol != "" {
		query = query.Where("protocol = ?", input.Protocol)
	}
	if input.Stream != nil {
		query = query.Where("stream = ?", *input.Stream)
	}
	if input.FinalStatusCode != nil {
		query = query.Where("status_code = ?", *input.FinalStatusCode)
	}
	if input.UsageState != "" {
		query = query.Where("usage_state = ?", input.UsageState)
	}
	if input.CostState != "" {
		query = query.Where("cost_state = ?", input.CostState)
	}
	if input.PricingCompleteness != "" {
		query = query.Where("pricing_completeness = ?", input.PricingCompleteness)
	}
	if input.CachePresent != nil {
		expression := `(cache_read_tokens + cache_write_5m_tokens + cache_write_1h_tokens + cache_write_unknown_tokens) > 0`
		if !*input.CachePresent {
			expression = `(cache_read_tokens + cache_write_5m_tokens + cache_write_1h_tokens + cache_write_unknown_tokens) = 0`
		}
		query = query.Where(expression)
	}
	if input.RetryState == RetryStateRetried {
		query = query.Where("attempt_count > 1")
	} else if input.RetryState == RetryStateNotRetried {
		query = query.Where("attempt_count <= 1")
	}
	retryCountExpression := "CASE WHEN attempt_count > 1 THEN attempt_count - 1 ELSE 0 END"
	if input.RetryCountMin != nil {
		query = query.Where(retryCountExpression+" >= ?", *input.RetryCountMin)
	}
	if input.RetryCountMax != nil {
		query = query.Where(retryCountExpression+" <= ?", *input.RetryCountMax)
	}
	query = applyNullableRange(query, "first_response_ms", input.FirstResponseMinMS, input.FirstResponseMaxMS)
	query = applyNullableRange(query, "duration_ms", input.DurationMinMS, input.DurationMaxMS)
	query = applyNullableRange(
		query,
		"(uncached_input_tokens + cache_read_tokens + cache_write_5m_tokens + cache_write_1h_tokens + cache_write_unknown_tokens)",
		input.InputTokensMin,
		input.InputTokensMax,
	)
	query = applyNullableRange(query, "output_tokens", input.OutputTokensMin, input.OutputTokensMax)
	query = applyNullableRange(query, "estimated_cost_nano_usd", input.CostMinNanoUSD, input.CostMaxNanoUSD)
	query = applyAttemptFilters(query, input)
	if input.Cursor != nil {
		query = query.Where(
			"completed_at_ms < ? OR (completed_at_ms = ? AND id < ?)",
			input.Cursor.CompletedAtMS,
			input.Cursor.CompletedAtMS,
			input.Cursor.RequestID,
		)
	}

	var rows []models.RequestLog
	if err := query.Find(&rows).Error; err != nil {
		return Page{}, fmt.Errorf("query request logs: %w", err)
	}

	hasNext := len(rows) > limit
	if hasNext {
		rows = rows[:limit]
	}
	records, err := decodeRequestLogRows(rows)
	if err != nil {
		return Page{}, err
	}
	if err := service.loadAccessKeyRefs(ctx, records); err != nil {
		return Page{}, err
	}
	if err := service.loadFinalExecutionObservations(ctx, records); err != nil {
		return Page{}, err
	}

	page := Page{Items: records}
	if hasNext {
		last := records[len(records)-1]
		page.NextCursor = &Cursor{
			CompletedAtMS: last.CompletedAtMS,
			RequestID:     last.RequestID,
		}
	}
	return page, nil
}

func applyNullableRange[T int | int64](
	query *gorm.DB,
	expression string,
	minimum *T,
	maximum *T,
) *gorm.DB {
	if minimum != nil {
		query = query.Where(expression+" >= ?", *minimum)
	}
	if maximum != nil {
		query = query.Where(expression+" <= ?", *maximum)
	}
	return query
}

func applyAttemptFilters(query *gorm.DB, input ListQuery) *gorm.DB {
	conditions := make([]string, 0, 8)
	arguments := make([]any, 0, 8)
	if input.GroupID != nil {
		conditions = append(conditions, "attempt.group_id = ?")
		arguments = append(arguments, *input.GroupID)
	}
	if input.ChannelID != "" {
		conditions = append(conditions, "attempt.channel_id = ?")
		arguments = append(arguments, input.ChannelID)
	}
	if input.CredentialID != nil {
		conditions = append(conditions, "attempt.credential_id = ?")
		arguments = append(arguments, *input.CredentialID)
	}
	if input.UpstreamModel != "" {
		conditions = append(conditions, "attempt.upstream_model = ?")
		arguments = append(arguments, input.UpstreamModel)
	}
	if input.AttemptStatusCode != nil {
		conditions = append(conditions, "attempt.status_code = ?")
		arguments = append(arguments, *input.AttemptStatusCode)
	}
	if input.FailureCategory != "" {
		conditions = append(conditions, "attempt.failure_category = ?")
		arguments = append(arguments, input.FailureCategory)
	}
	if input.AttemptErrorCode != "" {
		conditions = append(conditions, "attempt.error_code = ?")
		arguments = append(arguments, input.AttemptErrorCode)
	}
	if len(conditions) == 0 {
		return query
	}
	statement := `EXISTS (
		SELECT 1 FROM request_log_attempts AS attempt
		WHERE attempt.request_id = request_logs.id AND ` +
		strings.Join(conditions, " AND ") + `
	)`
	return query.Where(statement, arguments...)
}

// Get returns one request with all retry attempts and its frozen pricing receipt.
func (service *Service) Get(ctx context.Context, requestID string) (Record, error) {
	var record Record
	err := service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var row models.RequestLog
		if err := tx.Where("id = ?", requestID).First(&row).Error; err != nil {
			return fmt.Errorf("query request log detail: %w", err)
		}
		records, err := decodeRequestLogRows([]models.RequestLog{row})
		if err != nil {
			return err
		}
		if err := loadAccessKeyRefs(ctx, tx, records); err != nil {
			return err
		}

		var attemptRows []models.RequestLogAttempt
		if err := tx.Where("request_id = ?", requestID).
			Order("sequence ASC").
			Find(&attemptRows).Error; err != nil {
			return fmt.Errorf("query request log attempts: %w", err)
		}
		attempts, err := decodeAttemptRows(attemptRows)
		if err != nil {
			return err
		}
		records[0].Attempts = attempts
		records[0].RouteMode = finalRouteMode(records[0], attempts)
		records[0].UpstreamAPI = finalUpstreamAPI(records[0], attempts)
		record = records[0]
		return nil
	})
	if err != nil {
		return Record{}, err
	}
	return record, nil
}

func decodeAttemptRows(rows []models.RequestLogAttempt) ([]Attempt, error) {
	attempts := make([]Attempt, 0, len(rows))
	for _, row := range rows {
		var receipt *pricing.Receipt
		if len(row.PricingReceipt) > 0 && string(row.PricingReceipt) != "null" {
			var decoded pricing.Receipt
			if err := json.Unmarshal(row.PricingReceipt, &decoded); err != nil {
				return nil, fmt.Errorf("decode request log pricing receipt: %w", err)
			}
			if err := pricing.ValidateReceipt(decoded); err != nil {
				return nil, fmt.Errorf("decode request log pricing receipt: %w", err)
			}
			if decoded.SchemaVersion == 3 && decoded.Rule != (pricing.ReceiptRule{
				ChannelID: row.ChannelID,
				ModelID:   row.UpstreamModel,
			}) {
				return nil, fmt.Errorf("decode request log pricing receipt: v3 identity does not match attempt")
			}
			receipt = &decoded
		}
		attempts = append(attempts, Attempt{
			Sequence:          row.Sequence,
			GroupID:           row.GroupID,
			GroupName:         row.GroupName,
			ChannelID:         channel.ID(row.ChannelID),
			CredentialID:      row.CredentialID,
			Operation:         execution.Operation(row.Operation),
			RouteMode:         channel.RouteMode(row.RouteMode),
			UpstreamModel:     row.UpstreamModel,
			UpstreamRequestID: row.UpstreamRequestID,
			DispatchState:     execution.DispatchState(row.DispatchState),
			ResponseStarted:   row.ResponseStarted,
			UpstreamAPI:       execution.UpstreamAPI(row.UpstreamAPI),
			Reasoning: reasoning.Config{
				Mode:         row.ReasoningMode,
				Effort:       row.ReasoningEffort,
				BudgetTokens: row.ReasoningBudgetTokens,
			},
			StatusCode:      row.StatusCode,
			DurationMs:      row.DurationMs,
			FailureCategory: telemetry.FailureCategory(row.FailureCategory),
			Action:          telemetry.Action(row.Action),
			WillRetry:       row.WillRetry,
			ErrorCode:       row.ErrorCode,
			ErrorSummary:    row.ErrorSummary,
			Committed:       row.Committed,
			PricingReceipt:  receipt,
		})
	}
	return attempts, nil
}

func decodeRequestLogRows(rows []models.RequestLog) ([]Record, error) {
	records := make([]Record, 0, len(rows))
	for _, row := range rows {
		if err := validateStoredModelObservation(row); err != nil {
			return nil, err
		}
		if err := validateRequestLogUsageCost(row); err != nil {
			return nil, err
		}
		records = append(records, Record{
			RequestID:             row.ID,
			CompletedAtMS:         row.CompletedAtMS,
			AccessKey:             AccessKeyRef{ID: row.AccessKeyID, Deleted: true},
			Protocol:              protocol.Protocol(row.Protocol),
			Operation:             execution.Operation(row.Operation),
			ClientModel:           row.ClientModel,
			UpstreamModel:         row.UpstreamModel,
			UpstreamReportedModel: row.UpstreamReportedModel,
			ModelConsistency:      telemetry.ModelConsistency(row.ModelConsistency),
			Status:                telemetry.RequestStatus(row.Status),
			StatusCode:            row.StatusCode,
			Stream:                row.Stream,
			FirstResponseMs:       row.FirstResponseMs,
			DurationMs:            row.DurationMs,
			AttemptCount:          row.AttemptCount,
			ErrorCode:             row.ErrorCode,
			ErrorSummary:          row.ErrorSummary,
			AffinityHit:           row.AffinityHit,
			Reasoning: reasoning.Config{
				Mode:         row.ReasoningMode,
				Effort:       row.ReasoningEffort,
				BudgetTokens: row.ReasoningBudgetTokens,
			},
			Attempts:                nil,
			GroupID:                 row.GroupID,
			ChannelID:               channel.ID(row.ChannelID),
			CredentialID:            row.CredentialID,
			UsageState:              usage.State(row.UsageState),
			CostState:               pricing.CostState(row.CostState),
			PricingCompleteness:     pricing.Completeness(row.PricingCompleteness),
			UncachedInputTokens:     row.UncachedInputTokens,
			CacheReadTokens:         row.CacheReadTokens,
			CacheWrite5MTokens:      row.CacheWrite5MTokens,
			CacheWrite1HTokens:      row.CacheWrite1HTokens,
			CacheWriteUnknownTokens: row.CacheWriteUnknownTokens,
			OutputTokens:            row.OutputTokens,
			EstimatedCostNanoUSD:    row.EstimatedCostNanoUSD,
		})
	}
	return records, nil
}

func validateStoredModelObservation(row models.RequestLog) error {
	successfulModeledRequest := row.Status == string(telemetry.RequestStatusSuccess) &&
		row.UpstreamModel != ""
	reported := row.UpstreamReportedModel != ""

	switch telemetry.ModelConsistency(row.ModelConsistency) {
	case telemetry.ModelConsistencyNotApplicable:
		if reported || successfulModeledRequest {
			return fmt.Errorf("decode request log model consistency: invalid not-applicable observation")
		}
	case telemetry.ModelConsistencyUnknown:
		if !successfulModeledRequest || reported {
			return fmt.Errorf("decode request log model consistency: invalid unknown observation")
		}
	case telemetry.ModelConsistencyMatch, telemetry.ModelConsistencyMismatch:
		if !successfulModeledRequest || !reported {
			return fmt.Errorf("decode request log model consistency: invalid observed model")
		}
	default:
		return fmt.Errorf(
			"decode request log model consistency: invalid state %q",
			row.ModelConsistency,
		)
	}
	return nil
}

func validateRequestLogUsageCost(row models.RequestLog) error {
	if err := validateFrozenPricingState(
		usage.State(row.UsageState),
		pricing.CostState(row.CostState),
		pricing.Completeness(row.PricingCompleteness),
		row.EstimatedCostNanoUSD,
	); err != nil {
		return fmt.Errorf("decode request log usage/cost: %w", err)
	}
	for _, value := range []int64{
		row.UncachedInputTokens,
		row.CacheReadTokens,
		row.CacheWrite5MTokens,
		row.CacheWrite1HTokens,
		row.CacheWriteUnknownTokens,
		row.OutputTokens,
	} {
		if value < 0 {
			return fmt.Errorf("decode request log usage tokens: negative value")
		}
	}
	if _, ok := usage.CheckedTotal(usage.Tokens{
		UncachedInput:     row.UncachedInputTokens,
		CacheRead:         row.CacheReadTokens,
		CacheWrite5M:      row.CacheWrite5MTokens,
		CacheWrite1H:      row.CacheWrite1HTokens,
		CacheWriteUnknown: row.CacheWriteUnknownTokens,
		Output:            row.OutputTokens,
	}); !ok {
		return fmt.Errorf("decode request log usage tokens: total overflow")
	}
	return nil
}

func (service *Service) loadAccessKeyRefs(ctx context.Context, records []Record) error {
	return loadAccessKeyRefs(ctx, service.db, records)
}

func (service *Service) loadFinalExecutionObservations(
	ctx context.Context,
	records []Record,
) error {
	if len(records) == 0 {
		return nil
	}
	requestIDs := make([]string, 0, len(records))
	recordIndexes := make(map[string]int, len(records))
	for index, record := range records {
		requestIDs = append(requestIDs, record.RequestID)
		recordIndexes[record.RequestID] = index
	}

	var attempts []models.RequestLogAttempt
	if err := service.db.WithContext(ctx).
		Select("request_id", "sequence", "group_id", "channel_id", "credential_id", "route_mode", "upstream_api").
		Where("request_id IN ?", requestIDs).
		Order("request_id ASC").
		Order("sequence DESC").
		Find(&attempts).Error; err != nil {
		return fmt.Errorf("query request log final execution observations: %w", err)
	}
	resolved := make(map[string]struct{}, len(records))
	for _, attempt := range attempts {
		index, ok := recordIndexes[attempt.RequestID]
		if !ok {
			continue
		}
		if _, ok := resolved[attempt.RequestID]; ok {
			continue
		}
		record := records[index]
		if record.GroupID != attempt.GroupID ||
			record.ChannelID != channel.ID(attempt.ChannelID) ||
			record.CredentialID != attempt.CredentialID {
			continue
		}
		resolved[attempt.RequestID] = struct{}{}
		mode := channel.RouteMode(attempt.RouteMode)
		if mode != "" && !mode.Valid() {
			return fmt.Errorf("query request log final execution observations: invalid route mode")
		}
		records[index].RouteMode = mode
		upstreamAPI := execution.UpstreamAPI(attempt.UpstreamAPI)
		if upstreamAPI != "" && !upstreamAPI.Valid() {
			return fmt.Errorf("query request log final execution observations: invalid upstream API")
		}
		records[index].UpstreamAPI = upstreamAPI
	}
	return nil
}

func finalRouteMode(record Record, attempts []Attempt) channel.RouteMode {
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := attempts[index]
		if attempt.GroupID == record.GroupID &&
			attempt.ChannelID == record.ChannelID &&
			attempt.CredentialID == record.CredentialID {
			return attempt.RouteMode
		}
	}
	return ""
}

func finalUpstreamAPI(record Record, attempts []Attempt) execution.UpstreamAPI {
	for index := len(attempts) - 1; index >= 0; index-- {
		attempt := attempts[index]
		if attempt.GroupID == record.GroupID &&
			attempt.ChannelID == record.ChannelID &&
			attempt.CredentialID == record.CredentialID {
			return attempt.UpstreamAPI
		}
	}
	return ""
}

func loadAccessKeyRefs(ctx context.Context, db *gorm.DB, records []Record) error {
	if len(records) == 0 {
		return nil
	}

	ids := make([]uint, 0, len(records))
	seen := make(map[uint]struct{}, len(records))
	for _, record := range records {
		if _, ok := seen[record.AccessKey.ID]; ok {
			continue
		}
		seen[record.AccessKey.ID] = struct{}{}
		ids = append(ids, record.AccessKey.ID)
	}

	var accessKeys []struct {
		ID   uint
		Name string
	}
	if err := db.WithContext(ctx).
		Model(&models.AccessKey{}).
		Select("id", "name").
		Where("id IN ?", ids).
		Find(&accessKeys).Error; err != nil {
		return fmt.Errorf("query request log access keys: %w", err)
	}
	refs := make(map[uint]AccessKeyRef, len(accessKeys))
	for _, accessKey := range accessKeys {
		name := accessKey.Name
		refs[accessKey.ID] = AccessKeyRef{
			ID:      accessKey.ID,
			Name:    &name,
			Deleted: false,
		}
	}
	for index := range records {
		if ref, ok := refs[records[index].AccessKey.ID]; ok {
			records[index].AccessKey = ref
		}
	}
	return nil
}
