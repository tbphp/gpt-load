package requestlog

import (
	"fmt"
	"math"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gpt-load/internal/platform/epochms"
	"gpt-load/internal/storage/models"
)

func writeAutoDecisionUsage(tx *gorm.DB, rows []models.RequestLog) error {
	for _, row := range rows {
		if row.DecisionModel == "" || row.DecisionPricingCompleteness == "not_applicable" {
			continue
		}
		stat := models.AutoDecisionUsageStat{
			BucketStartMS: row.CompletedAtMS - row.CompletedAtMS%epochms.MillisecondsPerHour,
			AccessKeyID:   row.AccessKeyID, GroupID: row.DecisionGroupID,
			ChannelID: row.DecisionChannelID, CredentialID: row.DecisionCredentialID,
			Model: row.DecisionModel, EstimatedCostNanoUSD: row.DecisionCostNanoUSD,
		}
		if row.DecisionPricingCompleteness == "unavailable" && !(row.CostState == "unpriced" && (row.UsageState == "complete" || row.UsageState == "partial")) {
			stat.UnpricedRequestCount = 1
		}
		if row.DecisionPricingCompleteness == "partial" && row.PricingCompleteness != "partial" {
			stat.PricingPartialCount = 1
		}
		updates := map[string]any{}
		for name, amount := range map[string]int64{"estimated_cost_nano_usd": stat.EstimatedCostNanoUSD, "unpriced_request_count": stat.UnpricedRequestCount, "pricing_partial_count": stat.PricingPartialCount} {
			column := clause.Column{Name: name, Table: clause.CurrentTable}
			updates[name] = gorm.Expr("CASE WHEN ? > ? THEN -1 ELSE ? + ? END", column, math.MaxInt64-amount, column, amount)
		}
		if err := tx.Clauses(clause.OnConflict{Columns: []clause.Column{
			{Name: "bucket_start_ms"}, {Name: "access_key_id"}, {Name: "group_id"},
			{Name: "channel_id"}, {Name: "credential_id"}, {Name: "model"},
		}, DoUpdates: clause.Assignments(updates)}).Create(&stat).Error; err != nil {
			return fmt.Errorf("save automatic decision usage: %w", err)
		}
	}
	return nil
}

func decisionUsageScope(db *gorm.DB, input UsageQuery, groupIDs ...uint) *gorm.DB {
	scope := db.Session(&gorm.Session{NewDB: true}).Model(&models.AutoDecisionUsageStat{}).Where("bucket_start_ms >= ? AND bucket_start_ms < ?", input.FromMS, input.ToMS)
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
	return scope
}

const decisionUsageProjection = `bucket_start_ms, group_id, access_key_id, model,
	0 AS request_count, 0 AS success_count, 0 AS failure_count,
	0 AS uncached_input_tokens, 0 AS cache_read_tokens, 0 AS cache_write_5m_tokens,
	0 AS cache_write_1h_tokens, 0 AS cache_write_unknown_tokens, 0 AS output_tokens,
	estimated_cost_nano_usd, 0 AS usage_missing_count, 0 AS partial_count,
	unpriced_request_count, pricing_partial_count, channel_id, credential_id`

const decisionRequestProjection = `completed_at_ms - completed_at_ms % ? AS bucket_start_ms,
	decision_group_id AS group_id, access_key_id, decision_model AS model,
	0 AS request_count, 0 AS success_count, 0 AS failure_count,
	0 AS uncached_input_tokens, 0 AS cache_read_tokens, 0 AS cache_write_5m_tokens,
	0 AS cache_write_1h_tokens, 0 AS cache_write_unknown_tokens, 0 AS output_tokens,
	decision_cost_nano_usd AS estimated_cost_nano_usd, 0 AS usage_missing_count, 0 AS partial_count,
	CASE WHEN decision_pricing_completeness = 'unavailable' AND NOT (cost_state = 'unpriced' AND usage_state IN ('complete','partial')) THEN 1 ELSE 0 END AS unpriced_request_count,
	CASE WHEN decision_pricing_completeness = 'partial' AND pricing_completeness <> 'partial' THEN 1 ELSE 0 END AS pricing_partial_count,
	? + 0 AS bucket_alignment_ms`

func decisionRequestScope(db *gorm.DB, input UsageQuery, groupIDs ...uint) *gorm.DB {
	scope := db.Session(&gorm.Session{NewDB: true}).Model(&models.RequestLog{}).
		Where("completed_at_ms >= ? AND completed_at_ms < ? AND decision_model <> '' AND decision_pricing_completeness <> 'not_applicable'", input.FromMS, input.ToMS)
	if len(groupIDs) > 0 {
		scope = scope.Where("decision_group_id IN ?", groupIDs)
	}
	if input.GroupID != nil {
		scope = scope.Where("decision_group_id = ?", *input.GroupID)
	}
	if input.ChannelID != "" {
		scope = scope.Where("decision_channel_id = ?", input.ChannelID)
	}
	if input.CredentialID != nil {
		scope = scope.Where("decision_credential_id = ?", *input.CredentialID)
	}
	if input.AccessKeyID != nil {
		scope = scope.Where("access_key_id = ?", *input.AccessKeyID)
	}
	if input.UpstreamModel != "" {
		scope = scope.Where("decision_model = ?", input.UpstreamModel)
	}
	return scope.Select(decisionRequestProjection, UsageFiveMinuteBucketMS, UsageFiveMinuteBucketMS)
}
