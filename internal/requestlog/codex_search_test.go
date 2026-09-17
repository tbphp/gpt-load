package requestlog

import (
	"testing"
	"time"

	"gpt-load/internal/execution"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
	"gpt-load/internal/usage"
)

func TestCodexSearchLogsDoNotContributeToModelUsage(t *testing.T) {
	db := openRequestLogQueryDB(t)
	completed := time.Now().UTC().Add(-time.Minute)
	row := aggregationRow(aggregationRequestID(650), completed, 1, "gpt-5")
	row.CredentialID = 42
	row.Operation = string(execution.OperationWebSearch)
	row.UsageState = string(usage.StateNotApplicable)
	row.CostState = string(pricing.CostStateNotApplicable)
	row.PricingCompleteness = string(pricing.CompletenessNotApplicable)
	row.UncachedInputTokens, row.OutputTokens, row.EstimatedCostNanoUSD = 0, 0, 0
	if err := (&gormBatchWriter{db: db}).WriteBatch(t.Context(), []models.RequestLog{row}); err != nil {
		t.Fatal(err)
	}
	assertRequestLogAndUsageStatCounts(t, db, 1, 0)
	assertUsageJournalCount(t, db, 0)
	service := newRequestLogTestService(db)
	report, err := service.QueryUsage(t.Context(), minuteUsageQuery(completed.Add(-time.Minute)))
	if err != nil || report.Summary.RequestCount != 0 {
		t.Fatalf("model usage = %#v, error = %v", report, err)
	}
	account, err := service.QueryCredentialWindowUsage(t.Context(), CredentialWindowUsageQuery{
		CredentialID: row.CredentialID, FromMS: completed.Add(-time.Hour).UnixMilli(),
		ToMS: completed.Add(time.Minute).UnixMilli(), Source: CredentialWindowUsageSourceRequestLogs,
	})
	if err != nil || account.RequestCount != 0 {
		t.Fatalf("account model usage = %#v, error = %v", account, err)
	}
}
