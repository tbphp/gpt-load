package requestlog

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gpt-load/internal/automodel"
	"gpt-load/internal/platform/redact"
	"gpt-load/internal/pricing"
	"gpt-load/internal/storage/models"
)

func TestAutoDecisionLogKeepsAnswerCostAndStripsOverridePayload(t *testing.T) {
	event := testEvent("00000000-0000-4000-8000-000000000592")
	event.AutoDecision = &automodel.Decision{Selection: automodel.Selection{EntryID: "auto", EntryName: "auto", PresetID: "medium", TargetModel: "upstream-model", ParameterOverrides: json.RawMessage(`[{"set":{"messages":[{"role":"user","content":"private task"}]}}]`), TaskFingerprint: "private-fingerprint"}, Source: "jev", Status: "selected", Provider: "openrouter", GroupID: 8, ChannelID: "openrouter", CredentialID: 9, RequestedModel: "jev-router", UpstreamModel: "typesafe/jev-latest", PromptVersion: automodel.PromptVersion, Called: true, CostState: "priced", PricingCompleteness: "complete", EstimatedCostNanoUSD: 84000}
	row, err := mapEvent(redact.New(), event)
	if err != nil {
		t.Fatal(err)
	}
	if row.EstimatedCostNanoUSD != event.Usage.Pricing.EstimatedCostNanoUSD || row.DecisionCostNanoUSD != 84000 || row.DecisionPricingCompleteness != "complete" || row.DecisionModel != "typesafe/jev-latest" || row.DecisionGroupID != 8 || row.DecisionChannelID != "openrouter" || row.DecisionCredentialID != 9 || len(row.AutoDecision) == 0 {
		t.Fatalf("decision quote not saved separately: %#v", row)
	}
	if strings.Contains(string(row.AutoDecision), "private task") || strings.Contains(string(row.AutoDecision), "private-fingerprint") {
		t.Fatal("decision log persisted private automatic-selection state")
	}
}

func TestDecisionFeeIsCountedOnceAndAttributedToDecisionRoute(t *testing.T) {
	db, _ := openRequestLogFileDB(t)
	event := testEvent("00000000-0000-4000-8000-000000000593")
	event.AutoDecision = &automodel.Decision{Selection: automodel.Selection{EntryID: "auto", EntryName: "auto", PresetID: "medium", TargetModel: "upstream-model"}, Source: "jev", Status: "selected", Provider: "openrouter", GroupID: 8, ChannelID: "openrouter", CredentialID: 9, RequestedModel: "jev-router", UpstreamModel: "typesafe/jev-latest", PromptVersion: automodel.PromptVersion, Called: true, CostState: "priced", PricingCompleteness: "complete", EstimatedCostNanoUSD: 84000}
	row := mustMapEvent(t, redact.New(), event)
	writer := &gormBatchWriter{db: db}
	for range 2 {
		if err := writer.WriteBatch(t.Context(), []models.RequestLog{row}); err != nil {
			t.Fatal(err)
		}
	}
	service := newRequestLogTestService(db)
	query := UsageQuery{FromMS: row.CompletedAtMS - 1000, ToMS: row.CompletedAtMS + 86400000}
	report, err := service.QueryUsage(context.Background(), query)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.EstimatedCostNanoUSD != 84000 || report.Summary.RequestCount != 1 {
		t.Fatalf("global aggregate = %#v", report.Summary)
	}
	group := uint(7)
	query.GroupID = &group
	report, err = service.QueryUsage(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.EstimatedCostNanoUSD != 0 || report.Summary.RequestCount != 1 {
		t.Fatalf("answer Group aggregate = %#v", report.Summary)
	}
	decisionGroup := uint(8)
	query.GroupID = &decisionGroup
	report, err = service.QueryUsage(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	if report.Summary.EstimatedCostNanoUSD != 84000 || report.Summary.RequestCount != 0 {
		t.Fatalf("decision Group aggregate = %#v", report.Summary)
	}
	query.GroupID = nil
	query.ChannelID = "openrouter"
	report, err = service.QueryUsage(t.Context(), query)
	if err != nil || report.Summary.EstimatedCostNanoUSD != 84000 || report.Summary.RequestCount != 0 {
		t.Fatalf("decision channel aggregate = %#v, %v", report.Summary, err)
	}
	query.ChannelID = ""
	query.GroupID = nil
	query.ToMS = row.CompletedAtMS + 1000
	report, err = service.QueryUsage(t.Context(), query)
	if err != nil || report.Summary.EstimatedCostNanoUSD != 84000 {
		t.Fatalf("minute source lost fee: %#v, %v", report.Summary, err)
	}
	page, err := service.List(t.Context(), ListQuery{CostState: pricing.CostStatePriced, Limit: 10})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("total-cost priced filter lost a billed automatic request: %#v, %v", page, err)
	}
}
