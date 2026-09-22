package requestlog

import (
	"fmt"

	"gorm.io/gorm"

	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/storage/models"
)

func writeAuditUsage(tx *gorm.DB, rows []models.RequestLog) error {
	var calls []models.RequestAuditUsage
	for _, row := range rows {
		calls = append(calls, row.AuditUsageRows...)
	}
	if len(calls) == 0 {
		return nil
	}
	if err := tx.CreateInBatches(calls, batchSize).Error; err != nil {
		return fmt.Errorf("save audit usage: %w", err)
	}
	for _, call := range calls {
		if err := addDecisionUsageStat(tx, models.AutoDecisionUsageStat{
			BucketStartMS: call.CompletedAtMS - call.CompletedAtMS%epochms.MillisecondsPerHour, AccessKeyID: call.AccessKeyID, GroupID: call.GroupID, ChannelID: call.ChannelID, CredentialID: call.CredentialID, Model: call.Model,
			EstimatedCostNanoUSD: call.EstimatedCostNanoUSD, UnpricedRequestCount: call.UnpricedRequestCount, PricingPartialCount: call.PricingPartialCount,
		}); err != nil {
			return err
		}
	}
	return nil
}

func auditRequestScope(db *gorm.DB, input UsageQuery, groupIDs ...uint) *gorm.DB {
	scope := db.Session(&gorm.Session{NewDB: true}).Model(&models.RequestAuditUsage{}).Where("completed_at_ms >= ? AND completed_at_ms < ?", input.FromMS, input.ToMS)
	if len(groupIDs) > 0 {
		scope = scope.Where("group_id IN ?", groupIDs)
	}
	if input.GroupID != nil {
		scope = scope.Where("group_id = ?", *input.GroupID)
	}
	if input.ChannelID != "" {
		scope = scope.Where("channel_id = ?", input.ChannelID)
	}
	if input.CredentialID != nil {
		scope = scope.Where("credential_id = ?", *input.CredentialID)
	}
	if input.AccessKeyID != nil {
		scope = scope.Where("access_key_id = ?", *input.AccessKeyID)
	}
	if input.UpstreamModel != "" {
		scope = scope.Where("model = ?", input.UpstreamModel)
	}
	return scope.Select(`completed_at_ms - completed_at_ms % ? AS bucket_start_ms,
 group_id, access_key_id, model, 0 AS request_count, 0 AS success_count, 0 AS failure_count,
 0 AS uncached_input_tokens, 0 AS cache_read_tokens, 0 AS cache_write_5m_tokens, 0 AS cache_write_1h_tokens,
 0 AS cache_write_unknown_tokens, 0 AS output_tokens, estimated_cost_nano_usd,
 0 AS usage_missing_count, 0 AS partial_count, unpriced_request_count, pricing_partial_count,
 ? + 0 AS bucket_alignment_ms`, UsageFiveMinuteBucketMS, UsageFiveMinuteBucketMS)
}
