package demo

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"

	"gpt-load/internal/app"
	"gpt-load/internal/control"
	"gpt-load/internal/storage/models"
)

type seededLoader struct {
	loader  app.RuntimeStateLoader
	service *control.Service
	db      *gorm.DB
}

func (seed seededLoader) Load(ctx context.Context) error {
	if err := seed.loader.Load(ctx); err != nil {
		return err
	}
	// 通过现有应用服务创建配置，保持加密、校验和运行时发布规则一致。
	var groups []control.GroupCreateResult
	for _, raw := range []string{
		`{"name":"Demo · OpenAI","channel_id":"openai","connection_type":"api_key","params":{},"models":[{"id":"gpt-4.1","alias":"","alias_enabled":false},{"id":"gpt-4.1-mini","alias":"","alias_enabled":false}],"credentials":"sk-demo-openai-01\nsk-demo-openai-02"}`,
		`{"name":"Demo · Anthropic","channel_id":"anthropic","connection_type":"api_key","params":{},"models":[{"id":"claude-sonnet-4-5","alias":"","alias_enabled":false}],"credentials":"sk-demo-anthropic-01"}`,
		`{"name":"Demo · Gemini","channel_id":"gemini","connection_type":"api_key","params":{},"models":[{"id":"gemini-2.5-flash","alias":"","alias_enabled":false}],"credentials":"demo-gemini-01"}`,
	} {
		var request control.GroupCreateRequest
		if err := json.Unmarshal([]byte(raw), &request); err != nil {
			return err
		}
		group, err := seed.service.CreateGroup(ctx, request)
		if err != nil {
			return fmt.Errorf("seed demo group: %w", err)
		}
		groups = append(groups, group)
	}
	for _, name := range []string{"Demo · Development", "Demo · Application"} {
		if _, err := seed.service.CreateAccessKey(ctx, control.AccessKeyCreateRequest{Name: name}); err != nil {
			return fmt.Errorf("seed demo access key: %w", err)
		}
	}
	return seed.seedLogs(ctx, groups)
}

func (seed seededLoader) seedLogs(ctx context.Context, groups []control.GroupCreateResult) error {
	var accessKeys []models.AccessKey
	if err := seed.db.WithContext(ctx).Order("id").Find(&accessKeys).Error; err != nil {
		return err
	}
	now := time.Now().UTC()
	channels := []string{"openai", "anthropic", "gemini"}
	protocols := []string{"openai-completions", "anthropic", "gemini"}
	modelNames := []string{"gpt-4.1", "claude-sonnet-4-5", "gemini-2.5-flash"}
	return seed.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for index, group := range groups {
			var credential models.Credential
			if err := tx.Where("group_id = ?", group.GroupID).Order("id").First(&credential).Error; err != nil {
				return err
			}
			// 每小时一条合成记录，覆盖近七天，时间随每次初始化更新。
			for hour := 0; hour < 7*24; hour++ {
				at := now.Add(-time.Duration(hour) * time.Hour)
				input := int64(800 + (hour*73+index*151)%2400)
				output := int64(150 + (hour*31+index*59)%600)
				cost := input*2000 + output*8000
				row := models.RequestLog{
					ID: fmt.Sprintf("00000000-0000-4000-8000-%012d", index*7*24+hour+1), CompletedAtMS: at.UnixMilli(),
					AccessKeyID: accessKeys[index%len(accessKeys)].ID,
					GroupID:     group.GroupID, ChannelID: channels[index], CredentialID: credential.ID,
					Protocol: protocols[index], Operation: "chat_completion",
					ClientModel: modelNames[index], UpstreamModel: modelNames[index],
					UpstreamReportedModel: modelNames[index], ModelConsistency: "match",
					Status: "success", StatusCode: 200, Stream: true,
					DurationMs: int64(500 + hour%13*90), AttemptCount: 1,
					UncachedInputTokens: input, OutputTokens: output,
					EstimatedCostNanoUSD: cost, UsageState: "complete", CostState: "priced", PricingCompleteness: "complete",
				}
				attempt := models.RequestLogAttempt{
					RequestID: row.ID, Sequence: 1, CompletedAtMS: row.CompletedAtMS,
					GroupID: group.GroupID, GroupName: group.GroupName, ChannelID: row.ChannelID,
					CredentialID: credential.ID, Operation: row.Operation, RouteMode: "native",
					UpstreamModel: row.UpstreamModel, UpstreamProtocol: row.Protocol,
					DispatchState: "maybe_sent", ResponseStarted: true, StatusCode: 200,
					DurationMs: row.DurationMs, FailureCategory: "ok", Action: "terminate", Committed: true,
				}
				stat := models.UsageStat{
					BucketStartMS: at.Truncate(time.Hour).UnixMilli(), AccessKeyID: row.AccessKeyID,
					ChannelID: row.ChannelID, GroupID: row.GroupID, CredentialID: row.CredentialID,
					Model: row.UpstreamModel, RequestCount: 1, SuccessCount: 1,
					UncachedInputTokens: input, OutputTokens: output, EstimatedCostNanoUSD: cost,
				}
				if hour%17 == 8 {
					row.Status, row.StatusCode = "error", 502
					row.ModelConsistency, row.UpstreamReportedModel = "not_applicable", ""
					row.ErrorCode, row.ErrorSummary = "demo_upstream_error", "Synthetic demo upstream failure"
					attempt.StatusCode, attempt.FailureCategory = 502, "upstream_host_error"
					attempt.ErrorCode, attempt.ErrorSummary = row.ErrorCode, row.ErrorSummary
					stat.SuccessCount, stat.FailureCount = 0, 1
				}
				for _, record := range []any{&row, &attempt, &stat} {
					if err := tx.Create(record).Error; err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
}
