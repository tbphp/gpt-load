package requestlog

import (
	"gorm.io/gorm"

	"gpt-load/internal/storage/models"
)

// 将请求级已保存数据投影为小时聚合相同的统计字段，在数据库内复用总览和分布查询。
func usageRequestLogScope(db *gorm.DB, input UsageQuery) *gorm.DB {
	logs := db.Session(&gorm.Session{NewDB: true}).Model(&models.RequestLog{}).
		Where("completed_at_ms >= ? AND completed_at_ms < ?", input.FromMS, input.ToMS).
		Where("attempt_count > 0")
	if input.GroupID != nil {
		logs = logs.Where("group_id = ?", *input.GroupID)
	}
	if input.ChannelID != "" {
		logs = logs.Where("channel_id = ?", input.ChannelID)
	}
	if input.CredentialID != nil {
		logs = logs.Where("credential_id = ?", *input.CredentialID)
	}
	if input.AccessKeyID != nil {
		logs = logs.Where("access_key_id = ?", *input.AccessKeyID)
	}
	if input.UpstreamModel != "" {
		logs = logs.Where("upstream_model = ?", input.UpstreamModel)
	}
	return db.Session(&gorm.Session{NewDB: true}).
		Table("(?) AS usage_rows", logs.Select(usageRequestLogProjection, UsageFiveMinuteBucketMS, UsageFiveMinuteBucketMS))
}

// 与 usageStatDelta.addRow 保持一致：只统计已完成的最终归属，每个请求仅计一次；
// missing/not_applicable 不累加 Token，missing 也不计入可报价用量的 unpriced 数量。
const usageRequestLogProjection = `
	completed_at_ms - completed_at_ms % ? AS bucket_start_ms,
	group_id, access_key_id, upstream_model AS model,
	1 AS request_count,
	CASE WHEN status = 'success' THEN 1 ELSE 0 END AS success_count,
	CASE WHEN status IN ('error', 'incomplete', 'canceled') THEN 1 ELSE 0 END AS failure_count,
	CASE WHEN usage_state IN ('complete', 'partial') THEN uncached_input_tokens ELSE 0 END AS uncached_input_tokens,
	CASE WHEN usage_state IN ('complete', 'partial') THEN cache_read_tokens ELSE 0 END AS cache_read_tokens,
	CASE WHEN usage_state IN ('complete', 'partial') THEN cache_write_5m_tokens ELSE 0 END AS cache_write_5m_tokens,
	CASE WHEN usage_state IN ('complete', 'partial') THEN cache_write_1h_tokens ELSE 0 END AS cache_write_1h_tokens,
	CASE WHEN usage_state IN ('complete', 'partial') THEN cache_write_unknown_tokens ELSE 0 END AS cache_write_unknown_tokens,
	CASE WHEN usage_state IN ('complete', 'partial') THEN output_tokens ELSE 0 END AS output_tokens,
	estimated_cost_nano_usd,
	CASE WHEN usage_state = 'missing' THEN 1 ELSE 0 END AS usage_missing_count,
	CASE WHEN usage_state = 'partial' THEN 1 ELSE 0 END AS partial_count,
	CASE WHEN usage_state IN ('complete', 'partial') AND cost_state = 'unpriced' THEN 1 ELSE 0 END AS unpriced_request_count,
	CASE WHEN cost_state = 'priced' AND pricing_completeness = 'partial' THEN 1 ELSE 0 END AS pricing_partial_count,
	? + 0 AS bucket_alignment_ms
`
