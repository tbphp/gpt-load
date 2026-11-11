package requestlog

import (
	"testing"
	"time"

	"gpt-load/internal/channel"
	"gpt-load/internal/execution"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/protocol"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/telemetry"
	"gpt-load/internal/usage"
)

func TestCredentialAttemptStatsExcludeManualProbes(t *testing.T) {
	t.Parallel()
	completedAt := time.Date(2026, time.August, 9, 1, 0, 0, 0, time.UTC)
	deltas, err := buildCredentialAttemptStatDeltas([]models.RequestLogAttempt{{
		CompletedAtMS:   completedAt.UTC().UnixMilli(),
		CredentialID:    11,
		Operation:       string(execution.OperationProbe),
		DispatchState:   string(execution.DispatchMaybeSent),
		FailureCategory: string(telemetry.FailureCategoryAmbiguous),
	}})
	if err != nil {
		t.Fatalf("buildCredentialAttemptStatDeltas() error = %v", err)
	}
	if len(deltas) != 0 {
		t.Fatalf("credential attempt deltas = %#v, want none for a manual probe", deltas)
	}
}

func TestUsageAggregationExcludeManualProbes(t *testing.T) {
	t.Parallel()
	completedAt := time.Date(2026, time.August, 9, 1, 0, 0, 0, time.UTC)
	row := aggregationRow("probe-row", completedAt, 7, "gpt-4o")
	row.Operation = string(execution.OperationProbe)
	journals, err := buildUsageAggregationJournals([]models.RequestLog{row})
	if err != nil {
		t.Fatalf("buildUsageAggregationJournals() error = %v", err)
	}
	if len(journals) != 0 {
		t.Fatalf("usage journals = %#v, want none for a manual probe", journals)
	}
}

// TestProbeEventProjectsIntoRequestLogRow 保证凭据测试事件能真正落库。
// 投影失败时写入方会静默丢弃这行请求日志，探测记录将完全不可见。
func TestProbeEventProjectsIntoRequestLogRow(t *testing.T) {
	t.Parallel()
	completedAt := time.Date(2026, time.August, 9, 1, 0, 0, 0, time.UTC)
	event := telemetry.RequestEvent{
		RequestID:     "00000000-0000-4000-8000-000000000901",
		CompletedAt:   completedAt,
		Protocol:      protocol.OpenAICompletions,
		Operation:     execution.OperationProbe,
		ClientModel:   "gpt-4o",
		UpstreamModel: "gpt-4o",
		Status:        telemetry.RequestStatusError,
		ErrorCode:     "internal_error",
		ErrorSummary:  "failed to unmarshal response from provider API",
		DurationMs:    1060,
		Usage: telemetry.UsageObservation{
			GroupID:         7,
			ChannelID:       channel.OpenAI,
			CredentialID:    11,
			AttemptSequence: 1,
			Result:          usage.Result{State: usage.StateNotApplicable},
			Pricing: telemetry.PricingObservation{
				UpstreamModel:       "gpt-4o",
				CostState:           string(pricing.CostStateNotApplicable),
				PricingCompleteness: string(pricing.CompletenessNotApplicable),
			},
		},
		Attempts: []telemetry.Attempt{{
			Sequence:         1,
			CompletedAt:      completedAt,
			GroupID:          7,
			GroupName:        "probe-group",
			ChannelID:        channel.OpenAI,
			CredentialID:     11,
			Operation:        execution.OperationProbe,
			RouteMode:        channel.RouteNative,
			UpstreamModel:    "gpt-4o",
			DispatchState:    execution.DispatchMaybeSent,
			UpstreamProtocol: protocol.OpenAICompletions,
			StatusCode:       200,
			DurationMs:       1060,
			FailureCategory:  telemetry.FailureCategoryAmbiguous,
			FailureOrigin:    execution.ErrorOriginInternal,
			FailureScope:     execution.ErrorScopeGroup,
			RetryDirective:   telemetry.RetryNone,
			Effect:           telemetry.EffectNone,
			RuleID:           "fallback.ambiguous",
			Action:           telemetry.ActionTerminate,
			ErrorCode:        "internal_error",
			ErrorSummary:     "failed to unmarshal response from provider API",
		}},
	}
	row, err := mapEvent(redact.New(), event)
	if err != nil {
		t.Fatalf("mapEvent() error = %v, want the probe event to be persisted", err)
	}
	if row.Operation != string(execution.OperationProbe) {
		t.Fatalf("operation = %q, want %q", row.Operation, execution.OperationProbe)
	}
	if row.GroupID != 7 || row.CredentialID != 11 {
		t.Fatalf("row attribution = group %d credential %d, want group 7 credential 11",
			row.GroupID, row.CredentialID)
	}
	if row.Status != string(telemetry.RequestStatusError) {
		t.Fatalf("status = %q, want error", row.Status)
	}
	if len(row.AttemptRows) != 1 || row.AttemptRows[0].FailureOrigin != string(execution.ErrorOriginInternal) {
		t.Fatalf("attempt rows = %#v, want one internal-origin attempt", row.AttemptRows)
	}
}
