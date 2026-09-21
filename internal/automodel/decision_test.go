package automodel

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"gpt-load/internal/usage"
)

func compiledDecisionFixture(t *testing.T) (*Compiled, CompiledEntry) {
	t.Helper()
	config := DefaultConfig()
	config.Model = "jev-router"
	config.Models = []Entry{Template()}
	compiled, err := Compile(config, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, _ := compiled.Lookup("auto")
	return compiled, entry
}

func TestBuildDecisionRequestPreservesTaskAndHidesTargetModels(t *testing.T) {
	compiled, entry := compiledDecisionFixture(t)
	payload, decision := BuildDecisionRequest(compiled, entry.Presets, TaskState{CurrentTask: "最新任务"})
	if len(payload) == 0 || decision.RequestedModel != "jev-router" {
		t.Fatalf("payload=%s decision=%#v", payload, decision)
	}
	if strings.Contains(string(payload), "gpt-5.6") || strings.Contains(string(payload), `"uncertain"`) ||
		!strings.Contains(string(payload), "最新任务") {
		t.Fatalf("decision payload violates task/preset boundary: %s", payload)
	}
}

func TestInterpretDecisionResponseAcceptsConfiguredChoiceAtAnyConfidence(t *testing.T) {
	compiled, entry := compiledDecisionFixture(t)
	_, decision := BuildDecisionRequest(compiled, entry.Presets, TaskState{CurrentTask: "task"})
	result := InterpretDecisionResponse(
		decision,
		entry.Presets,
		http.StatusOK,
		http.Header{"X-Request-Id": {"decision-1"}},
		[]byte(`{"model":"jev-pinned","answers":{"preset":{"choice":"low","confidence":0.01}},"usage":{"input_tokens":100,"output_tokens":1,"cost":0.0000042}}`),
		usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 100, Output: 1}},
	)
	if result.Status != "selected" || result.Source != "jev" || result.Choice != "low" ||
		result.Confidence == nil || *result.Confidence != 0.01 || result.ReportedModel != "jev-pinned" ||
		result.ProviderCostUSD != "0.0000042" || result.InputTokens == nil || *result.InputTokens != 100 ||
		result.OutputTokens == nil || *result.OutputTokens != 1 || result.RequestID != "decision-1" {
		encoded, _ := json.Marshal(result)
		t.Fatalf("decision = %s", encoded)
	}
}

func TestInterpretDecisionResponseKeepsUsageOnInvalidAnswer(t *testing.T) {
	compiled, entry := compiledDecisionFixture(t)
	_, decision := BuildDecisionRequest(compiled, entry.Presets, TaskState{CurrentTask: "hello"})
	result := InterpretDecisionResponse(
		decision,
		entry.Presets,
		http.StatusOK,
		http.Header{},
		[]byte(`{"answers":{"preset":{"choice":"unconfigured","confidence":0.9}},"usage":{"input_tokens":2000,"output_tokens":0}}`),
		usage.Result{State: usage.StateComplete, Tokens: usage.Tokens{UncachedInput: 2000}},
	)
	if result.Status != "fallback" || result.Reason != "invalid_choice" ||
		result.InputTokens == nil || *result.InputTokens != 2000 || result.OutputTokens == nil || *result.OutputTokens != 0 {
		t.Fatalf("invalid selection discarded billed usage: %#v", result)
	}
}
