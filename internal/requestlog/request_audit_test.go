package requestlog

import (
	"os"
	"strings"
	"testing"

	"gorm.io/gorm"
	"time"

	"gpt-load/internal/automodel"
	"gpt-load/internal/jev"
	"gpt-load/internal/platform/config"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/requestaudit"
	"gpt-load/internal/storage"
	"gpt-load/internal/storage/models"
)

func TestRequestAuditCallsAreCountedOnceWithoutExtraRequests(t *testing.T) {
	db, _ := openRequestLogFileDB(t)
	testAuditUsage(t, db)
}
func testAuditUsage(t *testing.T, db *gorm.DB) {
	t.Helper()
	event := testEvent("00000000-0000-4000-8000-000000000597")
	event.AutoDecision = &automodel.Decision{Called: true, UpstreamModel: "jev-model", GroupID: 8, ChannelID: "jev", CredentialID: 8, CostState: "priced", PricingCompleteness: "complete", EstimatedCostNanoUSD: 3}
	event.RequestAudit = &requestaudit.Result{Mode: "enforce", Status: "passed", Findings: []requestaudit.Finding{}, Calls: []jev.Observation{
		{Called: true, UpstreamModel: "jev-model", GroupID: 9, ChannelID: "jev", CredentialID: 9, CostState: "priced", PricingCompleteness: "complete", EstimatedCostNanoUSD: 5},
		{Called: true, UpstreamModel: "jev-model", GroupID: 10, ChannelID: "jev", CredentialID: 10, CostState: "priced", PricingCompleteness: "complete", EstimatedCostNanoUSD: 7},
	}}
	row := mustMapEvent(t, redact.New(), event)
	writer := &gormBatchWriter{db: db}
	for range 2 {
		if err := writer.WriteBatch(t.Context(), []models.RequestLog{row}); err != nil {
			t.Fatal(err)
		}
	}
	service := newRequestLogTestService(db)
	for _, span := range []int64{2000, 86400000} {
		report, err := service.QueryUsage(t.Context(), UsageQuery{FromMS: row.CompletedAtMS - 1000, ToMS: row.CompletedAtMS + span})
		if err != nil || report.Summary.EstimatedCostNanoUSD != 15 || report.Summary.RequestCount != 1 {
			t.Fatalf("combined usage: %#v, %v", report.Summary, err)
		}
	}
	group := uint(9)
	report, err := service.QueryUsage(t.Context(), UsageQuery{FromMS: row.CompletedAtMS - 1000, ToMS: row.CompletedAtMS + 86400000, GroupID: &group})
	if err != nil || report.Summary.EstimatedCostNanoUSD != 5 || report.Summary.RequestCount != 0 {
		t.Fatalf("audit attribution: %#v %v", report.Summary, err)
	}
	page, err := service.List(t.Context(), ListQuery{CostState: pricing.CostStatePriced, Limit: 10})
	if err != nil || len(page.Items) != 1 || page.Items[0].TotalPricing.EstimatedCostNanoUSD != 15 || page.Items[0].RequestAudit == nil {
		t.Fatalf("audit log lost: %#v %v", page, err)
	}
	service.Sweep(t.Context(), event.CompletedAt.Add(8*24*time.Hour))
	var remaining int64
	if err := db.Model(&models.RequestAuditUsage{}).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("expired audit usage retained: %d %v", remaining, err)
	}
}

func TestExternalRequestAuditUsageContract(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("GPT_LOAD_DATABASE_TEST_DSN"))
	if dsn == "" {
		t.Skip("GPT_LOAD_DATABASE_TEST_DSN is not set")
	}
	db, err := storage.OpenWithSource(dsn, config.DatabaseSourceExternal)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	testAuditUsage(t, db)
}
