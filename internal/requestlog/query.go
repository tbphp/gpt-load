package requestlog

import (
	"context"
	"encoding/json"
	"fmt"

	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
)

const defaultListLimit = 50

func (service *Service) List(ctx context.Context, input ListQuery) (Page, error) {
	limit := input.Limit
	if limit <= 0 {
		limit = defaultListLimit
	}

	query := service.db.WithContext(ctx).
		Model(&models.RequestLog{}).
		Order("created_at DESC").
		Order("id DESC").
		Limit(limit + 1)
	if input.From != nil {
		query = query.Where("created_at >= ?", input.From.UTC())
	}
	if input.To != nil {
		query = query.Where("created_at < ?", input.To.UTC())
	}
	if input.GroupID != nil {
		query = query.Where(`
			EXISTS (
				SELECT 1
				FROM json_each(COALESCE(request_logs.attempts, '[]')) AS attempt
				WHERE json_type(attempt.value, '$.group_id') = 'integer'
					AND json_extract(attempt.value, '$.group_id') = ?
			)
		`, *input.GroupID)
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
	if input.Cursor != nil {
		query = query.Where(
			"created_at < ? OR (created_at = ? AND id < ?)",
			input.Cursor.CompletedAt.UTC(),
			input.Cursor.CompletedAt.UTC(),
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

	page := Page{Items: records}
	if hasNext {
		last := records[len(records)-1]
		page.NextCursor = &Cursor{
			CompletedAt: last.CompletedAt,
			RequestID:   last.RequestID,
		}
	}
	return page, nil
}

func decodeRequestLogRows(rows []models.RequestLog) ([]Record, error) {
	records := make([]Record, 0, len(rows))
	for _, row := range rows {
		attempts := make([]Attempt, 0)
		if len(row.Attempts) > 0 && string(row.Attempts) != "null" {
			if err := json.Unmarshal(row.Attempts, &attempts); err != nil {
				return nil, fmt.Errorf("decode request log attempts: %w", err)
			}
			if attempts == nil {
				attempts = make([]Attempt, 0)
			}
		}
		records = append(records, Record{
			RequestID:     row.ID,
			CompletedAt:   row.CreatedAt.UTC(),
			AccessKey:     AccessKeyRef{ID: row.AccessKeyID, Deleted: true},
			Protocol:      protocol.Protocol(row.Protocol),
			ClientModel:   row.ClientModel,
			UpstreamModel: row.UpstreamModel,
			Status:        telemetry.RequestStatus(row.Status),
			StatusCode:    row.StatusCode,
			DurationMs:    row.DurationMs,
			ErrorCode:     row.ErrorCode,
			ErrorSummary:  row.ErrorSummary,
			AffinityHit:   row.AffinityHit,
			Attempts:      attempts,
		})
	}
	return records, nil
}

func (service *Service) loadAccessKeyRefs(ctx context.Context, records []Record) error {
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
	if err := service.db.WithContext(ctx).
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
